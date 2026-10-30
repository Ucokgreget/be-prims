package controllers

import (
	"net/http"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetUsersByJabatan: daftar karyawan berdasarkan jabatan (opsional via query
// param jabatan), company-scoped — untuk mengisi Dual Listbox Share To.
func GetUsersByJabatan(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		tx := db.Model(&models.User{}).
			Where("company_id = ? AND status = ?", claims.CompanyID, models.StatusActive)

		if jabatan := c.Query("jabatan"); jabatan != "" {
			tx = tx.Where("jabatan LIKE ?", "%"+jabatan+"%")
		}

		var users []models.User
		if err := tx.Order("name ASC").Find(&users).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   users,
		})
	}
}
