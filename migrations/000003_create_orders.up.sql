-- Kuota tiket per akun per event. Satu baris = "user X sudah memakai N tiket di event Y".
-- Yang dihitung: tiket yang sedang ditahan (belum bayar) + tiket yang sudah lunas.
CREATE TABLE user_event_quota (
    user_id  BIGINT NOT NULL REFERENCES users(id),
    event_id BIGINT NOT NULL REFERENCES events(id),
    used     INT NOT NULL DEFAULT 0 CHECK (used >= 0), -- tidak boleh minus saat kuota dikembalikan
    -- Satu user hanya punya satu baris kuota per event.
    PRIMARY KEY (user_id, event_id)
);

-- Order = satu kali checkout. Isinya boleh beberapa kategori di SATU hari yang sama.
CREATE TABLE orders (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT NOT NULL REFERENCES users(id),
    event_id          BIGINT NOT NULL REFERENCES events(id),     -- untuk hitung kuota per event
    event_day_id      BIGINT NOT NULL REFERENCES event_days(id), -- hari yang dibeli, mis. BMTH Day-1
    -- pending = tiket ditahan, menunggu bayar
    -- paid    = sudah dibayar
    -- expired = 30 menit lewat tanpa bayar, tiket dikembalikan ke stok
    -- failed  = pembayaran gagal
    status            TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'paid', 'expired', 'failed')),
    total_amount      BIGINT NOT NULL CHECK (total_amount >= 0), -- total rupiah
    midtrans_order_id TEXT NOT NULL UNIQUE,  -- kode order yang dikirim ke Midtrans (Modul 5), harus unik
    hold_expires_at   TIMESTAMPTZ NOT NULL,  -- batas waktu bayar (dibuat + 30 menit)
    paid_at           TIMESTAMPTZ,           -- kosong sampai dibayar
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Satu user hanya boleh punya SATU order yang belum dibayar per event.
-- Index ini hanya berlaku untuk baris berstatus pending. Setelah order dibayar atau hangus, user boleh checkout lagi. Efeknya: kalau user menekan tombol
-- checkout dua kali (atau request terkirim ulang), order kedua ditolak database.
CREATE UNIQUE INDEX orders_one_pending_per_user_event
    ON orders (user_id, event_id)
    WHERE status = 'pending';

-- Index untuk sweeper: cepat menemukan order pending yang waktu bayarnya sudah lewat.
CREATE INDEX orders_sweep_idx ON orders (hold_expires_at) WHERE status = 'pending';

-- Isi order: berapa tiket dari kategori apa. Mis. 2 tiket VIP + 1 tiket Festival.
CREATE TABLE order_items (
    id          BIGSERIAL PRIMARY KEY,
    order_id    BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE, -- item ikut terhapus kalau order dihapus
    category_id BIGINT NOT NULL REFERENCES categories(id),
    qty         INT NOT NULL CHECK (qty > 0),             -- minimal 1 tiket
    unit_price  BIGINT NOT NULL CHECK (unit_price >= 0),  -- harga per tiket SAAT dibeli
    -- Satu kategori hanya muncul sekali di satu order (2 VIP ditulis qty = 2, bukan dua baris).
    UNIQUE (order_id, category_id)
);