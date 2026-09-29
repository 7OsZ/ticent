package order

import "time"

const (
	StatusPending = "pending"
	StatusPaid    = "paid"
	StatusExpired = "expired"
	StatusFailed  = "failed"
)

// Order memetakan tabel orders: satu kali checkout.
type Order struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	UserID          int64     `json:"user_id"`
	EventID         int64     `json:"event_id"`
	EventDayID      int64     `json:"event_day_id"`
	Status          string    `json:"status"`
	TotalAmount     int64     `json:"total_amount"`      // total rupiah
	MidtransOrderID string    `json:"midtrans_order_id"` // kode unik untuk Midtrans
	HoldExpiresAt   time.Time `json:"hold_expires_at"`   // frontend memakai ini untuk hitung mundur
	// Pointer karena boleh kosong: order yang belum dibayar tidak punya waktu bayar.
	PaidAt    *time.Time `json:"paid_at"`
	CreatedAt time.Time  `json:"created_at"`

	// Daftar isi order (kategori dan jumlah tiketnya).
	Items []OrderItem `gorm:"foreignKey:OrderID" json:"items,omitempty"`
}

type OrderItem struct {
	ID         int64 `gorm:"primaryKey" json:"id"`
	OrderID    int64 `json:"order_id"`    // order pemilik baris ini
	CategoryID int64 `json:"category_id"` // kategori yang dibeli, mis. VIP
	Qty        int   `json:"qty"`         // jumlah tiket kategori ini
	UnitPrice  int64 `json:"unit_price"`  // harga per tiket, disalin saat checkout
}

type UserEventQuota struct {
	UserID  int64 `gorm:"primaryKey"`
	EventID int64 `gorm:"primaryKey"`
	Used    int   // tiket yang sedang ditahan + yang sudah lunas
}

// TableName diperlukan karena GORM akan menebak "user_event_quota" menjadi  "user_event_quota" + s = "user_event_quotas", yang tidak ada di database.
func (UserEventQuota) TableName() string {
	return "user_event_quota"
}
