package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ClaimsKey adalah context key tempat AuthClaims disimpan.
const ClaimsKey = "auth_claims"

// AuthMiddleware memverifikasi Bearer JWT dan menyimpan claims ke context.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		unauthorized := func() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success":    false,
				"message":    "Token tidak valid atau telah kedaluwarsa",
				"error_code": "UNAUTHORIZED",
			})
		}

		if !strings.HasPrefix(header, "Bearer ") {
			unauthorized()
			return
		}

		claims, err := ParseToken(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			unauthorized()
			return
		}

		c.Set(ClaimsKey, claims)
		c.Next()
	}
}

// Claims mengambil AuthClaims dari context; hanya dipanggil setelah AuthMiddleware.
func Claims(c *gin.Context) *AuthClaims {
	return c.MustGet(ClaimsKey).(*AuthClaims)
}

// RequireRole menolak request dengan role selain yang diizinkan.
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if Claims(c).Role != role {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success":    false,
				"message":    "Anda tidak memiliki akses untuk operasi ini.",
				"error_code": "FORBIDDEN",
			})
			return
		}
		c.Next()
	}
}
