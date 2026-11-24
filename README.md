# ticent — War Ticket API

Backend API berbahasa **Go** yang menyimulasikan penjualan tiket event berkuota terbatas pada jam *war ticket*: antrean ruang tunggu (*waiting room*) dengan pengacakan adil saat jam *war*, pembatasan sesi belanja aktif, penahanan stok (*hold*) 30 menit, pembayaran **Midtrans Sandbox**, dan penerbitan *e-ticket*.

> **Catatan ruang lingkup:** Proyek ini adalah **simulasi** masalah konkurensi dan korektnes data untuk portfolio backend Go — **bukan klon tiket.com** atau sistem produksi skala ratusan ribu pengguna.

---

## Fitur & Desain Kunci

- **0 Oversell pada Satu Transaksi PostgreSQL:** Pengurangan stok (`UPDATE categories SET available = available - ? WHERE id = ? AND available >= ?`) dan kuota akun (`INSERT ... ON CONFLICT DO UPDATE ... WHERE used + EXCLUDED.used <= ?`) dijalankan secara atomik dalam satu transaksi database, diperkuat `CHECK (available BETWEEN 0 AND total_seats)`, `CHECK (used >= 0)`, dan *partial unique index* sebagai jaring pengaman kedua.
- **Bebas Deadlock (Urutan Kunci Global):** Seluruh transaksi (`Checkout`, sweeper hold hangus, webhook pembayaran) selalu mengunci tabel dengan urutan tetap: `orders` → `queue_entries` → `user_event_quota` → `categories` (diurutkan berdasarkan `category_id` naik). Baris `events` hanya dikunci oleh worker antrean.
- **Waiting Room Adil & Hemat Write:** Semua pengguna yang masuk sebelum jam *war* menerima nomor acak pada putaran pertama setelah `sale_starts_at`; pendaftar berikutnya masuk secara FIFO. Posisi antrean dihitung lewat pointer `position - admitted_up_to` tanpa meng-*update* ribuan baris, dan disiarkan *real-time* lewat **Server-Sent Events (SSE)** (dengan *throttling* tulis `last_seen_at` tiap 20 detik).
- **Idempotensi Penuh:** Satu order `pending` per `(user_id, event_id)` dijaga *partial unique index*, pembuatan token Snap menyimpan ulang token yang sama bila dipanggil ulang, dan webhook Midtrans diverifikasi `SHA-512` (`crypto/subtle.ConstantTimeCompare`) serta aman dikirim berulang.
- **Ramah Pengembangan Lokal Tanpa Tunnel:** Selain endpoint webhook penuh, tersedia `POST /orders/:id/sync` untuk menarik status langsung dari Midtrans Core Status API dan memprosesnya lewat fungsi yang persis sama dengan webhook.
- **Observability & Portabilitas:** Log JSON terstruktur (`log/slog`) dengan pelacakan `X-Request-ID`, endpoint `/healthz` yang mengecek koneksi database, *graceful shutdown* yang menunggu worker selesai, serta migrasi SQL yang disematkan ke dalam binary lewat `go:embed`.

---

## Arsitektur

Monolit *layered* per fitur tanpa microservice, Redis, maupun message broker — seluruh jaminan anti-oversell bertumpu pada transaksi PostgreSQL.

```text
cmd/api/main.go                 # Wiring manual, HTTP server, worker goroutines, graceful shutdown
migrations/                     # File migrasi SQL (000001..000005) + go:embed
api/openapi.yaml                # Kontrak OpenAPI 3.0 lengkap (21 endpoint)
loadtest/war.js                 # Skrip load test k6 skenario war checkout
internal/
  platform/{config,database,httpx,testutil}
  auth/                         # Register, login (bcrypt + JWT HS256), role user/admin
  event/                        # Admin draft -> hari -> kategori -> publish, katalog publik
  queue/                        # Waiting room, worker admitter, heartbeat, SSE stream
  order/                        # Checkout hold 30 menit, sweeper otomatis, pelepasan stok/kuota
  payment/                      # Midtrans Snap, verifikasi webhook SHA-512, sync status lokal
  ticket/                       # Alokasi nomor kursi atomik (RETURNING) & daftar e-ticket
```

Arah dependensi satu arah (tanpa *cyclic import*):
`platform` ← `auth`, `event`, `queue`, `ticket` ← `order` ← `payment`.

---

## Cara Menjalankan (Windows PowerShell)

### Prasyarat
- **Go 1.26+**
- **PostgreSQL 18** (lokal atau Neon koneksi **direct**, bukan host `-pooler`)
- Akun **Midtrans Sandbox** (untuk mencoba alur pembayaran Snap secara manual)

### 1. Siapkan Environment
```powershell
Copy-Item .env.example .env
```
Isi `.env`:
```env
HTTP_PORT=8080
DATABASE_URL=postgres://postgres:<password>@localhost:5432/ticent?sslmode=disable
JWT_SECRET=ganti-dengan-kunci-acak-minimal-32-karakter
TEST_DATABASE_URL=postgres://postgres:<password>@localhost:5432/ticent_test?sslmode=disable
MIDTRANS_SERVER_KEY=SB-Mid-server-xxxx
MIDTRANS_CLIENT_KEY=SB-Mid-client-xxxx
MIDTRANS_IS_PRODUCTION=false
```
> **Penting:** `TEST_DATABASE_URL` **wajib** memakai database terpisah dari `DATABASE_URL` karena seluruh tabel di `TEST_DATABASE_URL` dikosongkan (`TRUNCATE`) setiap kali test berjalan.

### 2. Jalankan Server
Migrasi SQL dijalankan otomatis saat aplikasi menyala:
```powershell
go run ./cmd/api
```

Cek kesehatan server (pakai `curl.exe` di PowerShell, bukan alias `curl`):
```powershell
curl.exe http://localhost:8080/healthz
```

### 3. Buat Akun Admin Pertama
Daftar akun biasa lewat `POST /auth/register`, lalu ubah role-nya di PostgreSQL:
```sql
UPDATE users SET role = 'admin' WHERE email = 'admin@contoh.com';
```
Login ulang lewat `POST /auth/login` untuk memperoleh JWT ber-role `admin`.

---

## Kontrak Endpoint

Spesifikasi lengkap skema request/response tersedia di [`api/openapi.yaml`](api/openapi.yaml).

| Grup | Method & Path | Auth | Keterangan |
|---|---|---|---|
| Health | `GET /healthz` | – | Ping PostgreSQL (`200` / `503`) |
| Auth | `POST /auth/register` | – | Daftar pembeli baru (email dinormalisasi lowercase) |
| Auth | `POST /auth/login` | – | Login → JWT HS256 (berlaku 24 jam) |
| Auth | `GET /auth/me` | User | Baca `user_id` & `role` dari token |
| Admin | `POST /admin/events` | Admin | Buat event baru (`draft`) |
| Admin | `POST /admin/events/:id/days` | Admin | Tambah hari pertunjukan (`YYYY-MM-DD`) |
| Admin | `POST /admin/days/:id/categories` | Admin | Tambah kategori tiket (`has_seats` bernomor/berdiri) |
| Admin | `POST /admin/events/:id/publish` | Admin | Tayangkan event ke katalog |
| Katalog | `GET /catalog` | – | Kartu hari pertunjukan aktif (tanggal WIB belum lewat) |
| Katalog | `GET /catalog/:id` | – | Detail hari pertunjukan & sisa stok tiap kategori |
| Queue | `POST /events/:id/queue` | User | Masuk waiting room event |
| Queue | `GET /events/:id/queue/me` | User | Cek posisi antrean (`position_in_line` dari `total_in_line`) |
| Queue | `POST /events/:id/queue/heartbeat` | User | Kirim sinyal hadir + terima posisi terbaru |
| Queue | `GET /events/:id/queue/stream` | User | Aliran posisi antrean *real-time* (SSE) |
| Queue | `DELETE /events/:id/queue` | User | Keluar dari antrean |
| Order | `POST /orders` | User | Checkout & tahan tiket 30 menit (wajib `active` di antrean) |
| Order | `GET /orders/:id` | User | Detail order milik sendiri |
| Payment | `POST /orders/:id/pay` | User | Buat/ambil token Midtrans Snap (idempoten, expiry 28 menit) |
| Payment | `POST /orders/:id/sync` | User | Tarik status pembayaran dari Midtrans Status API |
| Payment | `POST /payments/midtrans/notification` | Signature | Webhook Midtrans (verifikasi `SHA-512`, idempoten) |
| Ticket | `GET /tickets` | User | Daftar *e-ticket* milik user |

---

## Bukti Korektnes (Terverifikasi di PostgreSQL 18)

Seluruh skenario konkurensi diuji terhadap **PostgreSQL 18 sungguhan** (bukan mock):

```powershell
go test ./... -v -count=1
```

Hasil eksekusi nyata pada lingkungan pengembangan lokal (**Windows, Intel64 8 logical CPU, Go 1.26.3, PostgreSQL 18 lokal**):

| Metrik | Test Go | Skenario | Hasil Terukur |
|---|---|---|---|
| **0 Oversell** | `TestCheckoutNoOversell` | 200 goroutine paralel menekan checkout di detik yang sama untuk 50 tiket | **50 sukses**, **150 `ErrSoldOut`**, **0 error lain**, `available = 0`, `SUM(qty) = 50` (`0.83s`) |
| **Kuota & Anti-Order Ganda** | `TestCheckoutQuotaAndRetryNeverExceedsLimit` | Akun dengan 3 tiket terpakai (batas 4) mengirim 10 request paralel × 2 tiket lalu 10 retry paralel × 1 tiket | Semua request 2 tiket ditolak; retry 1 tiket menghasilkan **tepat 1 order pending**, `used = 4` (`0.56s`) |
| **Sweeper Hold Hangus** | `TestExpireOverdueReleasesStockQuotaAndQueue` | Order belum dibayar melewati 30 menit lalu disapu worker | Order → `expired`, stok kembali utuh, `used = 0`, user bisa antre ulang (`0.39s`) |
| **Batas Sesi Aktif Antrean** | `TestQueueSimulation` | 1.000 user antre dengan batas `max_active_sessions = 50` selama 30 putaran worker paralel dengan keluar/checkout | `COUNT(active) <= 50` di **setiap** putaran, nomor `1..1000` unik, 0 entri hidup ganda (`1.66s`) |
| **Idempotensi Webhook** | `TestWebhookSettlementIdempotentSeatedAndStanding` | Notifikasi `settlement` valid dikirim dua kali untuk 2 VIP + 1 Festival | Tepat **3 tiket** terbit (VIP kursi `1, 2`, Festival `seat_no = null`), tidak pernah ganda (`0.54s`) |
| **Penomoran Kursi Paralel** | `TestConcurrentSettlementSeatAllocationNoDuplicates` | 20 order (masing-masing 2 tiket VIP) lunas bersamaan di detik yang sama | Terbit **40 tiket** dengan `seat_no` tepat **`1..40`** berurutan tanpa bolong/kembar (`0.63s`) |
| **Bayar Setelah Hangus** | `TestWebhookSettlementAfterExpiredMarksFailedWithoutTickets` | Webhook `settlement` terlambat masuk setelah order dihanguskan sweeper | Status order menjadi `failed`, **0 tiket** diterbitkan, stok tetap utuh (`0.27s`) |
| **Registrasi Paralel** | `TestRegisterConcurrentDuplicateAndCaseInsensitive` | 10 goroutine paralel mendaftar email sama dengan variasi spasi & huruf besar | Tepat **1 akun** dibuat (`fan@bmth.com`), **9 `409 Conflict`**, **0 error `500`** (`0.55s`) |

---

## Load Test (k6)

Skrip [`loadtest/war.js`](loadtest/war.js) menjalankan skenario HTTP *end-to-end* terhadap PostgreSQL 18 lokal: membuat event *war*, mendaftarkan `BUYERS` akun (default `200`), memasukkan semuanya ke *waiting room*, menunggu jam *war* dibuka, lalu menembakkan `POST /orders` serentak terhadap `STOCK` tiket (default `50`) dengan *threshold* `orders_created_201 == STOCK` dan `unexpected_responses == 0`.

Cara menjalankan di PowerShell (pastikan [k6](https://grafana.com/docs/k6/latest/set-up/install-k6/) terpasang dan server `go run ./cmd/api` menunjuk ke PostgreSQL 18 lokal):

```powershell
k6 run -e BASE_URL=http://localhost:8080 -e ADMIN_EMAIL=admin@contoh.com -e ADMIN_PASSWORD=password123 -e BUYERS=200 -e STOCK=50 loadtest/war.js
```

> *Catatan:* Binary `k6` belum terpasang di mesin saat dokumentasi ini ditulis sehingga angka throughput HTTP `k6` belum dicantumkan; jalankan perintah di atas pada PostgreSQL 18 lokal untuk merekam metrik `http_req_duration` (p95/p99) dan RPS sesuai lingkungan mesin.

---

## Batasan yang Disengaja

1. **Simulasi Satu Instance:** Worker antrean dan sweeper hold berjalan di proses yang sama dengan HTTP server; tidak dirancang untuk *multi-instance* tanpa koordinasi tambahan.
2. **Semua Event Wajib Antre:** Tidak ada jalur pembelian langsung tanpa melewati *waiting room*, karena fokus proyek ini adalah pembuktian mekanisme *war ticket*.
3. **Nomor Kursi Berurutan per Kategori:** Kategori bernomor (`has_seats = true`) mengalokasikan nomor berurutan (`next_seat`) dalam satu kategori saat lunas — bukan pemilihan kursi pada denah baris fisik.
4. **Pembayaran Sandbox:** Integrasi Midtrans hanya menargetkan lingkungan Sandbox; pengembalian dana (*refund*) otomatis untuk pembayaran yang masuk setelah hold hangus berada di luar *scope* (order ditandai `failed` dan dicatat pada log peringatan).
