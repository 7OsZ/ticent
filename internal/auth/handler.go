package auth

import (
	"errors"
	"net/http"

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
