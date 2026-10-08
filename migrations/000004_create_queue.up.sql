CREATE TABLE queue_entries (
    id                 BIGSERIAL PRIMARY KEY,
    event_id           BIGINT NOT NULL REFERENCES events(id),
    user_id            BIGINT NOT NULL REFERENCES users(id),
    position           INT,
    status             TEXT NOT NULL DEFAULT 'waiting'
                       CHECK (status IN ('waiting', 'active', 'checkout', 'done', 'expired', 'left')),

    joined_at          TIMESTAMPTZ NOT NULL DEFAULT now(), -- kapan mulai antre
    last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(), -- sinyal "masih membuka halaman" terakhir
    admitted_at        TIMESTAMPTZ,                        -- kapan lolos antrean
    session_expires_at TIMESTAMPTZ                         -- batas waktu belanja (mis. lolos + 30 menit)
);

CREATE UNIQUE INDEX queue_one_live_entry
    ON queue_entries (event_id, user_id)
    WHERE status IN ('waiting', 'active', 'checkout');

    CREATE UNIQUE INDEX queue_position_unique
    ON queue_entries (event_id, position)
    WHERE position IS NOT NULL;

    CREATE INDEX queue_admit_idx ON queue_entries (event_id, status, position);