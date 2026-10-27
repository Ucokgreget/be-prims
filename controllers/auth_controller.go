package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var validate = validator.New()

const invalidCredentialMsg = "No. Badge atau Password salah."

func Login(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input request.LoginRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Nomor Badge dan password wajib diisi.",
			})
			return
		}

		if err := validate.Struct(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": err.Error(),
			})
			return
		}

		var user models.User
		if err := db.Preload("Department").Preload("Company").
			Where("badge_number = ? AND company_id = ?", input.BadgeNumber, input.CompanyID).
			First(&user).Error; err != nil {
			// Cek apakah badge terdaftar di company lain sebelum menyimpulkan kredensial salah.
			var count int64
			db.Model(&models.User{}).Where("badge_number = ?", input.BadgeNumber).Count(&count)
			if count > 0 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"status":  "error",
					"message": "No. Badge Anda tidak terdaftar di anak perusahaan ini. Silakan pilih anak perusahaan yang sesuai.",
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": invalidCredentialMsg,
			})
			return
		}

		if user.Status != models.StatusActive {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"status":  "error",
				"message": "Akun Anda tidak aktif. Silakan hubungi Admin Anak Perusahaan Anda.",
			})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": invalidCredentialMsg,
			})
			return
		}

		token, expiresIn, err := middleware.GenerateToken(middleware.AuthClaims{
			UserID:       user.Id,
			BadgeNumber:  user.BadgeNumber,
			Name:         user.Name,
			Role:         user.Role,
			CompanyID:    user.CompanyID,
			DepartmentID: user.DepartmentID,
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal membuat token.",
			})
			return
		}

		user.Password = ""
		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Login berhasil.",
			"data": gin.H{
				"token":      token,
				"token_type": "Bearer",
				"expires_in": expiresIn,
				"user":       user,
			},
		})
	}
}

// Me mengembalikan profil user dari token yang valid (handshake FE saat refresh).
func Me(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var user models.User
		err := db.Preload("Department").Preload("Company").
			First(&user, claims.UserID).Error
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Token tidak valid atau telah kedaluwarsa",
			})
			return
		}

		user.Password = ""
		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "OK",
			"data":    gin.H{"user": user},
		})
	}
}
