package httpx

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	HeaderRequestID = "X-Request-ID"
	ctxUserID       = "user_id"
	ctxRole         = "role"
	ctxRequestID    = "request_id"
)

// RequestID meneruskan header X-Request-ID dari klien atau membuat ID baru bila kosong,
// supaya satu request dapat dilacak dari awal sampai log database/error.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(HeaderRequestID))
		if !isSafeRequestID(id) {
			id = rand.Text()
		}
		c.Set(ctxRequestID, id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

func isSafeRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := range len(s) {
		ch := s[i]
		isAlphaNum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
		if !isAlphaNum && ch != '-' && ch != '_' && ch != '.' {
			return false
		}
	}
	return true
}

func RequestIDFrom(c *gin.Context) string {
	return c.GetString(ctxRequestID)
}

// RequestLogger mencatat setiap request HTTP secara terstruktur lewat log/slog.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			"request_id", RequestIDFrom(c),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		}
		if status >= http.StatusInternalServerError {
			slog.Error("http request", attrs...)
			return
		}
		slog.Info("http request", attrs...)
	}
}

// AuthRequired memverifikasi JWT di header Authorization.
func AuthRequired(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak ada",
			})
			return
		}

		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims,
			func(t *jwt.Token) (any, error) { return []byte(secret), nil },
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak valid",
			})
			return
		}

		sub, err := claims.GetSubject()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak valid",
			})
			return
		}
		userID, err := strconv.ParseInt(sub, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak valid",
			})
			return
		}

		role, _ := claims["role"].(string)
		c.Set(ctxUserID, userID)
		c.Set(ctxRole, role)

		c.Next()
	}
}

// RequireRole menolak request kalau role tidak cocok.
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(ctxRole) != role {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "akses ditolak",
			})
			return
		}
		c.Next()
	}
}

func UserID(c *gin.Context) int64 {
	return c.GetInt64(ctxUserID)
}

func Role(c *gin.Context) string {
	return c.GetString(ctxRole)
}

// ParseID membaca :id dari URL dan memastikan berupa bilangan bulat positif.
// Jika tidak valid, fungsi langsung mengirim 400 dan mengembalikan false.
func ParseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID tidak valid"})
		return 0, false
	}
	return id, true
}
