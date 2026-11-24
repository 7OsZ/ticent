package queue

import "time"

const (
	// Jarak antar putaran worker antrean. Orang yang join setelah jam war
	// menunggu paling lama satu putaran sebelum mendapat nomor.
	WorkerInterval = 2 * time.Second

	// Tanpa heartbeat selama ini = dianggap sudah menutup halaman.
	// Klien disarankan mengirim heartbeat setiap 20 detik, jadi 3 kali gagal baru hangus.
	HeartbeatTimeout = 60 * time.Second

	// Jarak kiriman "posisi X dari Y" lewat SSE.
	SSEInterval = 2 * time.Second

	// Jarak minimal penulisan last_seen_at dari aliran SSE supaya DB tidak dihujani write tiap 2 detik.
	TouchInterval = 20 * time.Second
)
