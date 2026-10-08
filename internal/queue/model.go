package queue

import "time"

const (
	StatusWaiting  = "waiting"  // sedang antre
	StatusActive   = "active"   // lolos, sedang belanja (memakai slot)
	StatusCheckout = "checkout" // sudah checkout, slot sudah dilepas
	StatusDone     = "done"     // sudah bayar
	StatusExpired  = "expired"  // hangus, harus antre ulang
	StatusLeft     = "left"     // keluar sendiri
)

type Entry struct {
	ID      int64 `gorm:"primaryKey" json:"id"`
	EventID int64 `json:"event_id"`
	UserID  int64 `json:"user_id"`

	Position *int   `json:"position"` // NULL sampai worker membagikan nomor (D-25)
	Status   string `json:"status"`

	JoinedAt   time.Time `json:"joined_at"`
	LastSeenAt time.Time `json:"-"` // urusan internal heartbeat, tidak dikirim ke klien

	AdmittedAt       *time.Time `json:"admitted_at"`
	SessionExpiresAt *time.Time `json:"session_expires_at"`
}

func (Entry) TableName() string {
	return "queue_entries"
}

// StatusView = jawaban "posisi saya di antrean". Dipakai GET /me, heartbeat, dan SSE.
type StatusView struct {
	EntryID int64  `json:"entry_id"`
	Status  string `json:"status"`

	// Berapa orang lagi di depan saya (termasuk saya). Contoh: 332.
	// null = belum jam war, nomor belum dibagikan. 0 = sudah lolos.
	PositionInLine *int `json:"position_in_line"`

	// Panjang antrean yang belum lolos. Perkiraan: ikut menghitung
	// nomor milik orang yang sudah keluar atau hangus.
	TotalInLine int `json:"total_in_line"`

	SaleStartsAt     time.Time  `json:"sale_starts_at"`
	SessionExpiresAt *time.Time `json:"session_expires_at"` // batas belanja, terisi setelah lolos
}

// Live = entri masih perlu dipantau (masih antre atau sedang belanja).
func (v *StatusView) Live() bool {
	return v.Status == StatusWaiting || v.Status == StatusActive
}

// RoundResult = ringkasan satu putaran worker, untuk log.
type RoundResult struct {
	Numbered int // entri yang baru mendapat nomor
	Expired  int // entri yang dihanguskan (tanpa heartbeat / sesi habis)
	Admitted int // entri yang lolos dan mulai belanja
}
