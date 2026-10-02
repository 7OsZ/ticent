package order

import (
	"context" // sinyal "berhenti" dari main.go
	"log"
	"time"
)

type Worker struct {
	service  *Service
	interval time.Duration // jarak antar putaran, mis. 30 detik
}

func NewWorker(service *Service, interval time.Duration) *Worker {
	return &Worker{service: service, interval: interval}
}

// Run berjalan terus sampai ctx dibatalkan (mis. server dimatikan).
func (w *Worker) Run(ctx context.Context) {
	log.Printf("sweeper berjalan, setiap %s", w.interval)

	// Langsung sapu sekali saat start.
	w.sweep()

	ticker := time.NewTicker(w.interval) // "alarm" yang berbunyi setiap interval
	defer ticker.Stop()                  // matikan alarm saat Run selesai

	for {
		// select menunggu salah satu dari dua hal terjadi lebih dulu.
		select {
		case <-ctx.Done(): // ada perintah berhenti
			log.Println("sweeper berhenti")
			return
		case <-ticker.C: // alarm berbunyi
			w.sweep()
		}
	}
}

// sweep menjalankan satu putaran dan mencatat hasilnya di terminal.
func (w *Worker) sweep() {
	n, err := w.service.ExpireOverdue(time.Now())
	if err != nil {
		log.Println("sweeper error:", err)
	}
	if n > 0 {
		log.Printf("sweeper: %d order hangus, tiket dan jatah beli dikembalikan", n)
	}
}
