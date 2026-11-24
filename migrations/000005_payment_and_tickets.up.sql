-- Selaraskan default hold_minutes ke keputusan D-08 (30 menit).
ALTER TABLE events ALTER COLUMN hold_minutes SET DEFAULT 30;

-- Token dan URL pembayaran Midtrans Snap disimpan di order supaya retry POST /orders/:id/pay idempoten.
ALTER TABLE orders
    ADD COLUMN snap_token TEXT,
    ADD COLUMN snap_redirect_url TEXT;

-- Satu baris = satu e-ticket fisik/digital (mis. 2 VIP menghasilkan 2 baris tiket).
CREATE TABLE tickets (
    id            BIGSERIAL PRIMARY KEY,
    order_item_id BIGINT NOT NULL REFERENCES order_items(id) ON DELETE CASCADE,
    category_id   BIGINT NOT NULL REFERENCES categories(id),
    seat_no       INT,                   -- NULL untuk kategori berdiri seperti Festival (D-10)
    code          TEXT NOT NULL UNIQUE,  -- kode e-ticket unik, mis. TKT-...
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Di PostgreSQL, NULL dianggap berbeda, jadi tiket berdiri (seat_no NULL) tidak saling bentrok,
    -- sementara kursi bernomor (VIP kursi 41) dijamin tidak pernah ganda.
    UNIQUE (category_id, seat_no)
);

CREATE INDEX tickets_order_item_idx ON tickets (order_item_id);
