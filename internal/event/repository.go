package event

import (
	"errors"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db} // koneksi db
}

// Menyimpan Event baru.
func (r *Repository) CreateEvent(e *Event) error {
	return r.db.Create(e).Error
}

// menyimpan satu hari pertunjukan milik sebuah event, "Day-1, 10 Okt".
func (r *Repository) CreateDay(d *EventDay) error {
	return r.db.Create(d).Error
}

// menyimpan satu jenis tiket yang dijual di satu hari,
func (r *Repository) CreateCategory(c *Category) error {
	return r.db.Create(c).Error
}

// FindEventByID mencari event berdasarkan ID-nya. Kalau event tidak ada, hasilnya (nil, nil), bukan error.
func (r *Repository) FindEventByID(id int64) (*Event, error) {
	var e Event
	// First(&e, id) artinya: ambil baris pertama yang id-nya sama.
	err := r.db.First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil // tidak ada event dengan id tsb
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// FindDayByID mencari satu hari pertunjukan berdasarkan ID-nya. Dipakai sebelum admin menambah kategori, untuk memastikan harinya memang ada.
func (r *Repository) FindDayByID(id int64) (*EventDay, error) {
	var d EventDay
	err := r.db.First(&d, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, err
}

// Publish mengubah status event dari 'draft' menjadi 'published', supaya event muncul di katalog.
func (r *Repository) Publish(id int64) (int64, error) {
	res := r.db.Model(&Event{}).
		Where("id = ? AND status = ?", id, "draft").
		Update("status", "published")
	return res.RowsAffected, res.Error
}

// ListCatalog mengambil semua kartu yang tampil di katalog.
/* Kartu yang tampil hanya yang:
   - event-nya sudah published, dan
   - tanggalnya hari ini atau nanti (yang sudah lewat disembunyikan).
   today berformat "2026-10-10" dan dihitung di service memakai jam WIB. */
func (r *Repository) ListCatalog(today string) ([]EventDay, error) {
	var days []EventDay
	err := r.db.
		// Ambil hanya kolom dari tabel event_days.
		Select("event_days.*").
		// Gabungkan dengan tabel events supaya bisa mengecek status event
		Joins("JOIN events ON events.id = event_days.event_id").
		Where("events.status = ? AND event_days.show_date >= ?", "published", today).
		// Ikut ambil data event
		Preload("Event").
		// Ikut ambil daftar kategori tiap hari, yang termahal di atas.
		Preload("Categories", func(db *gorm.DB) *gorm.DB {
			return db.Order("price DESC")
		}).
		// Urutkan kartu dari tanggal paling dekat.
		Order("event_days.show_date ASC, event_days.id ASC").
		Find(&days).Error
	return days, err
}

// FindCatalogDay mengambil detail satu kartu katalog (satu hari pertunjukan).
func (r *Repository) FindCatalogDay(id int64, today string) (*EventDay, error) {
	var d EventDay
	err := r.db.
		Select("event_days.*").
		Joins("JOIN events ON events.id = event_days.event_id").
		Where("event_days.id = ? AND events.status = ? AND event_days.show_date >= ?", id, "published", today).
		Preload("Event").
		Preload("Categories", func(db *gorm.DB) *gorm.DB {
			return db.Order("price DESC")
		}).
		First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil // tidak ada, masih draft, atau sudah lewat
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// FindEventWithDays mengambil event lengkap dengan semua harinya dan kategori di setiap hari. Dipakai sebelum publish,
// untuk memastikan event sudah siap dijual (punya hari, dan setiap hari punya kategori).
func (r *Repository) FindEventWithDays(id int64) (*Event, error) {
	var e Event
	err := r.db.
		Preload("Days").            // ikut ambil semua hari milik event ini
		Preload("Days.Categories"). // dan kategori di setiap hari itu
		First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}
