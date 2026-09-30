package order

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"ticent/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// checkoutItemRequest = satu baris keranjang dari klien.
type checkoutItemRequest struct {
	CategoryID int64 `json:"category_id" binding:"required"`
	Qty        int   `json:"qty" binding:"required,min=1"`
}

// checkoutRequest = isi body POST /orders. Satu checkout untuk SATU hari.
type checkoutRequest struct {
	EventDayID int64 `json:"event_day_id" binding:"required"`
	// dive = "periksa juga aturan binding di setiap isi daftar ini".
	// Tanpa dive, Gin hanya mengecek daftarnya ada, bukan isinya.
	Items []checkoutItemRequest `json:"items" binding:"required,min=1,dive"`
}

// Checkout: POST /orders (wajib login) Menahan tiket selama 30 menit sambil menunggu pembayaran.
func (h *Handler) Checkout(c *gin.Context) {
	var req checkoutRequest
	// Baca body JSON dan cek aturan binding. Gagal = input salah (400).
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	// Ubah bentuk request (urusan HTTP) menjadi bentuk input service (urusan bisnis).
	// Dipisah supaya service tidak tahu-menahu soal JSON.
	items := make([]ItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, ItemInput{CategoryID: it.CategoryID, Qty: it.Qty})
	}

	// httpx.UserID membaca ID pembeli dari TOKEN, bukan dari body.
	o, err := h.service.Checkout(httpx.UserID(c), CheckoutInput{
		EventDayID: req.EventDayID,
		Items:      items,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, o) // 201

}

// GetOrder: GET /orders/:id (wajib login)
func (h *Handler) GetOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "ID tidak valid",
		})
		return
	}

	// ID pembeli dari token, supaya user hanya bisa melihat order miliknya sendiri.
	o, err := h.service.GetOrder(httpx.UserID(c), id)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, o)
}

// RegisterRoutes mendaftarkan alamat modul order.
// authMW = middleware cek token, sama dengan yang dipakai modul lain.
func (h *Handler) RegisterRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	r.POST("/orders", authMW, h.Checkout)
	r.GET("/orders/:id", authMW, h.GetOrder)
}

// writeError memilih status HTTP yang tepat untuk setiap error dari service.
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}) // 400: isi keranjang salah
	case errors.Is(err, ErrDayNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()}) // 404: hari tidak tersedia
	case errors.Is(err, ErrSaleNotStarted),
		errors.Is(err, ErrQuotaExceeded),
		errors.Is(err, ErrSoldOut),
		errors.Is(err, ErrPendingExists):
		// 409 = permintaannya masuk akal, tapi bentrok dengan kondisi saat ini
		// (belum jam war, jatah habis, tiket habis, masih ada order belum bayar).
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrDayNotFound), errors.Is(err, ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()}) // 404: data tidak ada
	default:
		// Error tak terduga (mis. database putus): dicatat di server,
		// tidak dikirim ke klien supaya info internal tidak bocor.
		log.Println("error tak terduga:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "terjadi kesalahan"})
	}
}
