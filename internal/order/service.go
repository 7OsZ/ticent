package order

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"ticent/internal/event"
	"time"

	"gorm.io/gorm"
)

// Daftar error bisnis Checkout. Handler memakai ini untuk memilih status HTTP.
var (
	ErrValidation     = errors.New("Validasi gagal")                     // isi keranjang salah (400)
	ErrDayNotFound    = errors.New("Hari tidak tersedia")                // hari tidak ada / belum publish / sudah lewat (404)
	ErrSaleNotStarted = errors.New("Penjualan belum dibuka")             // belum jam war (409)
	ErrQuotaExceeded  = errors.New("Melebihi batas tiket per akun")      // jatah beli habis (409)
	ErrSoldOut        = errors.New("Stok tidak cukup")                   // tiket habis (409)
	ErrPendingExists  = errors.New("Masih ada order yang belum dibayar") // order pending lain di event ini (409)
)

// Service berisi aturan bisnis order.
type Service struct {
	repo   *Repository    // query order, stok, dan kuota
	events *event.Service // untuk membaca hari + kategori + batas tiket
}

func NewService(repo *Repository, events *event.Service) *Service {
	return &Service{repo: repo, events: events}
}

// ItemInput = satu baris keranjang. Contoh: kategori VIP, 2 tiket.
type ItemInput struct {
	CategoryID int64
	Qty        int
}

// CheckoutInput = isi keranjang saat user menekan "Checkout". Satu checkout hanya untuk SATU hari.
type CheckoutInput struct {
	EventDayID int64
	Items      []ItemInput
}

// Checkout menahan tiket selama 30 menit sambil menunggu pembayaran.
func (s *Service) Checkout(userID int64, in CheckoutInput) (*Order, error) {
	now := time.Now()

	// Ambil hari yang mau dibeli. GetCatalogDay sudah memastikan event-nya published dan tanggalnya belum lewat, dan ikut membawa kategori + event.
	day, err := s.events.GetCatalogDay(in.EventDayID)
	if errors.Is(err, event.ErrDayNotFound) {
		return nil, ErrDayNotFound
	}
	if err != nil {
		return nil, err
	}
	ev := day.Event // data event: batas tiket per akun, jam war

	// 2) Belum jam war = belum boleh beli.
	if now.Before(ev.SaleStartsAt) {
		return nil, ErrSaleNotStarted
	}

	// Periksa isi keranjang
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("%w: Keranjang kosong", ErrValidation)
	}

	// Peta kategori milik hari ini, supaya cepat dicari berdasarkan ID. Sekaligus mencegah user membeli kategori milik hari lain lewat ID palsu.
	cats := make(map[int64]event.Category, len(day.Categories))
	for _, c := range day.Categories {
		cats[c.ID] = c
	}

	seen := make(map[int64]bool) // untuk mendeteksi kategori yang ditulis dua kali
	total := 0                   // jumlah tiket dikeranjang
	var amount int64
	items := make([]OrderItem, 0, len(in.Items))

	for _, it := range in.Items {
		if it.Qty < 1 {
			return nil, fmt.Errorf("%w: Jumlah tiket minimal 1", ErrValidation)
		}
		if seen[it.CategoryID] {
			return nil, fmt.Errorf("%w: Kategori yang sama ditulis dua kali", ErrValidation)
		}
		seen[it.CategoryID] = true

		c, ok := cats[it.CategoryID]
		if !ok {
			return nil, fmt.Errorf("%w: Kategori %d tidak ada", ErrValidation, it.CategoryID)
		}

		total += it.Qty
		amount += c.Price * int64(it.Qty) // harga x jumlah, dijumlahkan
		items = append(items, OrderItem{
			CategoryID: c.ID,
			Qty:        it.Qty,
			UnitPrice:  c.Price, // harga disalin sekarang, untuk bukti transaksi
		})
	}

	// Total keranjang tidak boleh melebihi batas event (mis. 4).
	if total > ev.MaxTicketsPerUser {
		return nil, ErrQuotaExceeded
	}

	// Urutkan keranjang berdasarkan ID kategori, dari kecil ke besar.
	slices.SortFunc(items, func(a, b OrderItem) int {
		return int(a.CategoryID - b.CategoryID)
	})

	order := &Order{
		UserID:          userID,
		EventID:         ev.ID,
		EventDayID:      day.ID,
		Status:          StatusPending, // wajib diisi: string kosong ditolak CHECK
		TotalAmount:     amount,
		MidtransOrderID: "TCT-" + rand.Text(),                                    // kode acak unik, mis. TCT-6NQ2... (dipakai Modul 5)
		HoldExpiresAt:   now.Add(time.Duration(event.HoldMinutes) * time.Minute), // 30 menit dari sekarang
		Items:           items,
	}

	// Satu paket transaksi: jatah beli, stok, dan order.
	err = s.repo.InTx(func(tx *gorm.DB) error {
		// Jatah beli dulu, baru stok. Alasannya: baris kuota hanya milik user ini, jadi hampir tidak pernah rebutan. Kalau jatah sudah habis,
		// kita langsung berhenti tanpa sempat menyentuh baris stok yang sedang diperebutkan ratusan orang.
		ok, err := s.repo.ReserveQuota(tx, userID, ev.ID, total, ev.MaxTicketsPerUser)
		if err != nil {
			return err
		}
		if !ok {
			return ErrQuotaExceeded
		}

		// Kurangi stok tiap kategori, sesuai urutan yang sudah diurutkan tadi.
		for _, it := range items {
			ok, err := s.repo.DecrementStock(tx, it.CategoryID, it.Qty)
			if err != nil {
				return err
			}
			if !ok {
				// Satu kategori kurang = seluruh checkout batal (termasuk jatah beli tadi).
				return fmt.Errorf("%w: %s", ErrSoldOut, cats[it.CategoryID].Name)
			}
		}

		// Simpan order + isinya.
		if err := s.repo.CreateOrder(tx, order); err != nil {
			// Index "satu order pending per user per event" menolak order kedua.
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrPendingExists
			}
			return err
		}
		return nil // semua berhasil -> commit
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}
