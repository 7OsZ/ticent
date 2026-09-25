-- Event: satu konser/acara. Kolom config antrean ikut di sini (dipakai Modul 3-4).
CREATE TABLE events (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT NOT NULL,
    venue                TEXT NOT NULL,
    -- draft = belum tampil di katalog, published = tampil. CHECK tolak nilai lain.
    status               TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    sale_starts_at       TIMESTAMPTZ NOT NULL,                 -- jam war dibuka (mis. 14:00)
    max_active_sessions  INT NOT NULL DEFAULT 100 CHECK (max_active_sessions > 0),  -- D-07
    max_tickets_per_user INT NOT NULL DEFAULT 4   CHECK (max_tickets_per_user > 0), -- D-09
    session_minutes      INT NOT NULL DEFAULT 30  CHECK (session_minutes > 0),      -- D-07
    hold_minutes         INT NOT NULL DEFAULT 5   CHECK (hold_minutes > 0),         -- D-08
    queue_tail           INT NOT NULL DEFAULT 0,  -- posisi antrean terakhir (Modul 4)
    admitted_up_to       INT NOT NULL DEFAULT 0,  -- pointer admitter (Modul 4)
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hari event: DAY-1, DAY-2.
CREATE TABLE event_days (
    id        BIGSERIAL PRIMARY KEY,
    event_id  BIGINT NOT NULL REFERENCES events(id), -- FK: hari wajib milik event yang ada
    label     TEXT NOT NULL,                          -- 'DAY-1'
    show_date DATE NOT NULL,
    UNIQUE (event_id, label)                          -- satu event tak boleh punya dua 'DAY-1'
);

-- Kategori per hari: VIP, CAT 1, Festival. Di sinilah stok hidup.
CREATE TABLE categories (
    id           BIGSERIAL PRIMARY KEY,
    event_day_id BIGINT NOT NULL REFERENCES event_days(id),
    name         TEXT NOT NULL,
    price        BIGINT NOT NULL CHECK (price >= 0),         -- rupiah, integer (hindari float untuk uang)
    total_seats  INT NOT NULL CHECK (total_seats > 0),
    available    INT NOT NULL,                               -- stok tersisa, dikurangi saat hold (Modul 3)
    next_seat    INT NOT NULL DEFAULT 1,                     -- nomor seat berikutnya (Modul 5)
    has_seats    BOOLEAN NOT NULL DEFAULT TRUE,              -- false = berdiri/Festival, tanpa seat_no (D-10)
    UNIQUE (event_day_id, name),
    -- Jaring pengaman anti-oversell (D-11): stok tak pernah minus / melebihi total.
    CHECK (available BETWEEN 0 AND total_seats),
    CHECK (next_seat BETWEEN 1 AND total_seats + 1)
);