package order

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

// InTx menjalankan fn sebagai SATU paket. Semua perubahan di dalam fn berhasil bersama, atau batal bersama:
//   - fn mengembalikan nil   -> semua disimpan (commit) - fn mengembalikan error -> semua dibatalkan (rollback)
func (r *Repository) InTx(fn func(tx *gorm.DB) error) error {
	return r.db.Transaction(fn)
}

// DecrementStock mencoba mengambil qty tiket dari satu kategori. (contoh: 2 tiket VIP BMTH Day-1). Hasil true = berhasil, false = stok kurang.
func (r *Repository) DecrementStock(tx *gorm.DB, categoryID int64, qty int) (bool, error) {
	res := tx.Exec(
		`UPDATE categories
		    SET available = available - ?
		  WHERE id = ? AND available >= ?`,
		qty, categoryID, qty,
	)
	if res.Error != nil {
		return false, res.Error // masalah database sungguhan (mis. koneksi putus)
	}
	// 1 baris berubah = stok cukup dan sudah dikurangi.
	// 0 baris berubah = stok kurang, atau kategori tidak ada.
	return res.RowsAffected == 1, nil
}

// ReserveQuota mencoba memakai qty jatah beli milik satu user di satu event. Hasil true = jatah masih muat, false = akan melebihi batas (limit).
func (r *Repository) ReserveQuota(tx *gorm.DB, userID, eventID int64, qty, limit int) (bool, error) {
	res := tx.Exec(
		`INSERT INTO user_event_quota (user_id, event_id, used)
		 VALUES (?, ?, ?)
		 ON CONFLICT (user_id, event_id)
		 DO UPDATE SET used = user_event_quota.used + EXCLUDED.used
		 WHERE user_event_quota.used + EXCLUDED.used <= ?`,
		userID, eventID, qty, limit,
	)
	if res.Error != nil {
		return false, res.Error
	}
	// 1 = jatah berhasil dipakai. 0 = catatan sudah ada, tapi totalnya melebihi batas.
	return res.RowsAffected == 1, nil
}

// CreateOrder menyimpan order beserta isinya (Items) dalam paket transaksi yang sama.
func (r *Repository) CreateOrder(tx *gorm.DB, o *Order) error {
	return tx.Create(o).Error
}

// FindUserOrder mengambil satu order milik user tertentu, lengkap dengan isinya.
// Syarat user_id = ? penting: tanpa itu, user A bisa melihat order user B cukup dengan mengganti angka ID di URL.
func (r *Repository) FindUserOrder(userID, orderID int64) (*Order, error) {
	var o Order
	err := r.db.
		Preload("Items"). // ikut ambil isi order (kategori + jumlah tiket)
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&o).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return &o, nil
}

// ListExpiredPendingIDs mencari ID order yang masih pending tapi batas bayarnya sudah lewat

func (r *Repository) ListExpiredPendingIDs(now time.Time, limit int) ([]int64, error) {
	var ids []int64
	err := r.db.Model(&Order{}).
		Where("status = ? AND hold_expires_at < ?", StatusPending, now).
		Order("hold_expires_at").
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// ExpireOrder mengubah status order menjadi expired, TAPI hanya kalau masih pending dan waktunya memang sudah lewat. Hasil true = berhasil diubah.
func (r *Repository) ExpireOrder(tx *gorm.DB, orderID int64, now time.Time) (bool, error) {
	res := tx.Exec(
		`UPDATE orders SET status = ?
		  WHERE id = ? AND status = ? AND hold_expires_at < ?`,
		StatusExpired, orderID, StatusPending, now,
	)

	if res.Error != nil {
		return false, res.Error
	}

	return res.RowsAffected == 1, nil
}

// FindOrderForRelease mengambil order beserta isinya di dalam transaksi, untuk tahu berapa tiket per kategori dan berapa jatah beli yang harus dikembalikan.
func (r *Repository) FindOrderForRelease(tx *gorm.DB, orderID int64) (*Order, error) {
	var o Order
	err := tx.Preload("Items").First(&o, orderID).Error
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// IncrementStock mengembalikan qty tiket ke stok satu kategori.
func (r *Repository) IncrementStock(tx *gorm.DB, categoryID int64, qty int) error {
	return tx.Exec(
		`UPDATE categories SET available = available + ? WHERE id = ?`,
		qty, categoryID,
	).Error
}

// ReleaseQuota mengembalikan qty jatah beli milik user di satu event.
func (r *Repository) ReleaseQuota(tx *gorm.DB, userID, eventID int64, qty int) error {
	return tx.Exec(
		`UPDATE user_event_quota SET used = used - ?
		  WHERE user_id = ? AND event_id = ?`,
		qty, userID, eventID,
	).Error
}
