package event

import (
	"errors"
	"fmt"
	"strings"
	"time"
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
	Name        string
	Venue       string
	SaleStartAt time.Time
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
	e := &Event{
		Name:              name,
		Venue:             venue,
		Status:            "draft",
		SaleStartsAt:      in.SaleStartAt,
		MaxActiveSessions: 100, // maksimal 100 orang belanja bersamaan (D-07)
		MaxTicketsPerUser: 4,   // maksimal 4 tiket per akun per event (D-09)
		SessionMinutes:    30,  // waktu belanja setelah lolos antrean
		HoldMinutes:       5,   // waktu bayar setelah checkout
	}

	if err := s.repo.CreateEvent(e); err != nil {
		return nil, err
	}

	return e, nil
}
