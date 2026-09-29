package event

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"ticent/internal/platform/httpx"
	"time"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// createEventRequest = bentuk JSON yang dikirim admin saat membuat event.
type createEventRequest struct {
	Name              string    `json:"name" binding:"required"`
	Venue             string    `json:"venue" binding:"required"`
	SaleStartsAt      time.Time `json:"sale_starts_at" binding:"required"`
	MaxActiveSessions *int      `json:"max_active_sessions"`
	MaxTicketsPerUser *int      `json:"max_tickets_per_user"`
	SessionMinutes    *int      `json:"session_minutes"`
}

// addDayRequest = bentuk JSON untuk menambah hari
type addDayRequest struct {
	Label    string `json:"label" binding:"required"`
	ShowDate string `json:"show_date" binding:"required"`
}

// addCategoryRequest = bentuk JSON untuk menambah jenis tiket.
type addCategoryRequest struct {
	Name string `json:"name" binding:"required"`
	// Harga pakai pointer supaya harga 0 (tiket gratis) tetap dianggap diisi.
	// Tanpa pointer, "required" menolak angka 0.
	Price      *int64 `json:"price" binding:"required,min=0"`
	TotalSeats int    `json:"total_seats" binding:"required,min=1"`
	// Boleh tidak dikirim. Kalau kosong, dianggap kategori bernomor kursi.
	// Kirim false untuk kategori berdiri seperti Festival.
	HasSeats *bool `json:"has_seats"`
}

// CreateEvent: POST /admin/events
func (h *Handler) CreateEvent(c *gin.Context) {
	var req createEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	e, err := h.service.CreateEvent(CreateEventInput{
		Name:              req.Name,
		Venue:             req.Venue,
		SaleStartAt:       req.SaleStartsAt,
		MaxActiveSessions: req.MaxActiveSessions,
		MaxTicketsPerUser: req.MaxTicketsPerUser,
		SessionMinutes:    req.SessionMinutes,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, e) // 201 = berhasil dibuat
}

// AddDay: POST /admin/events/:id/days
func (h *Handler) AddDay(c *gin.Context) {
	// :id di URL adalah ID event.
	eventID, ok := parseID(c)
	if !ok {
		return
	}

	var req addDayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	d, err := h.service.AddDay(eventID, req.Label, req.ShowDate)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, d)

}

// AddCategory: POST /admin/days/:id/categories
func (h *Handler) AddCategory(c *gin.Context) {
	dayID, ok := parseID(c)
	if !ok {
		return
	}

	var req addCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	// Kalau admin tidak mengirim has_seats, anggap kategori bernomor kursi.
	hasSeats := true
	if req.HasSeats != nil {
		hasSeats = *req.HasSeats
	}

	cat, err := h.service.AddCategory(dayID, CreateCategoryInput{
		Name:       req.Name,
		Price:      *req.Price,
		TotalSeats: req.TotalSeats,
		HasSeats:   hasSeats,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, cat)
}

// Publish: POST /admin/events/:id/publish
func (h *Handler) Publish(c *gin.Context) {
	eventID, ok := parseID(c)
	if !ok {
		return
	}

	if err := h.service.Publish(eventID); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "published",
	})
}

// ListCatalog: GET /catalog (publik, tanpa login)
func (h *Handler) ListCatalog(c *gin.Context) {
	days, err := h.service.ListCatalog()
	if err != nil {
		writeError(c, err)
		return
	}

	if days == nil {
		days = []EventDay{}
	}
	c.JSON(http.StatusOK, days)
}

// GetCatalogDay: GET /catalog/:id (publik). :id = ID hari
func (h *Handler) GetCatalogDay(c *gin.Context) {
	dayID, ok := parseID(c)
	if !ok {
		return
	}

	d, err := h.service.GetCatalogDay(dayID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// ROUTES
func (h *Handler) RegisterRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	admin := r.Group("/admin", authMW, httpx.RequireRole("admin"))
	admin.POST("/events", h.CreateEvent)
	admin.POST("/events/:id/days", h.AddDay)
	admin.POST("/days/:id/categories", h.AddCategory)
	admin.POST("/events/:id/publish", h.Publish)

	// Katalog terbuka untuk siapa saja, tanpa login.
	r.GET("/catalog", h.ListCatalog)
	r.GET("/catalog/:id", h.GetCatalogDay)
}

// parseID membaca :id dari URL dan mengubahnya menjadi angka.
func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "ID tidak valid",
		})
	}
	return id, true
}

// writeError memilih status HTTP yang tepat untuk setiap error dari service. Disatukan di sini supaya setiap handler tidak menulis ulang aturan yang sama.
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error()}) // 400: input salah
	case errors.Is(err, ErrEventNotFound), errors.Is(err, ErrDayNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"error": err.Error()}) // 404: data tidak ada
	case errors.Is(err, ErrNotDraft), errors.Is(err, ErrDuplicate):
		c.JSON(http.StatusConflict, gin.H{
			"error": err.Error()}) // 409: bentrok dengan kondisi data
	default:
		// Error tak terduga (mis. database putus). Detailnya dicatat di server, tapi TIDAK dikirim ke klien supaya info internal tidak bocor.
		log.Println("error tak terduga:", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "terjadi kesalahan"})
	}
}
