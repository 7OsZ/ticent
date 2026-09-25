package event

import "time"

type Event struct {
	ID                int64     `gorm:"primaryKey" json:"id"` // BIGSERIAL -> int64
	Name              string    `json:"name"`                 // nama event
	Venue             string    `json:"venue"`                // lokasi
	Status            string    `json:"status"`               // 'draft' | 'published'
	SaleStartsAt      time.Time `json:"sale_starts_at"`       // jam war dibuka
	MaxActiveSessions int       `json:"max_active_sessions"`  // batas sesi aktif
	MaxTicketsPerUser int       `json:"max_tickets_per_user"` // kuota per akun
	SessionMinutes    int       `json:"session_minutes"`      // lama sesi antrean
	HoldMinutes       int       `json:"hold_minutes"`         // lama hold checkout
	QueueTail         int       `json:"-"`                    // internal antrean, tidak dikirim
	AdmittedUpTo      int       `json:"-"`                    // internal antrean, tidak dikirim
	CreatedAt         time.Time `json:"created_at"`           // diisi DEFAULT now()

	// Has-many: satu event punya banyak hari. foreignKey:EventID = kolom event_id di event_days.
	// omitempty: field tidak muncul di JSON kalau tidak di-Preload.
	Days []EventDay `gorm:"foreignKey:EventID" json:"days,omitempty"`
}

type EventDay struct {
	ID       int64     `gorm:"primaryKey" json:"id"`
	EventID  int64     `json:"event_id"`  // FK ke events.id
	Label    string    `json:"label"`     // 'DAY-1'
	ShowDate time.Time `json:"show_date"` // DATE -> time.Time (jam 00:00)

	// Belongs-to: hari ini milik satu event. Dipakai katalog untuk menampilkan nama event.
	// Pointer karena Event juga berisi []EventDay, dan pointer boleh nil saat tidak di-Preload.
	Event *Event `gorm:"foreignKey:EventID" json:"event,omitempty"`

	// Has-many: satu hari punya banyak kategori.
	Categories []Category `gorm:"foreignKey:EventDayID" json:"categories,omitempty"`

	// Dihitung di service, bukan kolom DB. gorm:"-" = GORM abaikan field ini.
	SoldOut bool `gorm:"-" json:"sold_out"` // true kalau SEMUA kategori hari ini habis
}

type Category struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	EventDayID int64  `json:"event_day_id"` // FK ke event_days.id
	Name       string `json:"name"`         // 'VIP'
	Price      int64  `json:"price"`        // rupiah, integer (bukan float)
	TotalSeats int    `json:"total_seats"`  // kapasitas kategori
	Available  int    `json:"available"`    // sisa stok, dikurangi saat hold (Modul 3)
	NextSeat   int    `json:"-"`            // internal penomoran seat (Modul 5)
	HasSeats   bool   `json:"has_seats"`    // false = berdiri/Festival, tanpa seat_no

	SoldOut bool `gorm:"-" json:"sold_out"` // dihitung di service: available == 0
}
