package order_test

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ticent/internal/event"
	"ticent/internal/order"
	"ticent/internal/platform/testutil"
	"ticent/internal/queue"

	"github.com/stretchr/testify/require"
)

// TestCheckoutNoOversell: 200 pembeli menekan "Checkout" di detik yang sama
// untuk 50 tiket Festival. Hasil yang benar: tepat 50 berhasil, 150 kehabisan,
// stok akhir 0. Jalankan hanya bila diminta:
//
//	go test ./internal/order/ -run TestCheckoutNoOversell -v -count=1
func TestCheckoutNoOversell(t *testing.T) {
	const buyers, stock = 200, 50

	db := testutil.DB(t)
	seed := testutil.SeedEvent(t, db, buyers, stock)
	users := testutil.SeedUsers(t, db, buyers)

	// Semua pembeli sudah lolos antrean dan sesinya masih berlaku (gerbang 4.6).
	now := time.Now()
	err := db.Exec(
		`INSERT INTO queue_entries (event_id, user_id, position, status, joined_at,
		                            last_seen_at, admitted_at, session_expires_at)
		 SELECT ?, u.id, row_number() OVER (ORDER BY u.id), 'active', ?, ?, ?, ?
		   FROM users u`,
		seed.EventID, now, now, now, now.Add(30*time.Minute),
	).Error
	require.NoError(t, err)

	svc := order.NewService(
		order.NewRepository(db),
		event.NewService(event.NewRepository(db)),
		queue.NewRepository(db),
	)

	var ok, soldOut, other atomic.Int64
	start := make(chan struct{}) // semua goroutine menunggu "pistol start" yang sama
	var wg sync.WaitGroup
	for _, uid := range users {
		wg.Go(func() {
			<-start
			_, err := svc.Checkout(uid, order.CheckoutInput{
				EventDayID: seed.DayID,
				Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 1}},
			})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, order.ErrSoldOut):
				soldOut.Add(1)
			default:
				other.Add(1)
				t.Log("error lain:", err)
			}
		})
	}
	close(start)
	wg.Wait()

	require.EqualValues(t, stock, ok.Load(), "jumlah checkout sukses")
	require.EqualValues(t, buyers-stock, soldOut.Load(), "jumlah yang kehabisan")
	require.Zero(t, other.Load(), "tidak boleh ada error lain")

	// Bukti di database, bukan hanya hitungan di Go.
	var available, sold, used, inCheckout int64
	require.NoError(t, db.Raw(`SELECT available FROM categories WHERE id = ?`, seed.CategoryID).Scan(&available).Error)
	require.NoError(t, db.Raw(`SELECT COALESCE(SUM(qty), 0) FROM order_items`).Scan(&sold).Error)
	require.NoError(t, db.Raw(`SELECT COALESCE(SUM(used), 0) FROM user_event_quota`).Scan(&used).Error)
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM queue_entries WHERE status = 'checkout'`).Scan(&inCheckout).Error)

	require.Zero(t, available, "stok akhir")
	require.EqualValues(t, stock, sold, "total tiket di order_items")
	require.EqualValues(t, stock, used, "total kuota terpakai")
	// Yang gagal karena stok habis tetap active: transaksi batal, status antrean ikut kembali.
	require.EqualValues(t, stock, inCheckout, "entri antrean berstatus checkout")
}

func TestCheckoutQtyOverflowRejected(t *testing.T) {
	db := testutil.DB(t)
	seed := testutil.SeedEvent(t, db, 10, 50)
	users := testutil.SeedUsers(t, db, 1)
	uid := users[0]

	var cat2ID int64
	require.NoError(t, db.Raw(
		`INSERT INTO categories (event_day_id, name, price, total_seats, available, has_seats)
		 VALUES (?, 'VIP', 1000000, 50, 50, true) RETURNING id`, seed.DayID,
	).Scan(&cat2ID).Error)

	now := time.Now()
	require.NoError(t, db.Exec(
		`INSERT INTO queue_entries (event_id, user_id, position, status, joined_at, last_seen_at, admitted_at, session_expires_at)
		 VALUES (?, ?, 1, 'active', ?, ?, ?, ?)`,
		seed.EventID, uid, now, now, now, now.Add(30*time.Minute),
	).Error)

	svc := order.NewService(
		order.NewRepository(db),
		event.NewService(event.NewRepository(db)),
		queue.NewRepository(db),
	)

	_, err := svc.Checkout(uid, order.CheckoutInput{
		EventDayID: seed.DayID,
		Items: []order.ItemInput{
			{CategoryID: seed.CategoryID, Qty: math.MaxInt},
			{CategoryID: cat2ID, Qty: 2},
		},
	})
	require.ErrorIs(t, err, order.ErrQuotaExceeded)
}

func TestCheckoutQuotaAndRetryNeverExceedsLimit(t *testing.T) {
	db := testutil.DB(t)
	seed := testutil.SeedEvent(t, db, 10, 50) // max_tickets_per_user = 4
	users := testutil.SeedUsers(t, db, 1)
	uid := users[0]

	now := time.Now()
	// Simulasikan user sudah membeli lunas 3 tiket di event ini, lalu antre lagi untuk sisa kuota (1 tiket).
	require.NoError(t, db.Exec(`INSERT INTO user_event_quota (user_id, event_id, used) VALUES (?, ?, 3)`, uid, seed.EventID).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO queue_entries (event_id, user_id, position, status, joined_at, last_seen_at, admitted_at, session_expires_at)
		 VALUES (?, ?, 1, 'active', ?, ?, ?, ?)`,
		seed.EventID, uid, now, now, now, now.Add(30*time.Minute),
	).Error)

	svc := order.NewService(
		order.NewRepository(db),
		event.NewService(event.NewRepository(db)),
		queue.NewRepository(db),
	)

	// 1) Minta 2 tiket paralel (3 + 2 = 5 > 4): semuanya wajib gagal, used tetap 3.
	start1 := make(chan struct{})
	var wg1 sync.WaitGroup
	for range 10 {
		wg1.Go(func() {
			<-start1
			_, err := svc.Checkout(uid, order.CheckoutInput{
				EventDayID: seed.DayID,
				Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 2}},
			})
			require.Error(t, err)
		})
	}
	close(start1)
	wg1.Wait()

	var used int
	require.NoError(t, db.Raw(`SELECT used FROM user_event_quota WHERE user_id = ? AND event_id = ?`, uid, seed.EventID).Scan(&used).Error)
	require.Equal(t, 3, used)

	// 2) Retry paralel 10 kali minta 1 tiket: tepat 1 order pending dibuat, tidak ada order ganda, used = 4.
	var ok, conflict, other atomic.Int64
	start2 := make(chan struct{})
	var wg2 sync.WaitGroup
	for range 10 {
		wg2.Go(func() {
			<-start2
			_, err := svc.Checkout(uid, order.CheckoutInput{
				EventDayID: seed.DayID,
				Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 1}},
			})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, order.ErrPendingExists), errors.Is(err, order.ErrNotAdmitted), errors.Is(err, order.ErrQuotaExceeded):
				conflict.Add(1)
			default:
				other.Add(1)
				t.Log("unexpected retry error:", err)
			}
		})
	}
	close(start2)
	wg2.Wait()

	require.EqualValues(t, 1, ok.Load(), "hanya 1 checkout boleh berhasil")
	require.EqualValues(t, 9, conflict.Load())
	require.Zero(t, other.Load())

	var pendingCount int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM orders WHERE user_id = ? AND status = 'pending'`, uid).Scan(&pendingCount).Error)
	require.EqualValues(t, 1, pendingCount)
	require.NoError(t, db.Raw(`SELECT used FROM user_event_quota WHERE user_id = ? AND event_id = ?`, uid, seed.EventID).Scan(&used).Error)
	require.Equal(t, 4, used)
}

func TestExpireOverdueReleasesStockQuotaAndQueue(t *testing.T) {
	db := testutil.DB(t)
	seed := testutil.SeedEvent(t, db, 10, 50)
	users := testutil.SeedUsers(t, db, 1)
	uid := users[0]

	now := time.Now()
	require.NoError(t, db.Exec(
		`INSERT INTO queue_entries (event_id, user_id, position, status, joined_at, last_seen_at, admitted_at, session_expires_at)
		 VALUES (?, ?, 1, 'active', ?, ?, ?, ?)`,
		seed.EventID, uid, now, now, now, now.Add(30*time.Minute),
	).Error)

	evSvc := event.NewService(event.NewRepository(db))
	qRepo := queue.NewRepository(db)
	qSvc := queue.NewService(qRepo, evSvc)
	svc := order.NewService(order.NewRepository(db), evSvc, qRepo)

	ord, err := svc.Checkout(uid, order.CheckoutInput{
		EventDayID: seed.DayID,
		Items:      []order.ItemInput{{CategoryID: seed.CategoryID, Qty: 2}},
	})
	require.NoError(t, err)

	n, err := svc.ExpireOverdue(time.Now().Add(31 * time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := svc.GetOrder(uid, ord.ID)
	require.NoError(t, err)
	require.Equal(t, order.StatusExpired, got.Status)

	var available, used int
	var qStatus string
	require.NoError(t, db.Raw(`SELECT available FROM categories WHERE id = ?`, seed.CategoryID).Scan(&available).Error)
	require.NoError(t, db.Raw(`SELECT used FROM user_event_quota WHERE user_id = ? AND event_id = ?`, uid, seed.EventID).Scan(&used).Error)
	require.NoError(t, db.Raw(`SELECT status FROM queue_entries WHERE user_id = ? AND event_id = ?`, uid, seed.EventID).Scan(&qStatus).Error)
	require.Equal(t, 50, available, "stok harus kembali utuh")
	require.Equal(t, 0, used, "kuota harus kembali ke 0")
	require.Equal(t, queue.StatusExpired, qStatus, "entri antrean harus expired")

	// Setelah hangus, user boleh join antrean ulang (FR-Q7).
	_, err = qSvc.Join(uid, seed.EventID)
	require.NoError(t, err)
}
