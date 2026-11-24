package queue_test

import (
	"sync"
	"testing"
	"time"

	"ticent/internal/event"
	"ticent/internal/platform/testutil"
	"ticent/internal/queue"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestQueueSimulation: 1.000 orang antre untuk event dengan 50 slot belanja.
// Worker berputar berkali-kali sementara sebagian orang yang sudah lolos keluar
// atau checkout di saat yang sama. Yang dibuktikan di setiap putaran:
//   - orang yang sedang belanja tidak pernah melebihi 50;
//   - nomor antrean unik;
//   - tidak ada user dengan dua entri hidup.
//
// Jalankan hanya bila diminta:
//
//	go test ./internal/queue/ -run TestQueueSimulation -v -count=1
func TestQueueSimulation(t *testing.T) {
	const users, maxActive, rounds = 1000, 50, 30

	db := testutil.DB(t)
	seed := testutil.SeedEvent(t, db, maxActive, 100)
	ids := testutil.SeedUsers(t, db, users)

	// Semua join sebelum jam war: belum punya nomor. Heartbeat baru saja dikirim.
	now := time.Now()
	entries := make([]queue.Entry, 0, users)
	for _, uid := range ids {
		entries = append(entries, queue.Entry{
			EventID: seed.EventID, UserID: uid, Status: queue.StatusWaiting,
			JoinedAt: now, LastSeenAt: now,
		})
	}
	require.NoError(t, db.CreateInBatches(&entries, 500).Error)

	repo := queue.NewRepository(db)
	svc := queue.NewService(repo, event.NewService(event.NewRepository(db)))

	for round := 1; round <= rounds; round++ {
		// Ambil beberapa orang yang sedang belanja untuk "mengganggu" putaran ini.
		var active []int64
		require.NoError(t, db.Raw(
			`SELECT user_id FROM queue_entries WHERE event_id = ? AND status = 'active'
			  ORDER BY random() LIMIT 20`, seed.EventID).Scan(&active).Error)

		var wg sync.WaitGroup
		for i, uid := range active {
			wg.Go(func() {
				if i%2 == 0 {
					_ = svc.Leave(uid, seed.EventID) // keluar sendiri
					return
				}
				// Checkout: status jadi checkout, slot lepas (sama seperti modul order).
				_ = repo.InTx(func(tx *gorm.DB) error {
					_, err := repo.EnterCheckout(tx, uid, seed.EventID, time.Now())
					return err
				})
			})
		}
		// Worker berputar BERSAMAAN dengan keluar/checkout di atas.
		_, err := svc.RunRound(time.Now())
		require.NoError(t, err)
		wg.Wait()

		var activeCount int64
		require.NoError(t, db.Raw(
			`SELECT COUNT(*) FROM queue_entries WHERE event_id = ? AND status = 'active'`,
			seed.EventID).Scan(&activeCount).Error)
		require.LessOrEqual(t, activeCount, int64(maxActive), "putaran %d: sesi aktif melebihi batas", round)
	}

	// Nomor antrean: semua 1.000 orang dapat nomor 1..1000, tanpa kembar.
	var numbered, distinct, maxPos, tail, admittedUpTo int64
	require.NoError(t, db.Raw(`SELECT COUNT(position), COUNT(DISTINCT position), COALESCE(MAX(position), 0)
		FROM queue_entries WHERE event_id = ?`, seed.EventID).Row().Scan(&numbered, &distinct, &maxPos))
	require.NoError(t, db.Raw(`SELECT queue_tail, admitted_up_to FROM events WHERE id = ?`,
		seed.EventID).Row().Scan(&tail, &admittedUpTo))
	require.EqualValues(t, users, numbered)
	require.Equal(t, numbered, distinct, "nomor antrean kembar")
	require.EqualValues(t, users, maxPos)
	require.EqualValues(t, users, tail)
	require.LessOrEqual(t, admittedUpTo, tail)

	// Satu user hanya punya satu entri hidup.
	var dup int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM (
		SELECT user_id FROM queue_entries
		 WHERE event_id = ? AND status IN ('waiting', 'active', 'checkout')
		 GROUP BY user_id HAVING COUNT(*) > 1) d`, seed.EventID).Scan(&dup).Error)
	require.Zero(t, dup, "ada user dengan dua entri hidup")
}

func TestPresenceThrottlesTouchWrites(t *testing.T) {
	db := testutil.DB(t)
	seed := testutil.SeedEvent(t, db, 10, 50)
	ids := testutil.SeedUsers(t, db, 1)
	uid := ids[0]

	repo := queue.NewRepository(db)
	svc := queue.NewService(repo, event.NewService(event.NewRepository(db)))

	recent := time.Now().Add(-5 * time.Second).Round(time.Microsecond)
	require.NoError(t, repo.CreateEntry(&queue.Entry{
		EventID: seed.EventID, UserID: uid, Status: queue.StatusWaiting,
		JoinedAt: recent, LastSeenAt: recent,
	}))

	// Baru 5 detik sejak heartbeat terakhir (< 20 detik): Presence tidak perlu UPDATE ke DB.
	_, err := svc.Presence(uid, seed.EventID)
	require.NoError(t, err)

	var seen time.Time
	require.NoError(t, db.Raw(`SELECT last_seen_at FROM queue_entries WHERE user_id = ?`, uid).Scan(&seen).Error)
	require.True(t, seen.Equal(recent), "last_seen_at tidak boleh di-update tiap tick 2 detik")

	// Sudah 25 detik (>= 20 detik): Presence wajib memperbarui last_seen_at sebelum timeout 60 detik.
	stale := time.Now().Add(-25 * time.Second).Round(time.Microsecond)
	require.NoError(t, db.Exec(`UPDATE queue_entries SET last_seen_at = ? WHERE user_id = ?`, stale, uid).Error)

	_, err = svc.Presence(uid, seed.EventID)
	require.NoError(t, err)

	require.NoError(t, db.Raw(`SELECT last_seen_at FROM queue_entries WHERE user_id = ?`, uid).Scan(&seen).Error)
	require.True(t, seen.After(stale), "last_seen_at wajib diperbarui saat melewati batas throttle")
}
