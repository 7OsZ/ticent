package queue

import (
	"context"
	"log/slog"
	"time"
)

// Worker = petugas antrean yang berjalan terus di belakang layar:
// membagikan nomor, menghanguskan yang pergi, dan meloloskan nomor berikutnya.
type Worker struct {
	service  *Service
	interval time.Duration // jarak antar putaran, mis. 2 detik
}

func NewWorker(service *Service, interval time.Duration) *Worker {
	return &Worker{service: service, interval: interval}
}

// Run berjalan terus sampai ctx dibatalkan (mis. server dimatikan).
func (w *Worker) Run(ctx context.Context) {
	slog.Info("worker antrean berjalan", "interval", w.interval.String())

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker antrean berhenti")
			return
		case <-ticker.C:
			w.round()
		}
	}
}

func (w *Worker) round() {
	r, err := w.service.RunRound(time.Now())
	if err != nil {
		slog.Error("worker antrean error", "err", err)
	}
	if r.Numbered > 0 || r.Expired > 0 || r.Admitted > 0 {
		slog.Info("putaran antrean", "numbered", r.Numbered, "expired", r.Expired, "admitted", r.Admitted)
	}
}
