package config

import (
	"errors"
	"log"
	"os"

	"github.com/joho/godotenv"
)

// config tampung semua nilai dari env
type Config struct {
	HTTPPort    string
	DATABASEURL string
	JWTSecret   string
}

// Load membaca .env lalu environment, dan mengembalikan Config.
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env tidak ditemukan")
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

	return Config{HTTPPort: port, DATABASEURL: dbURL}, nil
}
