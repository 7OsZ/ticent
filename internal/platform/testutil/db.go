// Package testutil berisi bantuan untuk test yang memakai PostgreSQL sungguhan (D-17).
// Semua data di TEST_DATABASE_URL DIHAPUS setiap kali DB(t) dipanggil.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"ticent/internal/platform/database"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

// DB membuka database test yang sudah dimigrasi dan dikosongkan.
// Test dilewati (skip) kalau TEST_DATABASE_URL belum diisi.
func DB(t *testing.T) *gorm.DB {
	t.Helper()

	// File ini ada di <root>/internal/platform/testutil, jadi naik 3 folder = root repo.
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	_ = godotenv.Load(filepath.Join(root, ".env")) // .env boleh tidak ada (mis. di CI)

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL belum diisi")
	}
	// Pengaman: test menghapus semua data. Jangan sampai mengenai database dev.
	if dsn == os.Getenv("DATABASE_URL") {
		t.Fatal("TEST_DATABASE_URL sama dengan DATABASE_URL; test akan menghapus data dev")
	}

	// Migrasi membaca file://migrations (path relatif), jadi jalankan dari root repo.
	t.Chdir(root)
	if err := database.RunMigration(dsn); err != nil {
		t.Fatal("migrasi test gagal:", err)
	}

	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal("koneksi database test gagal:", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})

	err = db.Exec(`TRUNCATE users, events, event_days, categories, user_event_quota,
		orders, order_items, queue_entries RESTART IDENTITY CASCADE`).Error
	if err != nil {
		t.Fatal("gagal mengosongkan database test:", err)
	}
	return db
}

// SeedUsers membuat n user biasa dan mengembalikan ID-nya.
func SeedUsers(t *testing.T, db *gorm.DB, n int) []int64 {
	t.Helper()
	var ids []int64
	err := db.Raw(
		`INSERT INTO users (email, full_name, password_hash, role)
		 SELECT 'user' || g || '@test.local', 'User ' || g, 'x', 'user'
		   FROM generate_series(1, ?) AS g
		 RETURNING id`, n,
	).Scan(&ids).Error
	if err != nil {
		t.Fatal("seed user gagal:", err)
	}
	return ids
}

// Seed = data event hasil SeedEvent.
type Seed struct {
	EventID    int64
	DayID      int64
	CategoryID int64
}

// SeedEvent membuat event published yang jam war-nya sudah lewat 1 jam,
// dengan satu hari (hari ini, tanggal WIB) dan satu kategori berisi stock tiket.
func SeedEvent(t *testing.T, db *gorm.DB, maxActive, stock int) Seed {
	t.Helper()
	var s Seed

	err := db.Raw(
		`INSERT INTO events (name, venue, status, sale_starts_at, max_active_sessions,
		                     max_tickets_per_user, session_minutes, hold_minutes)
		 VALUES ('BMTH Test', 'Jakarta', 'published', ?, ?, 4, 30, 30) RETURNING id`,
		time.Now().Add(-time.Hour), maxActive,
	).Scan(&s.EventID).Error
	if err != nil {
		t.Fatal("seed event gagal:", err)
	}

	// Kolom DATE disimpan sebagai tanggal kalender WIB (sama dengan todayDate() di event).
	wib := time.Now().In(time.FixedZone("WIB", 7*60*60))
	today := fmt.Sprintf("%04d-%02d-%02d", wib.Year(), wib.Month(), wib.Day())
	err = db.Raw(
		`INSERT INTO event_days (event_id, label, show_date) VALUES (?, 'DAY-1', ?) RETURNING id`,
		s.EventID, today,
	).Scan(&s.DayID).Error
	if err != nil {
		t.Fatal("seed hari gagal:", err)
	}

	err = db.Raw(
		`INSERT INTO categories (event_day_id, name, price, total_seats, available, has_seats)
		 VALUES (?, 'Festival', 500000, ?, ?, false) RETURNING id`,
		s.DayID, stock, stock,
	).Scan(&s.CategoryID).Error
	if err != nil {
		t.Fatal("seed kategori gagal:", err)
	}
	return s
}
