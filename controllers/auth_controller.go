package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const invalidCredentialMsg = "NPP/No. Badge atau Password salah."

// userResponse adalah data user yang aman dikirim ke FE (tanpa password hash).
type userResponse struct {
	ID         uint             `json:"id"`
	NPP        string           `json:"npp"`
	Name       string           `json:"name"`
	Jabatan    string           `json:"jabatan"`
	Role       string           `json:"role"`
	Department *deptResponse    `json:"department,omitempty"`
	Company    *companyResponse `json:"company,omitempty"`
}

type deptResponse struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type companyResponse struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

func toUserResponse(user *models.User) userResponse {
	res := userResponse{
		ID: user.Id, NPP: user.NPP, Name: user.Name,
		Jabatan: user.Jabatan, Role: user.Role,
	}
	if user.Department != nil {
		res.Department = &deptResponse{ID: user.Department.Id, Name: user.Department.Name}
	}
	if user.Company != nil {
		res.Company = &companyResponse{ID: user.Company.Id, Name: user.Company.Name}
	}
	return res
}

func Login(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input request.LoginRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "NPP dan password wajib diisi.",
			})
			return
		}

		npp := input.NPP
		if npp == "" {
			npp = input.BadgeNumber // alias badge_number
		}
		if npp == "" || input.Password == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "NPP dan password wajib diisi.",
			})
			return
		}

		var user models.User
		err := db.Preload("Department").Preload("Company").
			Where("npp = ?", npp).First(&user).Error

		// ponytail: dummy bcrypt compare mencegah timing leak pada user tidak ditemukan.
		// Angka tidak penting — hanya untuk menyamakan waktu eksekusi.
		const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy" // nosemgrep: generic.secrets.security.detected-bcrypt-hash.detected-bcrypt-hash — dummy untuk timing equalization
		if err != nil {
			bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(input.Password))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": invalidCredentialMsg,
			})
			return
		}

		if user.Status != models.StatusActive {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"status":     "error",
				"message":    "Akun Anda tidak aktif. Silakan hubungi Admin Anak Perusahaan Anda.",
				"error_code": "ACCOUNT_INACTIVE",
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
			NPP:          user.NPP,
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

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Login berhasil.",
			"data": gin.H{
				"token":      token,
				"token_type": "Bearer",
				"expires_in": expiresIn,
				"user":       toUserResponse(&user),
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
				"success":    false,
				"message":    "Token tidak valid atau telah kedaluwarsa",
				"error_code": "UNAUTHORIZED",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "OK",
			"data":    gin.H{"user": toUserResponse(&user)},
		})
	}
}
