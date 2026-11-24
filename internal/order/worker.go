package order

import (
	"context"
	"log/slog"
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
	slog.Info("sweeper berjalan", "interval", w.interval.String())

	// Langsung sapu sekali saat start.
	w.sweep()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("sweeper berhenti")
			return
		case <-ticker.C:
			w.sweep()
		}
	}
}

func (w *Worker) sweep() {
	n, err := w.service.ExpireOverdue(time.Now())
	if err != nil {
		slog.Error("sweeper error", "err", err)
	}
	if n > 0 {
		slog.Info("sweeper menghanguskan order", "expired_orders", n)
	}
}
