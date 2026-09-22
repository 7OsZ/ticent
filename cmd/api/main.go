package main

import (
	"log"
	"net/http"

	"ticent/internal/platform/config"
	"ticent/internal/platform/database"

	"github.com/gin-gonic/gin"
)

func main() {
	// config .env dipanggil dari config.load
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Gagal memuat config", err)
	}

	// buka database. db bertipe *gorm.DB.
	db, err := database.Open(cfg.DATABASEURL)
	if err != nil {
		log.Fatal("Gagal terhubung ke database", err)
	}
	log.Println("Berhasil terhubung ke database")

	if err := database.RunMigration(cfg.DATABASEURL); err != nil {
		log.Fatal("gagal menjalankan migrasi: ", err)
	}

	server := gin.Default()

	// /healthz mengecek database, bukan sekadar balas "ok".
	server.GET("/healthz", func(c *gin.Context) {
		// Ambil *sql.DB dari handle GORM untuk ping.
		sqlDB, err := db.DB()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		// Ping pakai context milik request, jadi timeout klien ikut berlaku.
		if err := sqlDB.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if err := server.Run(":" + cfg.HTTPPort); err != nil {
		log.Fatal("Server berhenti: ", err)
	}
}
