DROP TABLE IF EXISTS tickets;

ALTER TABLE orders
    DROP COLUMN IF EXISTS snap_redirect_url,
    DROP COLUMN IF EXISTS snap_token;

ALTER TABLE events ALTER COLUMN hold_minutes SET DEFAULT 5;
