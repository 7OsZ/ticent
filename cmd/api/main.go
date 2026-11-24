package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"ticent/internal/auth"
	"ticent/internal/event"
	"ticent/internal/order"
	"ticent/internal/payment"
	"ticent/internal/platform/config"
	"ticent/internal/platform/database"
	"ticent/internal/platform/httpx"
	"ticent/internal/queue"
	"ticent/internal/ticket"

	"github.com/gin-gonic/gin"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("gagal memuat config", "err", err)
		os.Exit(1)
	}

	db, err := database.Open(cfg.DATABASEURL)
	if err != nil {
		slog.Error("gagal terhubung ke database", "err", err)
		os.Exit(1)
	}
	slog.Info("terhubung ke database")

	if err := database.RunMigration(cfg.DATABASEURL); err != nil {
		slog.Error("gagal menjalankan migrasi", "err", err)
		os.Exit(1)
	}

	server := gin.New()
	server.Use(gin.Recovery(), httpx.RequestID(), httpx.RequestLogger())

	// /healthz mengecek database sungguhan, bukan sekadar membalas "ok".
	server.GET("/healthz", func(c *gin.Context) {
		sqlDB, err := db.DB()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		if err := sqlDB.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	authMW := httpx.AuthRequired(cfg.JWTSecret)

	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, cfg.JWTSecret)
	auth.NewHandler(authService).RegisterRoutes(server, authMW)

	eventRepo := event.NewRepository(db)
	eventService := event.NewService(eventRepo)
	event.NewHandler(eventService).RegisterRoutes(server, authMW)

	queueRepo := queue.NewRepository(db)
	queueService := queue.NewService(queueRepo, eventService)
	queue.NewHandler(queueService).RegisterRoutes(server, authMW)

	orderRepo := order.NewRepository(db)
	orderService := order.NewService(orderRepo, eventService, queueRepo)
	order.NewHandler(orderService).RegisterRoutes(server, authMW)

	ticketRepo := ticket.NewRepository(db)
	ticketService := ticket.NewService(ticketRepo)
	ticket.NewHandler(ticketService).RegisterRoutes(server, authMW)

	midtransGW := payment.NewMidtransGateway(cfg.MidtransServerKey, cfg.MidtransIsProduction)
	paymentService := payment.NewService(orderRepo, orderService, queueRepo, ticketRepo, authRepo, midtransGW, cfg.MidtransServerKey)
	payment.NewHandler(paymentService).RegisterRoutes(server, authMW)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var workers sync.WaitGroup
	workers.Go(func() { order.NewWorker(orderService, 30*time.Second).Run(ctx) })
	workers.Go(func() { queue.NewWorker(queueService, queue.WorkerInterval).Run(ctx) })

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		// Saat Ctrl+C, koneksi SSE antrean ikut berhenti sehingga Shutdown tidak tertahan.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go func() {
		slog.Info("server berjalan", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server berhenti", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("mematikan server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("gagal mematikan server dengan rapi", "err", err)
	}
	workers.Wait()
	slog.Info("server mati")
}
