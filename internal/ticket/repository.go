package ticket

import "gorm.io/gorm"

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// AllocateSeats memesan blok nomor kursi berurutan secara atomik untuk kategori bernomor (mis. VIP 41..44).
// Untuk kategori berdiri (has_seats = false), query tidak menyentuh baris sama sekali dan mengembalikan hasSeats = false.
func (r *Repository) AllocateSeats(tx *gorm.DB, categoryID int64, qty int) (int, bool, error) {
	rows, err := tx.Raw(
		`UPDATE categories
		    SET next_seat = next_seat + ?
		  WHERE id = ? AND has_seats = true
		  RETURNING next_seat - ?`,
		qty, categoryID, qty,
	).Rows()
	if err != nil {
		return 0, false, err
	}
	defer func() {
		_ = rows.Close()
	}()

	if rows.Next() {
		var startSeat int
		if err := rows.Scan(&startSeat); err != nil {
			return 0, false, err
		}
		return startSeat, true, rows.Err()
	}
	return 0, false, rows.Err()
}

// CreateTickets menyimpan seluruh lembar e-ticket milik satu order yang baru lunas.
func (r *Repository) CreateTickets(tx *gorm.DB, tickets []Ticket) error {
	return tx.Create(&tickets).Error
}

// ListByUser mengambil semua e-ticket milik user lengkap dengan info event, hari, dan kategori.
func (r *Repository) ListByUser(userID int64) ([]TicketView, error) {
	var list []TicketView
	err := r.db.Raw(
		`SELECT t.id, o.id AS order_id, e.name AS event_name, e.venue,
		        d.label AS day_label, d.show_date, c.name AS category_name,
		        t.seat_no, t.code, t.created_at
		   FROM tickets t
		   JOIN order_items oi ON oi.id = t.order_item_id
		   JOIN orders o       ON o.id = oi.order_id
		   JOIN categories c   ON c.id = t.category_id
		   JOIN event_days d   ON d.id = o.event_day_id
		   JOIN events e       ON e.id = o.event_id
		  WHERE o.user_id = ?
		  ORDER BY d.show_date ASC, t.id ASC`,
		userID,
	).Scan(&list).Error
	if list == nil {
		list = []TicketView{}
	}
	return list, err
}
