package httpx

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	ctxUserID = "user_id"
	ctxRole   = "role"
)

// AuthRequired memverifikasi JWT di header Authorization.
func AuthRequired(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Format header: "Authorization: Bearer <token>".
		tokenStr, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			// Abort = hentikan rantai handler, route tujuan tidak dijalankan.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak ada"})
			return
		}

		claims := jwt.MapClaims{} // wadah isi token

		// ParseWithClaims: cek tanda tangan + exp, isi claims.
		token, err := jwt.ParseWithClaims(tokenStr, claims,
			// keyFunc: berikan secret untuk verifikasi tanda tangan.
			func(t *jwt.Token) (any, error) { return []byte(secret), nil },
			// Hanya terima HS256. Mencegah serangan ganti algoritma (mis. "none").
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			// Token tanpa exp ditolak.
			jwt.WithExpirationRequired(),
		)
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak valid"})
			return
		}

		// Ambil id user dari "sub" (string) lalu ubah ke int64.
		sub, err := claims.GetSubject()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak valid"})
			return
		}
		userID, err := strconv.ParseInt(sub, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "token tidak valid"})
			return
		}

		// Role: type assertion ke string. ok=false kalau tidak ada/bukan string.
		role, _ := claims["role"].(string)

		// Simpan ke context supaya handler berikutnya bisa membaca.
		c.Set(ctxUserID, userID)
		c.Set(ctxRole, role)

		c.Next() // lanjut ke handler berikutnya
	}
}

// RequireRole menolak request kalau role tidak cocok.
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(ctxRole) != role {
			// 403 sudah login
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
