package main

import (
	"log"
	"net/http"

	"ticent/internal/auth"
	"ticent/internal/event"
	"ticent/internal/platform/config"
	"ticent/internal/platform/database"
	"ticent/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

func main() {
	// config .env dipanggil dari config.load
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Gagal memuat config", err)
	}

	// Database
	db, err := database.Open(cfg.DATABASEURL)
	if err != nil {
		log.Fatal("Gagal terhubung ke database", err)
	}
	log.Println("Berhasil terhubung ke database")

	// Migration
	if err := database.RunMigration(cfg.DATABASEURL); err != nil {
		log.Fatal("gagal menjalankan migrasi: ", err)
	}

	// router
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

	// auth middleware
	authMW := httpx.AuthRequired(cfg.JWTSecret)

	// wiring auth
	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, cfg.JWTSecret)
	authHandler := auth.NewHandler(authService)
	authHandler.RegisterRoutes(server, authMW)

	eventRepo := event.NewRepository(db)
	eventService := event.NewService(eventRepo)
	eventHandler := event.NewHandler(eventService)
	eventHandler.RegisterRoutes(server, authMW)

	// 8) shortcut: route uji admin, hapus di Modul 2 saat endpoint admin asli ada
	server.GET("/admin/ping", authMW, httpx.RequireRole("admin"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "admin ok"})
	})

	// 9) Jalankan server
	if err := server.Run(":" + cfg.HTTPPort); err != nil {
		log.Fatal("server berhenti: ", err)
	}
}
