package payment_test

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ticent/internal/auth"
	"ticent/internal/event"
	"ticent/internal/order"
	"ticent/internal/payment"
	"ticent/internal/platform/httpx"
	"ticent/internal/platform/testutil"
	"ticent/internal/queue"
	"ticent/internal/ticket"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testServerKey = "SB-Mid-server-test-key"
const testJWTSecret = "test-jwt-secret"

type fakeGateway struct {
	mu          sync.Mutex
	createCalls int
	lastReq     payment.SnapRequest
	statusByID  map[string]*payment.StatusResult
}

func (f *fakeGateway) CreateTransaction(req payment.SnapRequest) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.lastReq = req
	return "snap-tok-" + req.OrderID, "https://app.sandbox.midtrans.com/snap/v2/vtweb/" + req.OrderID, nil
}

func (f *fakeGateway) CheckTransaction(midtransOrderID string) (*payment.StatusResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st, ok := f.statusByID[midtransOrderID]; ok {
		return st, nil
	}
	return &payment.StatusResult{OrderID: midtransOrderID, StatusCode: "404", TransactionStatus: "pending"}, nil
}

type paymentHarness struct {
	db        *gorm.DB
	authSvc   *auth.Service
	orderSvc  *order.Service
	paySvc    *payment.Service
	ticketSvc *ticket.Service
	gw        *fakeGateway
	router    *gin.Engine
}

func newHarness(t *testing.T) *paymentHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)

	authRepo := auth.NewRepository(db)
	authSvc := auth.NewService(authRepo, testJWTSecret)
	evSvc := event.NewService(event.NewRepository(db))
	qRepo := queue.NewRepository(db)
	ordRepo := order.NewRepository(db)
	ordSvc := order.NewService(ordRepo, evSvc, qRepo)
	tktRepo := ticket.NewRepository(db)
	tktSvc := ticket.NewService(tktRepo)
	gw := &fakeGateway{statusByID: make(map[string]*payment.StatusResult)}
	paySvc := payment.NewService(ordRepo, ordSvc, qRepo, tktRepo, authRepo, gw, testServerKey)

	r := gin.New()
	authMW := httpx.AuthRequired(testJWTSecret)
	payment.NewHandler(paySvc).RegisterRoutes(r, authMW)
	ticket.NewHandler(tktSvc).RegisterRoutes(r, authMW)

	return &paymentHarness{
		db:        db,
		authSvc:   authSvc,
		orderSvc:  ordSvc,
		paySvc:    paySvc,
		ticketSvc: tktSvc,
		gw:        gw,
		router:    r,
	}
}

func admitUsers(t *testing.T, db *gorm.DB, eventID int64, userIDs []int64) {
	t.Helper()
	now := time.Now()
	for i, uid := range userIDs {
		require.NoError(t, db.Exec(
			`INSERT INTO queue_entries (event_id, user_id, position, status, joined_at, last_seen_at, admitted_at, session_expires_at)
			 VALUES (?, ?, ?, 'active', ?, ?, ?, ?)`,
			eventID, uid, i+1, now, now, now, now.Add(30*time.Minute),
		).Error)
	}
}

func signMidtrans(orderID, statusCode, grossAmount, serverKey string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}

func TestPayIdempotentAndOwnership(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 10, 50)
	users := testutil.SeedUsers(t, h.db, 2)
	admitUsers(t, h.db, seed.EventID, users[:1])

	ord, err := h.orderSvc.Checkout(users[0], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 2}},
	})
	require.NoError(t, err)

	res1, err := h.paySvc.Pay(users[0], ord.ID)
	require.NoError(t, err)
	require.Equal(t, "snap-tok-"+ord.MidtransOrderID, res1.SnapToken)
	require.Equal(t, event.HoldMinutes-2, h.gw.lastReq.DurationMin)
	require.Equal(t, ord.TotalAmount, h.gw.lastReq.GrossAmount)

	// Panggilan kedua mengembalikan token yang sama tanpa memanggil Midtrans lagi.
	res2, err := h.paySvc.Pay(users[0], ord.ID)
	require.NoError(t, err)
	require.Equal(t, res1, res2)
	require.Equal(t, 1, h.gw.createCalls)

	// User lain tidak boleh membayar order milik user[0].
	_, err = h.paySvc.Pay(users[1], ord.ID)
	require.ErrorIs(t, err, payment.ErrOrderNotFound)
}

func TestWebhookInvalidSignatureReturns403(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 10, 50)
	users := testutil.SeedUsers(t, h.db, 1)
	admitUsers(t, h.db, seed.EventID, users)

	ord, err := h.orderSvc.Checkout(users[0], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 1}},
	})
	require.NoError(t, err)

	body, _ := json.Marshal(payment.NotificationPayload{
		OrderID:           ord.MidtransOrderID,
		StatusCode:        "200",
		GrossAmount:       "500000.00",
		SignatureKey:      "deadbeef",
		TransactionStatus: "settlement",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/payments/midtrans/notification", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)

	got, err := h.orderSvc.GetOrder(users[0], ord.ID)
	require.NoError(t, err)
	require.Equal(t, order.StatusPending, got.Status)
}

func TestWebhookSettlementIdempotentSeatedAndStanding(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 10, 50) // Festival berdiri (500.000)
	var vipCatID int64
	require.NoError(t, h.db.Raw(
		`INSERT INTO categories (event_day_id, name, price, total_seats, available, has_seats)
		 VALUES (?, 'VIP', 1000000, 50, 50, true) RETURNING id`, seed.DayID,
	).Scan(&vipCatID).Error)

	users := testutil.SeedUsers(t, h.db, 1)
	admitUsers(t, h.db, seed.EventID, users)

	ord, err := h.orderSvc.Checkout(users[0], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items: []order.ItemInput{
			{CategoryID: vipCatID, Qty: 2},
			{CategoryID: seed.CategoryID, Qty: 1},
		},
	})
	require.NoError(t, err)

	gross := fmt.Sprintf("%d.00", ord.TotalAmount)
	notif := payment.NotificationPayload{
		OrderID:           ord.MidtransOrderID,
		StatusCode:        "200",
		GrossAmount:       gross,
		SignatureKey:      signMidtrans(ord.MidtransOrderID, "200", gross, testServerKey),
		TransactionStatus: "settlement",
	}

	// Dikirim dua kali: keduanya sukses tanpa membuat tiket ganda.
	require.NoError(t, h.paySvc.HandleNotification(notif))
	require.NoError(t, h.paySvc.HandleNotification(notif))

	got, err := h.orderSvc.GetOrder(users[0], ord.ID)
	require.NoError(t, err)
	require.Equal(t, order.StatusPaid, got.Status)
	require.NotNil(t, got.PaidAt)

	var qStatus string
	require.NoError(t, h.db.Raw(`SELECT status FROM queue_entries WHERE user_id = ?`, users[0]).Scan(&qStatus).Error)
	require.Equal(t, queue.StatusDone, qStatus)

	tickets, err := h.ticketSvc.ListMyTickets(users[0])
	require.NoError(t, err)
	require.Len(t, tickets, 3, "2 VIP + 1 Festival = tepat 3 tiket")

	var vipSeats []int
	standingNil := 0
	for _, tk := range tickets {
		require.Contains(t, tk.Code, "TKT-")
		if tk.CategoryName == "VIP" {
			require.NotNil(t, tk.SeatNo)
			vipSeats = append(vipSeats, *tk.SeatNo)
		} else {
			require.Nil(t, tk.SeatNo, "kategori berdiri tidak punya nomor kursi")
			standingNil++
		}
	}
	slices.Sort(vipSeats)
	require.Equal(t, []int{1, 2}, vipSeats)
	require.Equal(t, 1, standingNil)
}

func TestConcurrentSettlementSeatAllocationNoDuplicates(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 30, 100)
	var vipCatID int64
	require.NoError(t, h.db.Raw(
		`INSERT INTO categories (event_day_id, name, price, total_seats, available, has_seats)
		 VALUES (?, 'VIP', 1000000, 100, 100, true) RETURNING id`, seed.DayID,
	).Scan(&vipCatID).Error)

	const buyers, qtyPerBuyer = 20, 2
	users := testutil.SeedUsers(t, h.db, buyers)
	admitUsers(t, h.db, seed.EventID, users)

	orders := make([]*order.Order, buyers)
	for i, uid := range users {
		ord, err := h.orderSvc.Checkout(uid, order.CheckoutInput{
			EventDayID: seed.DayID,
			Items:      []order.ItemInput{{CategoryID: vipCatID, Qty: qtyPerBuyer}},
		})
		require.NoError(t, err)
		orders[i] = ord
	}

	var errs atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, ord := range orders {
		wg.Go(func() {
			<-start
			gross := strconv.FormatInt(ord.TotalAmount, 10) + ".00"
			err := h.paySvc.HandleNotification(payment.NotificationPayload{
				OrderID:           ord.MidtransOrderID,
				StatusCode:        "200",
				GrossAmount:       gross,
				SignatureKey:      signMidtrans(ord.MidtransOrderID, "200", gross, testServerKey),
				TransactionStatus: "settlement",
			})
			if err != nil {
				errs.Add(1)
				t.Log("settlement error:", err)
			}
		})
	}
	close(start)
	wg.Wait()
	require.Zero(t, errs.Load())

	var seats []int
	require.NoError(t, h.db.Raw(`SELECT seat_no FROM tickets WHERE category_id = ? ORDER BY seat_no`, vipCatID).Scan(&seats).Error)
	require.Len(t, seats, buyers*qtyPerBuyer)
	for i, s := range seats {
		require.Equal(t, i+1, s, "nomor kursi harus berurutan 1..40 tanpa bolong atau kembar")
	}
}

func TestWebhookSettlementAfterExpiredMarksFailedWithoutTickets(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 10, 50)
	users := testutil.SeedUsers(t, h.db, 1)
	admitUsers(t, h.db, seed.EventID, users)

	ord, err := h.orderSvc.Checkout(users[0], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 2}},
	})
	require.NoError(t, err)

	// Sweeper menghanguskan order lebih dulu.
	n, err := h.orderSvc.ExpireOverdue(time.Now().Add(31 * time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// Pembayaran terlambat masuk setelah hold hangus (FR-P3).
	gross := fmt.Sprintf("%d.00", ord.TotalAmount)
	err = h.paySvc.HandleNotification(payment.NotificationPayload{
		OrderID:           ord.MidtransOrderID,
		StatusCode:        "200",
		GrossAmount:       gross,
		SignatureKey:      signMidtrans(ord.MidtransOrderID, "200", gross, testServerKey),
		TransactionStatus: "settlement",
	})
	require.NoError(t, err)

	got, err := h.orderSvc.GetOrder(users[0], ord.ID)
	require.NoError(t, err)
	require.Equal(t, order.StatusFailed, got.Status)

	var ticketCount, available int
	require.NoError(t, h.db.Raw(`SELECT COUNT(*) FROM tickets`).Scan(&ticketCount).Error)
	require.NoError(t, h.db.Raw(`SELECT available FROM categories WHERE id = ?`, seed.CategoryID).Scan(&available).Error)
	require.Zero(t, ticketCount, "tidak boleh menerbitkan tiket setelah hangus")
	require.Equal(t, 50, available, "stok tetap utuh")
}

func TestWebhookDenyReleasesHoldAndSyncEndpoint(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 10, 50)
	users := testutil.SeedUsers(t, h.db, 2)
	admitUsers(t, h.db, seed.EventID, users)

	// 1) User 0 gagal bayar (deny) -> stok, kuota, dan slot antrean lepas.
	ord0, err := h.orderSvc.Checkout(users[0], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 2}},
	})
	require.NoError(t, err)

	gross0 := fmt.Sprintf("%d.00", ord0.TotalAmount)
	require.NoError(t, h.paySvc.HandleNotification(payment.NotificationPayload{
		OrderID:           ord0.MidtransOrderID,
		StatusCode:        "202",
		GrossAmount:       gross0,
		SignatureKey:      signMidtrans(ord0.MidtransOrderID, "202", gross0, testServerKey),
		TransactionStatus: "deny",
	}))

	got0, err := h.orderSvc.GetOrder(users[0], ord0.ID)
	require.NoError(t, err)
	require.Equal(t, order.StatusFailed, got0.Status)

	var available, used int
	require.NoError(t, h.db.Raw(`SELECT available FROM categories WHERE id = ?`, seed.CategoryID).Scan(&available).Error)
	require.NoError(t, h.db.Raw(`SELECT used FROM user_event_quota WHERE user_id = ?`, users[0]).Scan(&used).Error)
	require.Equal(t, 50, available)
	require.Equal(t, 0, used)

	// 2) User 1 bayar di sandbox lalu panggil POST /orders/:id/sync + GET /tickets lewat HTTP.
	ord1, err := h.orderSvc.Checkout(users[1], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 2}},
	})
	require.NoError(t, err)

	h.gw.statusByID[ord1.MidtransOrderID] = &payment.StatusResult{
		OrderID:           ord1.MidtransOrderID,
		StatusCode:        "200",
		GrossAmount:       fmt.Sprintf("%d.00", ord1.TotalAmount),
		TransactionStatus: "capture",
		FraudStatus:       "accept",
	}

	tok0, err := h.authSvc.GenerateToken(&auth.User{ID: users[0], Role: "user"})
	require.NoError(t, err)
	tok1, err := h.authSvc.GenerateToken(&auth.User{ID: users[1], Role: "user"})
	require.NoError(t, err)

	recSync := httptest.NewRecorder()
	reqSync := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/orders/%d/sync", ord1.ID), nil)
	reqSync.Header.Set("Authorization", "Bearer "+tok1)
	h.router.ServeHTTP(recSync, reqSync)
	require.Equal(t, http.StatusOK, recSync.Code)

	var synced order.Order
	require.NoError(t, json.Unmarshal(recSync.Body.Bytes(), &synced))
	require.Equal(t, order.StatusPaid, synced.Status)

	recTickets1 := httptest.NewRecorder()
	reqTickets1 := httptest.NewRequest(http.MethodGet, "/tickets", nil)
	reqTickets1.Header.Set("Authorization", "Bearer "+tok1)
	h.router.ServeHTTP(recTickets1, reqTickets1)
	require.Equal(t, http.StatusOK, recTickets1.Code)
	var list1 []ticket.TicketView
	require.NoError(t, json.Unmarshal(recTickets1.Body.Bytes(), &list1))
	require.Len(t, list1, 2)
	require.Equal(t, "BMTH Test", list1[0].EventName)
	require.Equal(t, "DAY-1", list1[0].DayLabel)

	// User 0 belum punya tiket: harus menerima array kosong [], bukan null.
	recTickets0 := httptest.NewRecorder()
	reqTickets0 := httptest.NewRequest(http.MethodGet, "/tickets", nil)
	reqTickets0.Header.Set("Authorization", "Bearer "+tok0)
	h.router.ServeHTTP(recTickets0, reqTickets0)
	require.Equal(t, http.StatusOK, recTickets0.Code)
	require.JSONEq(t, `[]`, recTickets0.Body.String())
}

func TestPayLastTwoMinutesAndEmptyGrossAmountRejected(t *testing.T) {
	h := newHarness(t)
	seed := testutil.SeedEvent(t, h.db, 10, 50)
	users := testutil.SeedUsers(t, h.db, 1)
	admitUsers(t, h.db, seed.EventID, users)

	ord, err := h.orderSvc.Checkout(users[0], order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 1}},
	})
	require.NoError(t, err)

	// 1) GrossAmount kosong dari Sync tidak boleh meloloskan pelunasan tanpa cek nominal.
	h.gw.statusByID[ord.MidtransOrderID] = &payment.StatusResult{
		OrderID:           ord.MidtransOrderID,
		StatusCode:        "200",
		GrossAmount:       "",
		TransactionStatus: "settlement",
	}
	_, err = h.paySvc.SyncOrder(users[0], ord.ID)
	require.ErrorIs(t, err, payment.ErrAmountMismatch)

	// 2) Order berusia 29 menit (jendela Snap 28 menit sudah tutup walau hold 30 menit tersisa 1 menit) -> 409.
	created29mAgo := time.Now().Add(-29 * time.Minute)
	require.NoError(t, h.db.Exec(
		`UPDATE orders SET created_at = ?, hold_expires_at = ? WHERE id = ?`,
		created29mAgo, created29mAgo.Add(30*time.Minute), ord.ID,
	).Error)

	_, err = h.paySvc.Pay(users[0], ord.ID)
	require.ErrorIs(t, err, payment.ErrOrderNotPayable)
	require.Zero(t, h.gw.createCalls)
}
