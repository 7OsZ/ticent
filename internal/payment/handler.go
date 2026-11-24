package payment

import (
	"errors"
	"log/slog"
	"net/http"
	"ticent/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Pay(c *gin.Context) {
	orderID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	res, err := h.service.Pay(httpx.UserID(c), orderID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) Notification(c *gin.Context) {
	var payload NotificationPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.HandleNotification(payload); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Sync(c *gin.Context) {
	orderID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	ord, err := h.service.SyncOrder(httpx.UserID(c), orderID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, ord)
}

func (h *Handler) RegisterRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	r.POST("/orders/:id/pay", authMW, h.Pay)
	r.POST("/orders/:id/sync", authMW, h.Sync)
	r.POST("/payments/midtrans/notification", h.Notification)
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidSignature):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAmountMismatch):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrOrderNotPayable):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		slog.Error("error tak terduga", "err", err, "request_id", httpx.RequestIDFrom(c))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "terjadi kesalahan"})
	}
}
