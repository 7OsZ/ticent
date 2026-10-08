package queue

import (
	"context" // sinyal "berhenti" dari main.go
	"log"
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
	log.Printf("worker antrean berjalan, setiap %s", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("worker antrean berhenti")
			return
		case <-ticker.C:
			w.round()
		}
	}
}

// round menjalankan satu putaran dan mencatat hasilnya kalau ada perubahan.
func (w *Worker) round() {
	r, err := w.service.RunRound(time.Now())
	if err != nil {
		log.Println("worker antrean error:", err)
	}
	if r.Numbered > 0 || r.Expired > 0 || r.Admitted > 0 {
		log.Printf("antrean: %d dapat nomor, %d hangus, %d lolos", r.Numbered, r.Expired, r.Admitted)
	}
}
