package order_test

import (
	"errors"
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
