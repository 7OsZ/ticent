package payment

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"ticent/internal/auth"
	"ticent/internal/event"
	"ticent/internal/order"
	"ticent/internal/queue"
	"ticent/internal/ticket"

	"gorm.io/gorm"
)

var (
	ErrOrderNotFound    = errors.New("Order tidak ditemukan")
	ErrOrderNotPayable  = errors.New("Order sudah tidak menunggu pembayaran")
	ErrInvalidSignature = errors.New("Tanda tangan notifikasi tidak valid")
	ErrAmountMismatch   = errors.New("Nominal pembayaran tidak cocok")
)

type PayResult struct {
	SnapToken   string `json:"snap_token"`
	RedirectURL string `json:"redirect_url"`
}

type NotificationPayload struct {
	OrderID           string `json:"order_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
}

type Service struct {
	orderRepo  *order.Repository
	orders     *order.Service
	queueRepo  *queue.Repository
	ticketRepo *ticket.Repository
	authRepo   *auth.Repository
	gateway    Gateway
	serverKey  string
}

func NewService(
	orderRepo *order.Repository,
	orders *order.Service,
	queueRepo *queue.Repository,
	ticketRepo *ticket.Repository,
	authRepo *auth.Repository,
	gateway Gateway,
	serverKey string,
) *Service {
	return &Service{
		orderRepo:  orderRepo,
		orders:     orders,
		queueRepo:  queueRepo,
		ticketRepo: ticketRepo,
		authRepo:   authRepo,
		gateway:    gateway,
		serverKey:  serverKey,
	}
}

// Pay membuat transaksi Midtrans Snap untuk order pending, atau mengembalikan token lama bila sudah pernah dibuat.
func (s *Service) Pay(userID, orderID int64) (*PayResult, error) {
	now := time.Now()
	ord, err := s.orders.GetOrder(userID, orderID)
	if errors.Is(err, order.ErrOrderNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if ord.Status != order.StatusPending || !now.Before(ord.HoldExpiresAt) {
		return nil, ErrOrderNotPayable
	}
	if ord.SnapToken != nil && *ord.SnapToken != "" {
		return &PayResult{SnapToken: *ord.SnapToken, RedirectURL: deref(ord.SnapRedirectURL)}, nil
	}
	// Durasi pembayaran Snap = HoldMinutes - 2 menit dari waktu order dibuat (A-04).
	snapDeadline := ord.CreatedAt.Add(time.Duration(event.HoldMinutes-2) * time.Minute)
	if !now.Before(snapDeadline) {
		return nil, ErrOrderNotPayable
	}

	user, err := s.authRepo.FindByID(userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrOrderNotFound
	}

	token, redirectURL, err := s.gateway.CreateTransaction(SnapRequest{
		OrderID:      ord.MidtransOrderID,
		GrossAmount:  ord.TotalAmount,
		CustomerName: user.FullName,
		CustomerMail: user.Email,
		StartTime:    ord.CreatedAt,
		DurationMin:  event.HoldMinutes - 2,
	})
	if err != nil {
		return nil, err
	}

	saved, err := s.orderRepo.SaveSnapToken(ord.ID, token, redirectURL)
	if err != nil {
		return nil, err
	}
	if !saved {
		// Request paralel mungkin lebih dulu menyimpan token atau status order sudah berubah.
		latest, err := s.orders.GetOrder(userID, orderID)
		if err != nil {
			return nil, err
		}
		if latest.Status == order.StatusPending && latest.SnapToken != nil && *latest.SnapToken != "" {
			return &PayResult{SnapToken: *latest.SnapToken, RedirectURL: deref(latest.SnapRedirectURL)}, nil
		}
		return nil, ErrOrderNotPayable
	}

	return &PayResult{SnapToken: token, RedirectURL: redirectURL}, nil
}

// HandleNotification memverifikasi tanda tangan SHA-512 Midtrans lalu memproses perubahan status secara idempoten.
func (s *Service) HandleNotification(n NotificationPayload) error {
	if !s.validSignature(n) {
		return ErrInvalidSignature
	}
	return s.applyStatus(n.OrderID, n.GrossAmount, n.TransactionStatus, n.FraudStatus)
}

// SyncOrder menarik status terbaru langsung dari Midtrans (D-32: alur lokal tanpa tunnel) lalu memprosesnya
// lewat jalur yang persis sama dengan webhook.
func (s *Service) SyncOrder(userID, orderID int64) (*order.Order, error) {
	ord, err := s.orders.GetOrder(userID, orderID)
	if errors.Is(err, order.ErrOrderNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if ord.Status == order.StatusPaid || ord.Status == order.StatusFailed {
		return ord, nil
	}

	st, err := s.gateway.CheckTransaction(ord.MidtransOrderID)
	if err != nil {
		return nil, err
	}
	if st.StatusCode != "404" && st.TransactionStatus != "" {
		if err := s.applyStatus(ord.MidtransOrderID, st.GrossAmount, st.TransactionStatus, st.FraudStatus); err != nil {
			return nil, err
		}
	}
	return s.orders.GetOrder(userID, orderID)
}

func (s *Service) applyStatus(midtransOrderID, grossAmount, txStatus, fraudStatus string) error {
	now := time.Now()
	ord, err := s.orderRepo.FindByMidtransID(midtransOrderID)
	if err != nil {
		return err
	}
	if ord == nil {
		return ErrOrderNotFound
	}
	switch {
	case txStatus == "settlement" || (txStatus == "capture" && fraudStatus == "accept"):
		if !matchesGrossAmount(grossAmount, ord.TotalAmount) {
			return ErrAmountMismatch
		}
		return s.settleOrder(ord, now)
	case txStatus == "deny" || txStatus == "cancel" || txStatus == "expire" || txStatus == "failure":
		if grossAmount != "" && !matchesGrossAmount(grossAmount, ord.TotalAmount) {
			return ErrAmountMismatch
		}
		return s.failOrder(ord)
	default:
		// pending / challenge: belum final, abaikan dengan aman.
		if grossAmount != "" && !matchesGrossAmount(grossAmount, ord.TotalAmount) {
			return ErrAmountMismatch
		}
		return nil
	}
}

func (s *Service) settleOrder(ord *order.Order, now time.Time) error {
	if ord.Status == order.StatusPaid {
		return nil
	}

	paid := false
	err := s.orderRepo.InTx(func(tx *gorm.DB) error {
		// Urutan kunci D-24: orders -> queue_entries -> categories (urut ID) -> tickets.
		ok, err := s.orderRepo.MarkPaid(tx, ord.MidtransOrderID, now)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.queueRepo.FinishCheckout(tx, ord.UserID, ord.EventID, queue.StatusDone); err != nil {
			return err
		}

		items := slices.Clone(ord.Items)
		order.SortByCategory(items)

		var tickets []ticket.Ticket
		for _, it := range items {
			startSeat, hasSeats, err := s.ticketRepo.AllocateSeats(tx, it.CategoryID, it.Qty)
			if err != nil {
				return err
			}
			for k := range it.Qty {
				var seatPtr *int
				if hasSeats {
					seat := startSeat + k
					seatPtr = &seat
				}
				tickets = append(tickets, ticket.Ticket{
					OrderItemID: it.ID,
					CategoryID:  it.CategoryID,
					SeatNo:      seatPtr,
					Code:        "TKT-" + rand.Text(),
				})
			}
		}
		if err := s.ticketRepo.CreateTickets(tx, tickets); err != nil {
			return err
		}
		paid = true
		return nil
	})
	if err != nil {
		return err
	}
	if !paid {
		// Bila order sudah keburu hangus saat pembayaran masuk, tandai failed tanpa menerbitkan tiket (FR-P3).
		markedFailed, err := s.orderRepo.MarkFailedFromExpired(ord.MidtransOrderID)
		if err != nil {
			return err
		}
		if markedFailed {
			slog.Warn("pembayaran setelah hangus, tiket tidak diterbitkan", "midtrans_order_id", ord.MidtransOrderID)
		}
	}
	return nil
}

func (s *Service) failOrder(ord *order.Order) error {
	if ord.Status != order.StatusPending {
		return nil
	}
	return s.orderRepo.InTx(func(tx *gorm.DB) error {
		ok, err := s.orderRepo.MarkFailedFromPending(tx, ord.MidtransOrderID)
		if err != nil || !ok {
			return err
		}
		return s.orders.ReleaseHold(tx, ord.ID)
	})
}

func (s *Service) validSignature(n NotificationPayload) bool {
	if s.serverKey == "" || n.OrderID == "" || n.SignatureKey == "" {
		return false
	}
	sum := sha512.Sum512([]byte(n.OrderID + n.StatusCode + n.GrossAmount + s.serverKey))
	expected := hex.EncodeToString(sum[:])
	got := strings.ToLower(strings.TrimSpace(n.SignatureKey))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
}

func matchesGrossAmount(grossAmount string, total int64) bool {
	base := strconv.FormatInt(total, 10)
	return grossAmount == base || grossAmount == fmt.Sprintf("%s.00", base)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
