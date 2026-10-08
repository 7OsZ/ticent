package queue

import (
	"errors"
	"fmt"
	"slices"
	"ticent/internal/event"
	"time"

	"gorm.io/gorm"
)

// Daftar error bisnis antrean. Handler memakai ini untuk memilih status HTTP.
var (
	ErrEventNotFound  = errors.New("Event tidak ditemukan")
	ErrAlreadyInQueue = errors.New("Kamu sudah ada di antrean ini")
	ErrNotInQueue     = errors.New("Kamu tidak sedang antre di event ini")
)

type Service struct {
	repo   *Repository
	events *event.Service
}

func NewService(repo *Repository, events *event.Service) *Service {
	return &Service{repo: repo, events: events}
}

// Join memasukkan user ke antrean event. Nomor antrean BELUM diberikan di sini.
func (s *Service) Join(userID, eventID int64) (*Entry, error) {
	// Pastikan event ada dan sudah published.
	if _, err := s.publishedEvent(eventID); err != nil {
		return nil, err
	}

	now := time.Now()
	e := &Entry{
		EventID:    eventID,
		UserID:     userID,
		Status:     StatusWaiting,
		JoinedAt:   now,
		LastSeenAt: now,
	}

	if err := s.repo.CreateEntry(e); err != nil {
		// Masih punya antrean hidup di event ini (waiting/active/checkout).
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrAlreadyInQueue
		}
		return nil, err
	}
	return e, nil
}

// Status menjawab "posisi saya di antrean". Hanya membaca, tidak dihitung sebagai heartbeat.
func (s *Service) Status(userID, eventID int64) (*StatusView, error) {
	e, err := s.repo.FindLiveEntry(eventID, userID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotInQueue
	}
	return s.view(e)
}

// Heartbeat mencatat bahwa user masih membuka halaman antrean, lalu mengembalikan posisinya.
// Klien cukup memanggil ini setiap ±20 detik sebagai pengganti polling GET /me.
func (s *Service) Heartbeat(userID, eventID int64) (*StatusView, error) {
	ok, err := s.repo.Touch(eventID, userID, time.Now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotInQueue
	}
	return s.Status(userID, eventID)
}

// Presence dipakai SSE: sama seperti Heartbeat, tapi kalau entri sudah tidak
// antre/belanja (mis. baru saja hangus atau checkout), tetap mengembalikan status
// terakhirnya supaya klien tahu kenapa aliran berhenti.
func (s *Service) Presence(userID, eventID int64) (*StatusView, error) {
	if _, err := s.repo.Touch(eventID, userID, time.Now()); err != nil {
		return nil, err
	}
	e, err := s.repo.FindLatestEntry(eventID, userID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotInQueue
	}
	return s.view(e)
}

// Leave mengeluarkan user dari antrean (tombol "keluar dari antrean").
func (s *Service) Leave(userID, eventID int64) error {
	ok, err := s.repo.Leave(eventID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotInQueue
	}
	return nil
}

// view menyusun jawaban "posisi X dari Y" (D-26).
// Contoh BMTH: admitted_up_to = 600, queue_tail = 1500, nomor saya 932
// -> posisi 332 dari 900. Antrean maju tanpa perlu meng-update ribuan baris.
func (s *Service) view(e *Entry) (*StatusView, error) {
	ev, err := s.publishedEvent(e.EventID)
	if err != nil {
		return nil, err
	}

	v := &StatusView{
		EntryID:          e.ID,
		Status:           e.Status,
		TotalInLine:      max(ev.QueueTail-ev.AdmittedUpTo, 0),
		SaleStartsAt:     ev.SaleStartsAt,
		SessionExpiresAt: e.SessionExpiresAt,
	}

	switch e.Status {
	case StatusWaiting:
		// Sebelum jam war nomor belum dibagikan: posisi null.
		if e.Position != nil {
			p := *e.Position - ev.AdmittedUpTo
			v.PositionInLine = &p
		}
	case StatusActive, StatusCheckout:
		zero := 0 // sudah lolos, tidak ada lagi orang di depan
		v.PositionInLine = &zero
	}
	// done/expired/left: posisi null, antrean sudah tidak berlaku.
	return v, nil
}

// publishedEvent memastikan event ada dan sudah published.
func (s *Service) publishedEvent(eventID int64) (*event.Event, error) {
	ev, err := s.events.GetPublishedEvent(eventID)
	if errors.Is(err, event.ErrEventNotFound) {
		return nil, ErrEventNotFound
	}
	if err != nil {
		return nil, err
	}
	return ev, nil
}

// RunRound menjalankan satu putaran worker antrean untuk semua event yang perlu diurus.
// Satu event gagal tidak menghentikan event lain.
func (s *Service) RunRound(now time.Time) (RoundResult, error) {
	var total RoundResult

	ids, err := s.repo.ListEventsToProcess(now)
	if err != nil {
		return total, err
	}

	var errs []error
	for _, id := range ids {
		r, err := s.processEvent(id, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("event %d: %w", id, err))
			continue
		}
		total.Numbered += r.Numbered
		total.Expired += r.Expired
		total.Admitted += r.Admitted
	}
	return total, errors.Join(errs...)
}

// processEvent mengurus antrean SATU event dalam satu transaksi:
//  1. kunci baris event (hanya worker yang mengunci events, D-24);
//  2. hanguskan yang tidak hadir lagi;
//  3. bagikan nomor ke yang belum punya;
//  4. isi slot kosong dengan nomor terkecil.
//
// Menghanguskan dilakukan SEBELUM membagi nomor, supaya tab yang sudah ditutup
// sebelum jam war tidak ikut mendapat nomor (mengurangi lubang di antrean).
func (s *Service) processEvent(eventID int64, now time.Time) (RoundResult, error) {
	var r RoundResult
	err := s.repo.InTx(func(tx *gorm.DB) error {
		st, err := s.repo.LockEvent(tx, eventID)
		if err != nil {
			return err
		}

		// 2) Hanguskan sesi habis / tanpa heartbeat.
		expired, err := s.repo.ExpireStale(tx, eventID, now, now.Add(-HeartbeatTimeout))
		if err != nil {
			return err
		}
		r.Expired = int(expired)

		// 3) Bagikan nomor. Putaran pertama setelah jam war (tail masih 0) diacak:
		// yang datang jam 08:00 dan 13:30 punya peluang sama. Setelah itu urut waktu datang.
		numbered, err := s.repo.AssignPositions(tx, eventID, st.QueueTail, st.QueueTail == 0)
		if err != nil {
			return err
		}
		if numbered > 0 {
			if err := s.repo.AdvanceTail(tx, eventID, numbered); err != nil {
				return err
			}
		}
		r.Numbered = int(numbered)

		// 4) Isi slot kosong. Contoh: batas 100, sedang belanja 97 -> 3 orang masuk.
		active, err := s.repo.CountActive(tx, eventID)
		if err != nil {
			return err
		}
		free := st.MaxActiveSessions - int(active)
		if free <= 0 {
			return nil
		}
		sessionEnd := now.Add(time.Duration(st.SessionMinutes) * time.Minute)
		positions, err := s.repo.Admit(tx, eventID, free, now, sessionEnd)
		if err != nil {
			return err
		}
		if len(positions) == 0 {
			return nil
		}
		if err := s.repo.AdvanceAdmitted(tx, eventID, slices.Max(positions)); err != nil {
			return err
		}
		r.Admitted = len(positions)
		return nil
	})
	return r, err
}
