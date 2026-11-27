package middleware

import (
	"errors"
	"net/http"
	"strings"

	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ClaimsKey = "auth_claims"
const InactiveCompanyMessage = "Anak Perusahaan Anda saat ini berstatus nonaktif. Silakan hubungi Administrator Pusri."

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

func RequireActiveCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := Claims(c)
		if CheckCompanyAccess(c, db, claims.Role, claims.CompanyID, claims.CompanyAuthVersion) {
			c.Next()
		}
	}
}

// CheckCompanyAccess also guards the PDF stream, which accepts a distinct viewer token.
func CheckCompanyAccess(c *gin.Context, db *gorm.DB, role string, id uint, version int64) bool {
	if role == models.RoleSuperAdmin { // System-wide administrator, not a tenant employee.
		return true
	}
	var company models.Company
	err := db.WithContext(c.Request.Context()).Select("status", "auth_version").Where("id = ?", id).Take(&company).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "message": "Sesi tidak berlaku. Silakan login kembali."})
	case err != nil:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal memeriksa status perusahaan."})
	case company.Status != models.StatusActive:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"status": "error", "message": InactiveCompanyMessage})
	case company.AuthVersion != version:
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "message": "Sesi tidak berlaku. Silakan login kembali."})
	default:
		return true
	}
	return false
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
