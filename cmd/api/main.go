package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"ticent/internal/auth"
	"ticent/internal/event"
	"ticent/internal/order"
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

	orderRepo := order.NewRepository(db)
	orderService := order.NewService(orderRepo, eventService)
	orderHandler := order.NewHandler(orderService)
	orderHandler.RegisterRoutes(server, authMW)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go order.NewWorker(orderService, 30*time.Second).Run(ctx)

	// 8) shortcut: route uji admin, hapus di Modul 2 saat endpoint admin asli ada
	server.GET("/admin/ping", authMW, httpx.RequireRole("admin"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "admin ok"})
	})

	// 9) Jalankan server
	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: server,
	}

	go func() {
		log.Printf("server berjalan di port %s", cfg.HTTPPort)
		// ErrServerClosed BUKAN masalah: itu tanda server dimatikan dengan sengaja.
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server berhenti: ", err)
		}
	}()

	<-ctx.Done()
	log.Println("mematikan server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Println("gagal mematikan server dengan rapi:", err)
	}
	log.Println("server mati")
}
