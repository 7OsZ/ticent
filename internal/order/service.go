package order

import (
	"cmp"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"ticent/internal/event"
	"ticent/internal/queue"
	"time"

	"gorm.io/gorm"
)

// Daftar error bisnis Checkout. Handler memakai ini untuk memilih status HTTP.
var (
	ErrValidation     = errors.New("Validasi gagal")                                         // isi keranjang salah (400)
	ErrDayNotFound    = errors.New("Hari tidak tersedia")                                    // hari tidak ada / belum publish / sudah lewat (404)
	ErrSaleNotStarted = errors.New("Penjualan belum dibuka")                                 // belum jam war (409)
	ErrQuotaExceeded  = errors.New("Melebihi batas tiket per akun")                          // jatah beli habis (409)
	ErrSoldOut        = errors.New("Stok tidak cukup")                                       // tiket habis (409)
	ErrPendingExists  = errors.New("Masih ada order yang belum dibayar")                     // order pending lain di event ini (409)
	ErrOrderNotFound  = errors.New("Order tidak ditemukan")                                  // tidak ada / bukan milik user ini (404)
	ErrNotAdmitted    = errors.New("Kamu belum lolos antrean atau sesi belanja sudah habis") // (409)
)

// Service berisi aturan bisnis order.
type Service struct {
	repo   *Repository       // query order, stok, dan kuota
	events *event.Service    // untuk membaca hari + kategori + batas tiket
	queue  *queue.Repository // gerbang antrean: hanya yang sudah lolos boleh checkout
}

func NewService(repo *Repository, events *event.Service, queueRepo *queue.Repository) *Service {
	return &Service{repo: repo, events: events, queue: queueRepo}
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
	sortByCategory(items)

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

	// Satu paket transaksi: order, antrean, jatah beli, lalu stok.
	// Urutan ini sama di checkout, sweeper, dan pembayaran (D-24), supaya tidak deadlock.
	// Baris yang paling diperebutkan (stok kategori) disentuh paling akhir:
	// kalau user ternyata belum lolos antrean atau jatahnya habis, kita berhenti
	// tanpa sempat mengunci stok yang sedang diincar ratusan orang.
	err = s.repo.InTx(func(tx *gorm.DB) error {
		if err := s.repo.CreateOrder(tx, order); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrPendingExists
			}
			return err
		}
		// Gerbang antrean: harus sedang belanja (active) dan sesinya belum habis.
		// Berhasil = status jadi checkout dan slot langsung lepas untuk nomor berikutnya.
		// Kalau langkah sesudahnya gagal (mis. stok habis), transaksi batal dan status kembali active.
		admitted, err := s.queue.EnterCheckout(tx, userID, ev.ID, now)
		if err != nil {
			return err
		}
		if !admitted {
			return ErrNotAdmitted
		}
		ok, err := s.repo.ReserveQuota(tx, userID, ev.ID, total, ev.MaxTicketsPerUser)
		if err != nil {
			return err
		}
		if !ok {
			return ErrQuotaExceeded // order dan status antrean ikut dibatalkan
		}
		for _, it := range items {
			ok, err := s.repo.DecrementStock(tx, it.CategoryID, it.Qty)
			if err != nil {
				return err
			}
			if !ok {
				// Satu kategori kurang = order, jatah beli, dan stok kategori lain ikut batal.
				return fmt.Errorf("%w: %s", ErrSoldOut, cats[it.CategoryID].Name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// GetOrder mengambil order milik user yang sedang login.
func (s *Service) GetOrder(userID, orderID int64) (*Order, error) {
	o, err := s.repo.FindUserOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrderNotFound
	}
	return o, nil
}

// sortByCategory mengurutkan isi order dari ID kategori terkecil ke terbesar.
func sortByCategory(items []OrderItem) {
	slices.SortFunc(items, func(a, b OrderItem) int {
		return cmp.Compare(a.CategoryID, b.CategoryID)
	})
}

const sweepBatch = 100

func (s *Service) ExpireOverdue(now time.Time) (int, error) {
	ids, err := s.repo.ListExpiredPendingIDs(now, sweepBatch)
	if err != nil {
		return 0, err
	}

	expired := 0
	var errs []error // kumpulan error, supaya satu kegagalan tidak menghentikan yang lain
	for _, id := range ids {
		done, err := s.expireOne(id, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("order %d: %w", id, err))
			continue // lanjut ke order berikutnya
		}
		if done {
			expired++
		}
	}
	// errors.Join menggabungkan semua error jadi satu. Hasilnya nil kalau tidak ada error.
	return expired, errors.Join(errs...)
}

// expireOne menghanguskan SATU order dan mengembalikan tiket + jatah belinya,
func (s *Service) expireOne(orderID int64, now time.Time) (bool, error) {
	done := false
	err := s.repo.InTx(func(tx *gorm.DB) error {
		// a) Kunci order: ubah ke expired, hanya kalau masih pending dan sudah lewat waktu.
		ok, err := s.repo.ExpireOrder(tx, orderID, now)
		if err != nil {
			return err
		}
		if !ok {
			// Order sudah dibayar atau sudah disapu duluan. Tidak ada yang perlu dikembalikan.
			return nil
		}

		// Ambil isi order untuk tahu apa saja yang harus dikembalikan.
		o, err := s.repo.FindOrderForRelease(tx, orderID)
		if err != nil {
			return err
		}

		// Tutup entri antrean: user harus antre ulang untuk mencoba lagi (D-08).
		if err := s.queue.FinishCheckout(tx, o.UserID, o.EventID, queue.StatusExpired); err != nil {
			return err
		}

		// Kembalikan jatah beli (jumlah semua tiket di order ini).
		total := 0
		for _, it := range o.Items {
			total += it.Qty
		}
		if err := s.repo.ReleaseQuota(tx, o.UserID, o.EventID, total); err != nil {
			return err
		}

		// Kembalikan stok tiap kategori, dengan urutan ID yang sama seperti Checkout.
		sortByCategory(o.Items)
		for _, it := range o.Items {
			if err := s.repo.IncrementStock(tx, it.CategoryID, it.Qty); err != nil {
				return err
			}
		}

		done = true
		return nil // order expired, jatah dan stok kembali
	})
	return done, err
}
