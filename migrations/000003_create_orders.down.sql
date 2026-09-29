-- Hapus dari yang paling bergantung: item dulu, lalu order, lalu kuota.
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS user_event_quota;