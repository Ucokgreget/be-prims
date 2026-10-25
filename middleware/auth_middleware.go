package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const ClaimsKey = "auth_claims"

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		unauthorized := func() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Unauthorized",
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

func Claims(c *gin.Context) *AuthClaims {
	return c.MustGet(ClaimsKey).(*AuthClaims)
}

func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if Claims(c).Role != role {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"status":  "error",
				"message": "Tidak diizinkan untuk melakukan action ini",
			})
			return
		}
		c.Next()
	}
}
