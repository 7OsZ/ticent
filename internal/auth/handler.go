package auth

import (
	"errors"
	"net/http"
	"ticent/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

// Handler memgang service. Disuntik via constructr
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// registerRequest
type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	FullName string `json:"full_name" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// register handler: POST /auth/register
func (h *Handler) Register(c *gin.Context) {
	var req registerRequest

	// Gagal = input not valid 400
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	user, err := h.service.Register(req.Email, req.FullName, req.Password)
	if err != nil {
		// Email sudah terpakai
		if errors.Is(err, ErrEmailTaken) {
			c.JSON(http.StatusConflict, gin.H{
				"error": err.Error(),
			})
			return
		}
		// Password pendek
		if errors.Is(err, ErrPasswordTooShort) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
			})
			return
		}
		// Error tdk terduga
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Terjadi kesalahan",
		})
		return
	}
	// sukses
	c.JSON(http.StatusCreated, user)
}

func (h *Handler) Login(c *gin.Context) {
	var req loginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	token, err := h.service.Login(req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Terjadi kesalahan",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
	})
}

// Me
func (h *Handler) Me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"user_id": httpx.UserID(c),
		"role":    httpx.Role(c),
	})
}

// RegisterRoutes sekarang menerima middleware auth dari luar.
func (h *Handler) RegisterRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	grp := r.Group("/auth")
	grp.POST("/register", h.Register)
	grp.POST("/login", h.Login)
	grp.GET("/me", authMW, h.Me) // authMW jalan dulu, baru Me
}
