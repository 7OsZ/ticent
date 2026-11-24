package config

import (
	"errors"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
)

// config tampung semua nilai dari env
type Config struct {
	HTTPPort             string
	DATABASEURL          string
	JWTSecret            string
	MidtransServerKey    string
	MidtransClientKey    string
	MidtransIsProduction bool
}

// Load membaca .env lalu environment, dan mengembalikan Config.
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil {
		slog.Debug(".env tidak ditemukan, memakai environment sistem")
	}

	// os.Getenv mengembalikan "" kalau variabel tidak ada.
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, errors.New("DATABASE_URL belum diisi")
	}

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}

	// Tanpa secret, token ditandatangani dengan kunci kosong:
	// siapa pun bisa membuat token admin palsu. Lebih baik server menolak start.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return Config{}, errors.New("JWT_SECRET belum diisi")
	}

	// Kunci server Midtrans wajib ada supaya tanda tangan webhook tidak bisa dipalsukan dengan kunci kosong.
	midtransServerKey := os.Getenv("MIDTRANS_SERVER_KEY")
	if midtransServerKey == "" {
		return Config{}, errors.New("MIDTRANS_SERVER_KEY belum diisi")
	}

	return Config{
		HTTPPort:             port,
		DATABASEURL:          dbURL,
		JWTSecret:            jwtSecret,
		MidtransServerKey:    midtransServerKey,
		MidtransClientKey:    os.Getenv("MIDTRANS_CLIENT_KEY"),
		MidtransIsProduction: os.Getenv("MIDTRANS_IS_PRODUCTION") == "true",
	}, nil
}
