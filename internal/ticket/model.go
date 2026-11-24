package ticket

import "time"

// Ticket memetakan tabel tickets: satu lembar e-ticket hasil pembayaran lunas.
type Ticket struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	OrderItemID int64     `json:"order_item_id"`
	CategoryID  int64     `json:"category_id"`
	SeatNo      *int      `json:"seat_no"` // nil untuk kategori berdiri (Festival)
	Code        string    `json:"code"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Ticket) TableName() string {
	return "tickets"
}

// TicketView = tampilan e-ticket untuk pembeli di GET /tickets.
type TicketView struct {
	ID           int64     `json:"id"`
	OrderID      int64     `json:"order_id"`
	EventName    string    `json:"event_name"`
	Venue        string    `json:"venue"`
	DayLabel     string    `json:"day_label"`
	ShowDate     time.Time `json:"show_date"`
	CategoryName string    `json:"category_name"`
	SeatNo       *int      `json:"seat_no"`
	Code         string    `json:"code"`
	CreatedAt    time.Time `json:"created_at"`
}
