package order

import "gorm.io/gorm"

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
