package queue

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strconv" // ubah ID di URL (teks) jadi angka
	"time"

	"ticent/internal/platform/httpx" // baca ID user dari token

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Join: POST /events/:id/queue (wajib login). :id = ID event, mis. BMTH.
func (h *Handler) Join(c *gin.Context) {
	eventID, ok := parseEventID(c)
	if !ok {
		return
	}

	// ID user dari token, bukan dari body: user tidak bisa mengantrekan orang lain.
	e, err := h.service.Join(httpx.UserID(c), eventID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, e) // 201: masuk antrean, position masih null
}

// Me: GET /events/:id/queue/me (wajib login). Posisi saya di antrean.
func (h *Handler) Me(c *gin.Context) {
	eventID, ok := parseEventID(c)
	if !ok {
		return
	}

	v, err := h.service.Status(httpx.UserID(c), eventID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// Heartbeat: POST /events/:id/queue/heartbeat (wajib login). "Saya masih di sini."
func (h *Handler) Heartbeat(c *gin.Context) {
	eventID, ok := parseEventID(c)
	if !ok {
		return
	}

	v, err := h.service.Heartbeat(httpx.UserID(c), eventID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// Leave: DELETE /events/:id/queue (wajib login). Keluar dari antrean.
func (h *Handler) Leave(c *gin.Context) {
	eventID, ok := parseEventID(c)
	if !ok {
		return
	}

	if err := h.service.Leave(httpx.UserID(c), eventID); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent) // 204: berhasil, tanpa isi
}

// Stream: GET /events/:id/queue/stream (wajib login). Server-Sent Events:
// koneksi dibiarkan terbuka, server mengirim posisi terbaru setiap SSEInterval.
// Selama koneksi terbuka, user dianggap hadir (tidak perlu heartbeat terpisah).
func (h *Handler) Stream(c *gin.Context) {
	eventID, ok := parseEventID(c)
	if !ok {
		return
	}
	userID := httpx.UserID(c)

	// Cek pertama sebelum aliran dibuka, supaya error masih bisa dikirim sebagai JSON biasa.
	v, err := h.service.Presence(userID, eventID)
	if err != nil {
		writeError(c, err)
		return
	}

	c.Header("Cache-Control", "no-cache")
	ticker := time.NewTicker(SSEInterval)
	defer ticker.Stop()

	first := true
	// c.Stream memanggil fungsi ini berulang-ulang sampai hasilnya false atau klien pergi.
	c.Stream(func(w io.Writer) bool {
		if !first {
			select {
			case <-c.Request.Context().Done(): // klien menutup koneksi / server dimatikan
				return false
			case <-ticker.C:
			}
			if v, err = h.service.Presence(userID, eventID); err != nil {
				log.Println("stream antrean error:", err)
				return false
			}
		}
		first = false

		if !v.Live() {
			// Kabar terakhir, nama event = status, mis. "event: expired" atau "event: checkout".
			c.SSEvent(v.Status, v)
			return false
		}
		c.SSEvent("position", v)
		return true
	})
}

// RegisterRoutes mendaftarkan alamat modul antrean.
func (h *Handler) RegisterRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	r.POST("/events/:id/queue", authMW, h.Join)
	r.DELETE("/events/:id/queue", authMW, h.Leave)
	r.GET("/events/:id/queue/me", authMW, h.Me)
	r.POST("/events/:id/queue/heartbeat", authMW, h.Heartbeat)
	r.GET("/events/:id/queue/stream", authMW, h.Stream)
}

// parseEventID membaca :id dari URL. Tidak valid = langsung dijawab 400.
func parseEventID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id tidak valid"})
		return 0, false
	}
	return id, true
}

// writeError memilih status HTTP untuk setiap error dari service.
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrEventNotFound), errors.Is(err, ErrNotInQueue):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAlreadyInQueue):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		// Error tak terduga: dicatat di server, tidak dibocorkan ke klien.
		log.Println("error tak terduga:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "terjadi kesalahan"})
	}
}
