package event

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Zona waktu Jakarta (UTC+7).
var wib = time.FixedZone("WIB", 7*60*60)

// Daftar error bisnis. Handler membaca error ini untuk memilih status HTTP.
var (
	// ErrValidation = input dari admin salah (jadi 400).
	ErrValidation    = errors.New("Validasi gagal")
	ErrEventNotFound = errors.New("Event tidak ditemukan")
	ErrDayNotFound   = errors.New("Hari tidak ditemukan")

	// Event yang sudah dijual tidak boleh diubah, supaya katalog tidak berubah di tengah war tiket.
	ErrNotDraft  = errors.New("Event sudah dipublished, tidak dapat diubah")
	ErrDuplicate = errors.New("Data dengan nama atau label sudah ada")
)

// Service berisi aturan bisnis modul event. Ia memanggil repository, tapi tidak pernah menulis query sendiri.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// CreateEventInput = data yang dikirim admin saat membuat event.
type CreateEventInput struct {
	Name              string
	Venue             string
	SaleStartAt       time.Time
	MaxActiveSessions *int
	MaxTicketsPerUser *int
	SessionMinutes    *int
}

// CreateEvent membuat event baru dengan status draft (belum tampil di katalog).
func (s *Service) CreateEvent(in CreateEventInput) (*Event, error) {
	name := strings.TrimSpace(in.Name)
	venue := strings.TrimSpace(in.Venue)
	if name == "" || venue == "" {
		// %w "membungkus" ErrValidation. Nanti handler bisa mengecek errors.Is(err, ErrValidation) walau pesannya lebih panjang.
		return nil, fmt.Errorf("%w: Nama dan Venue wajib diisi", ErrValidation)
	}
	if !in.SaleStartAt.After(time.Now()) {
		return nil, fmt.Errorf("%w: Jam penjualan harus di masa depan", ErrValidation)
	}

	// Ambil angka dari admin, atau default
	sessions := valueOr(in.MaxActiveSessions, DefaultMaxActiveSession)
	tickets := valueOr(in.MaxTicketsPerUser, DefaultMaxTicketsPerUser)
	minutes := valueOr(in.SessionMinutes, DefaultSessionMinutes)

	if err := checkRange("max_active_sessions", sessions, minActiveSession, maxActiveSession); err != nil {
		return nil, err
	}
	if err := checkRange("max_tickets_per_user", tickets, minTicketsPerUser, maxTicketsPerUser); err != nil {
		return nil, err
	}
	if err := checkRange("session_minutes", sessions, minSessionMinutes, maxSessionMinutes); err != nil {
		return nil, err
	}

	e := &Event{
		Name:              name,
		Venue:             venue,
		Status:            "draft",
		SaleStartsAt:      in.SaleStartAt,
		MaxActiveSessions: sessions,
		MaxTicketsPerUser: tickets,
		SessionMinutes:    minutes,
		HoldMinutes:       HoldMinutes,
	}
	if err := s.repo.CreateEvent(e); err != nil {
		return nil, err
	}
	return e, nil
}

// AddDay menambah satu hari pertunjukan ke event
func (s *Service) AddDay(eventID int64, label, showDate string) (*EventDay, error) {
	// Event masih ada dan di draft
	if _, err := s.findDraftEvent(eventID); err != nil {
		return nil, err
	}

	label = strings.TrimSpace(label)
	if label == "" {
		return nil, fmt.Errorf("%w: Label hari wajib diisi", ErrValidation)
	}
	// Ubah teks "2026-02-05" jadi tanggal.
	date, err := time.Parse("2006-01-02", showDate)
	if err != nil {
		return nil, fmt.Errorf("%w: Format tanggal harus YYYY-MM-DD", ErrValidation)
	}
	if date.Before(todayDate()) {
		return nil, fmt.Errorf("%w: Tanggal pertunjukkan sudah lewat", ErrValidation)
	}

	d := &EventDay{EventID: eventID, Label: label, ShowDate: date}
	if err := s.repo.CreateDay(d); err != nil {
		// Label kembar di event yang sama ditolak UNIQUE
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrDuplicate
		}
		return nil, err
	}

	return d, nil
}

// CATEGORY
type CreateCategoryInput struct {
	Name       string
	Price      int64
	TotalSeats int
	HasSeats   bool
}

// AddCategory menambah satu jenis tiket ke satu hari pertunjukan.
func (s *Service) AddCategory(dayID int64, in CreateCategoryInput) (*Category, error) {
	// hari ada
	day, err := s.repo.FindDayByID(dayID)
	if err != nil {
		return nil, err
	}
	if day == nil {
		return nil, ErrDayNotFound
	}
	if _, err := s.findDraftEvent(day.EventID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: Nama kategori wajib diisi", ErrValidation)
	}
	if in.Price < 0 {
		return nil, fmt.Errorf("%w: Harga tidak boleh negatif", ErrValidation)
	}
	if in.TotalSeats <= 0 {
		return nil, fmt.Errorf("%w: Jumlah tiket harus lebih dari 0", ErrValidation)
	}

	c := &Category{
		EventDayID: dayID,
		Name:       name,
		Price:      in.Price,
		TotalSeats: in.TotalSeats,
		Available:  in.TotalSeats, // di awal, semua tiket masih tersedia
		NextSeat:   1,             // nomor kursi pertama yang akan dibagikan
		HasSeats:   in.HasSeats,
	}

	if err := s.repo.CreateCategory(c); err != nil {
		// Nama kategori kembar di hari yang sama ditolak UNIQUE
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return c, err
}

// Publish menampilkan event di katalog.
func (s *Service) Publish(eventID int64) error {
	e, err := s.repo.FindEventWithDays(eventID)
	if err != nil {
		return err
	}
	if e == nil {
		return ErrEventNotFound
	}
	if e.Status != "draft" {
		return ErrNotDraft
	}
	if len(e.Days) == 0 {
		return fmt.Errorf("%w: Event belum punya tanggal pertunjukkan", ErrValidation)
	}
	for _, d := range e.Days {
		if len(d.Categories) == 0 {
			return fmt.Errorf("%w: %s belum punya kategori tiket", ErrValidation, d.Label)
		}
	}

	rows, err := s.repo.Publish(eventID)
	if err != nil {
		return err
	}
	// 0 baris berubah = ada request lain yang mem-publish lebih dulu.
	if rows == 0 {
		return ErrNotDraft
	}
	return nil
}

// ListCatalog mengambil semua kartu katalog, lalu menandai mana yang sold out.
func (s *Service) ListCatalog() ([]EventDay, error) {
	days, err := s.repo.ListCatalog(todayDate().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}

	for i := range days {
		markSoldOut(&days[i])
	}
	return days, nil
}

// GetCatalogDay mengambil detail satu kartu katalog.
func (s *Service) GetCatalogDay(dayID int64) (*EventDay, error) {
	d, err := s.repo.FindCatalogDay(dayID, todayDate().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDayNotFound
	}
	markSoldOut(d)
	return d, nil
}

// GetPublishedEvent mengambil event yang sudah diumumkan (published).
func (s *Service) GetPublishedEvent(id int64) (*Event, error) {
	e, err := s.repo.FindEventByID(id)
	if err != nil {
		return nil, err
	}
	if e == nil || e.Status != "published" {
		return nil, ErrEventNotFound
	}

	return e, nil
}

// findDraftEvent mengambil event dan memastikan masih draft.
func (s *Service) findDraftEvent(eventID int64) (*Event, error) {
	e, err := s.repo.FindEventByID(eventID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrEventNotFound
	}
	if e.Status != "draft" {
		return nil, ErrNotDraft
	}
	return e, nil
}

// markSoldOut mengisi label sold out untuk satu hari dan setiap kategorinya.
// Sold out tidak disimpan di database, karena nilainya bisa berubah lagi  saat tiket yang tidak dibayar dikembalikan ke stok.
func markSoldOut(d *EventDay) {
	allSold := len(d.Categories) > 0
	for i := range d.Categories {
		c := &d.Categories[i]
		c.SoldOut = c.Available == 0
		if !c.SoldOut {
			allSold = false
		}
	}
	d.SoldOut = allSold
}

// valueOr mengembalikan isi pointer kalau admin mengisinya, atau nilai default kalau tidak diisi (pointer nil).
func valueOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// checkRange memastikan angka dari admin ada di antara batas bawah dan atas. field dipakai di pesan error, supaya admin tahu angka mana yang salah.
func checkRange(field string, v, min, max int) error {
	if v < min || v > max {
		return fmt.Errorf("%w: %s harus antara %d dan %d", ErrValidation, field, min, max)
	}
	return nil
}

func todayDate() time.Time {
	now := time.Now().In(wib)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
