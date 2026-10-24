package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllCategory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var categories []models.Category

		if err := middleware.TenantDB(c, db).Model(&models.Category{}).Find(&categories).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Data berhasil diambil",
			"data":    categories,
		})
	}
}

func GetCategoryById(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var category models.Category
		id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
		if err != nil || id == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "ID kategori tidak valid",
			})
			return
		}

		if err := middleware.TenantDB(c, db).Model(&category).First(&category, id).Error; err != nil {
			status, message := http.StatusInternalServerError, "Ada yang salah"
			if errors.Is(err, gorm.ErrRecordNotFound) {
				status, message = http.StatusNotFound, "Data tidak ditemukan"
			}
			c.AbortWithStatusJSON(status, gin.H{
				"status":  "error",
				"message": message,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Data berhasil diambil",
			"data":    category,
		})
	}
}

func CreateCategory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input request.CategoryInput

		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "invalid request",
			})
			return
		}

		if err := validate.Struct(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "invalid request",
			})
			return
		}

		// ponytail: cek duplikat saat create; unique index (company_id, name) menutup race dan duplikat lewat update.
		var existingCategory models.Category
		err := middleware.TenantDB(c, db).Model(&existingCategory).
			Where("name = ?", input.Name).First(&existingCategory).Error
		if err == nil {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"status":  "error",
				"message": fmt.Sprintf("Sudah ada kategori dengan nama %s", input.Name),
			})
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		category := models.Category{
			Name:        input.Name,
			Description: input.Description,
			CompanyID:   middleware.Claims(c).CompanyID,
		}

		if err := db.Create(&category).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Kategori berhasil dibuat",
			"data":    category,
		})
	}
}

func UpdateCategory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
		if err != nil || id == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "ID kategori tidak valid",
			})
			return
		}

		var input request.CategoryInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "invalid request",
			})
			return
		}

		if err := validate.Struct(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "invalid request",
			})
			return
		}

		var category models.Category
		if err := middleware.TenantDB(c, db).Model(&category).First(&category, id).Error; err != nil {
			status, message := http.StatusInternalServerError, "Ada yang salah"
			if errors.Is(err, gorm.ErrRecordNotFound) {
				status, message = http.StatusNotFound, "Data tidak ditemukan"
			}
			c.AbortWithStatusJSON(status, gin.H{
				"status":  "error",
				"message": message,
			})
			return
		}

		category.Name = input.Name
		category.Description = input.Description
		result := middleware.TenantDB(c, db).Model(&category).
			Select("Name", "Description").Updates(&category)
		if result.Error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}
		if result.RowsAffected == 0 {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Data tidak ditemukan",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Kategori berhasil diperbarui",
			"data":    category,
		})
	}
}

func DeleteCategory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
		if err != nil || id == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "ID kategori tidak valid",
			})
			return
		}

		var category models.Category
		if err := middleware.TenantDB(c, db).Model(&category).First(&category, id).Error; err != nil {
			status, message := http.StatusInternalServerError, "Ada yang salah"
			if errors.Is(err, gorm.ErrRecordNotFound) {
				status, message = http.StatusNotFound, "Data tidak ditemukan"
			}
			c.AbortWithStatusJSON(status, gin.H{
				"status":  "error",
				"message": message,
			})
			return
		}

		result := middleware.TenantDB(c, db).Model(&category).Delete(&category)
		if result.Error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}
		if result.RowsAffected == 0 {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Data tidak ditemukan",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Kategori berhasil dihapus",
		})
	}
}
