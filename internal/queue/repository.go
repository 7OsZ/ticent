package queue

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// InTx menjalankan fn sebagai satu transaksi: semua berhasil, atau semua batal.
func (r *Repository) InTx(fn func(tx *gorm.DB) error) error {
	return r.db.Transaction(fn)
}

// CreateEntry memasukkan satu orang ke antrean event.
func (r *Repository) CreateEntry(e *Entry) error {
	return r.db.Create(e).Error
}

// FindLiveEntry mengambil entri antrean yang masih hidup milik user di satu event.
// Hidup = masih antre, sedang belanja, atau sedang bayar. Tidak ada = (nil, nil).
func (r *Repository) FindLiveEntry(eventID, userID int64) (*Entry, error) {
	var e Entry
	err := r.db.
		Where("event_id = ? AND user_id = ? AND status IN ?", eventID, userID,
			[]string{StatusWaiting, StatusActive, StatusCheckout}).
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// FindLatestEntry mengambil entri terbaru milik user di satu event, apa pun statusnya.
// Dipakai SSE untuk mengirim kabar terakhir, mis. "expired" setelah tab lama tertidur.
func (r *Repository) FindLatestEntry(eventID, userID int64) (*Entry, error) {
	var e Entry
	err := r.db.
		Where("event_id = ? AND user_id = ?", eventID, userID).
		Order("id DESC").
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Touch mencatat "user ini masih membuka halaman antrean" (heartbeat).
// Hasil false = tidak ada entri yang masih antre/belanja.
func (r *Repository) Touch(eventID, userID int64, now time.Time) (bool, error) {
	res := r.db.Exec(
		`UPDATE queue_entries SET last_seen_at = ?
		  WHERE event_id = ? AND user_id = ? AND status IN (?, ?)`,
		now, eventID, userID, StatusWaiting, StatusActive,
	)
	return res.RowsAffected == 1, res.Error
}

// Leave mengeluarkan user dari antrean. Kalau dia sedang belanja,
// slotnya otomatis lepas karena admitter hanya menghitung status active.
func (r *Repository) Leave(eventID, userID int64) (bool, error) {
	res := r.db.Exec(
		`UPDATE queue_entries SET status = ?
		  WHERE event_id = ? AND user_id = ? AND status IN (?, ?)`,
		StatusLeft, eventID, userID, StatusWaiting, StatusActive,
	)
	return res.RowsAffected == 1, res.Error
}

// ListEventsToProcess mencari event yang perlu diurus worker:
// sudah published, sudah lewat jam war, dan masih ada orang yang antre atau belanja.
func (r *Repository) ListEventsToProcess(now time.Time) ([]int64, error) {
	var ids []int64
	err := r.db.Raw(
		`SELECT id FROM events
		  WHERE status = 'published' AND sale_starts_at <= ?
		    AND EXISTS (SELECT 1 FROM queue_entries q
		                 WHERE q.event_id = events.id AND q.status IN (?, ?))
		  ORDER BY id`,
		now, StatusWaiting, StatusActive,
	).Scan(&ids).Error
	return ids, err
}

// eventState = angka-angka antrean milik satu event, dibaca sambil dikunci.
type eventState struct {
	QueueTail         int // nomor antrean terakhir yang sudah dibagikan
	AdmittedUpTo      int // nomor terbesar yang sudah lolos
	MaxActiveSessions int // batas orang belanja bersamaan, mis. 100
	SessionMinutes    int // lama sesi belanja, mis. 30
}

// LockEvent mengunci baris event (FOR UPDATE) selama satu putaran worker.
// Hanya worker antrean yang mengunci baris events (D-24).
func (r *Repository) LockEvent(tx *gorm.DB, eventID int64) (*eventState, error) {
	var s eventState
	err := tx.Raw(
		`SELECT queue_tail, admitted_up_to, max_active_sessions, session_minutes
		   FROM events WHERE id = ? FOR UPDATE`,
		eventID,
	).Scan(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ExpireStale menghanguskan entri yang sudah tidak hadir:
//   - sedang belanja tapi sesinya habis atau tanpa heartbeat, atau
//   - masih antre tapi tanpa heartbeat.
//
// seenBefore = batas heartbeat terakhir (now - HeartbeatTimeout).
func (r *Repository) ExpireStale(tx *gorm.DB, eventID int64, now, seenBefore time.Time) (int64, error) {
	res := tx.Exec(
		`UPDATE queue_entries SET status = ?
		  WHERE event_id = ?
		    AND ((status = ? AND (session_expires_at < ? OR last_seen_at < ?))
		      OR (status = ? AND last_seen_at < ?))`,
		StatusExpired, eventID,
		StatusActive, now, seenBefore,
		StatusWaiting, seenBefore,
	)
	return res.RowsAffected, res.Error
}

// Dua versi SQL pembagian nomor. ORDER BY tidak bisa diisi lewat parameter "?",
// jadi dipilih salah satu dari dua teks tetap ini (bukan disusun dari input user).
const (
	assignRandomSQL = `
		WITH numbered AS (
		  SELECT id, row_number() OVER (ORDER BY random()) AS rn
		    FROM queue_entries
		   WHERE event_id = ? AND status = 'waiting' AND position IS NULL
		)
		UPDATE queue_entries q SET position = ? + numbered.rn
		  FROM numbered WHERE q.id = numbered.id`

	assignFIFOSQL = `
		WITH numbered AS (
		  SELECT id, row_number() OVER (ORDER BY joined_at, id) AS rn
		    FROM queue_entries
		   WHERE event_id = ? AND status = 'waiting' AND position IS NULL
		)
		UPDATE queue_entries q SET position = ? + numbered.rn
		  FROM numbered WHERE q.id = numbered.id`
)

// AssignPositions membagikan nomor antrean ke semua yang belum punya nomor,
// mulai dari tail + 1. random = true untuk putaran jam war (semua yang sudah
// menunggu diacak), false untuk yang datang setelahnya (urut waktu datang).
func (r *Repository) AssignPositions(tx *gorm.DB, eventID int64, tail int, random bool) (int64, error) {
	query := assignFIFOSQL
	if random {
		query = assignRandomSQL
	}
	res := tx.Exec(query, eventID, tail)
	return res.RowsAffected, res.Error
}

// AdvanceTail memajukan nomor terakhir sebanyak n nomor yang baru dibagikan.
func (r *Repository) AdvanceTail(tx *gorm.DB, eventID int64, n int64) error {
	return tx.Exec(
		`UPDATE events SET queue_tail = queue_tail + ? WHERE id = ?`,
		n, eventID,
	).Error
}

// CountActive menghitung orang yang sedang belanja (memakai slot).
func (r *Repository) CountActive(tx *gorm.DB, eventID int64) (int64, error) {
	var n int64
	err := tx.Model(&Entry{}).
		Where("event_id = ? AND status = ?", eventID, StatusActive).
		Count(&n).Error
	return n, err
}

// Admit meloloskan paling banyak limit orang dengan nomor terkecil.
// Mengembalikan nomor-nomor yang lolos, mis. [101, 102, 104] (103 sudah keluar).
func (r *Repository) Admit(tx *gorm.DB, eventID int64, limit int, now, sessionEnd time.Time) ([]int, error) {
	var positions []int
	err := tx.Raw(
		`WITH next AS (
		   SELECT id FROM queue_entries
		    WHERE event_id = ? AND status = ? AND position IS NOT NULL
		    ORDER BY position
		    LIMIT ?
		    FOR UPDATE
		 )
		 UPDATE queue_entries q
		    SET status = ?, admitted_at = ?, session_expires_at = ?
		   FROM next WHERE q.id = next.id
		 RETURNING q.position`,
		eventID, StatusWaiting, limit,
		StatusActive, now, sessionEnd,
	).Scan(&positions).Error
	return positions, err
}

// AdvanceAdmitted memajukan penanda "sudah lolos sampai nomor berapa".
// GREATEST supaya penanda tidak pernah mundur.
func (r *Repository) AdvanceAdmitted(tx *gorm.DB, eventID int64, upTo int) error {
	return tx.Exec(
		`UPDATE events SET admitted_up_to = GREATEST(admitted_up_to, ?) WHERE id = ?`,
		upTo, eventID,
	).Error
}

// EnterCheckout dipakai modul order saat checkout. User harus sedang belanja
// dan sesinya belum habis. Status jadi checkout, slot langsung lepas untuk orang berikutnya.
// Hasil false = belum lolos antrean atau sesi sudah habis.
func (r *Repository) EnterCheckout(tx *gorm.DB, userID, eventID int64, now time.Time) (bool, error) {
	res := tx.Exec(
		`UPDATE queue_entries SET status = ?
		  WHERE event_id = ? AND user_id = ? AND status = ? AND session_expires_at > ?`,
		StatusCheckout, eventID, userID, StatusActive, now,
	)
	return res.RowsAffected == 1, res.Error
}

// FinishCheckout menutup entri yang sedang checkout: done (sudah bayar)
// atau expired (hold hangus, user harus antre ulang).
func (r *Repository) FinishCheckout(tx *gorm.DB, userID, eventID int64, status string) error {
	return tx.Exec(
		`UPDATE queue_entries SET status = ?
		  WHERE event_id = ? AND user_id = ? AND status = ?`,
		status, eventID, userID, StatusCheckout,
	).Error
}
