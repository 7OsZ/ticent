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

	_ = db // Go menolak variabel tak terpakai, jadi ini penanda sementara.

	server := gin.Default()

	server.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "OK"})
	})

	if err := server.Run(":" + cfg.HTTPPort); err != nil {
		log.Fatal("Server berhenti: ", err)
	}

}
