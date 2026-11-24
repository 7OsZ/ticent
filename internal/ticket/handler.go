package ticket

import (
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

func (h *Handler) ListMyTickets(c *gin.Context) {
	tickets, err := h.service.ListMyTickets(httpx.UserID(c))
	if err != nil {
		slog.Error("error tak terduga", "err", err, "request_id", httpx.RequestIDFrom(c))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "terjadi kesalahan"})
		return
	}
	if tickets == nil {
		tickets = []TicketView{}
	}
	c.JSON(http.StatusOK, tickets)
}

func (h *Handler) RegisterRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	r.GET("/tickets", authMW, h.ListMyTickets)
}
