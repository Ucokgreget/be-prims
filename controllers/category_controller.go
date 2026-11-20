package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"encoding/json"
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

		query, ok := filterCategoryLevel(c, middleware.TenantDB(c, db).Model(&models.Category{}))
		if !ok {
			return
		}
		if err := query.Find(&categories).Error; err != nil {
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
		var input request.CreateCategoryRequest

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
			Level:       input.Level,
			Status:      models.StatusActive,
			CompanyID:   middleware.Claims(c).CompanyID,
		}
		if input.Status != nil {
			category.Status = *input.Status
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

		var input request.UpdateCategoryRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "invalid request",
			})
			return
		}

		if err := validate.Struct(&input); err != nil ||
			(input.Name == nil && input.Description == nil && input.Level == nil && input.Status == nil) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "invalid request",
			})
			return
		}
		var level *int
		if input.Level != nil && (json.Unmarshal(input.Level, &level) != nil || (level != nil && (*level < 1 || *level > 5))) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Level harus 1–5 atau null (SK Direksi)"})
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

		var fields []string
		if input.Name != nil {
			category.Name = *input.Name
			fields = append(fields, "Name")
		}
		if input.Description != nil {
			category.Description = input.Description
			fields = append(fields, "Description")
		}
		if input.Level != nil {
			category.Level = level
			fields = append(fields, "Level")
		}
		if input.Status != nil {
			category.Status = *input.Status
			fields = append(fields, "Status")
		}
		result := middleware.TenantDB(c, db).Model(&category).
			Select(fields).Updates(&category)
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

		category.Status = models.StatusInactive
		result := middleware.TenantDB(c, db).Model(&category).Select("Status").Updates(&category)
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
			"message": "Kategori berhasil dinonaktifkan",
		})
	}
}

func filterCategoryLevel(c *gin.Context, query *gorm.DB) (*gorm.DB, bool) {
	values := c.QueryArray("level")
	if len(values) == 0 {
		return query, true
	}
	if len(values) == 1 {
		if values[0] == "sk_direksi" {
			return query.Where("categories.level IS NULL"), true
		}
		if level, err := strconv.Atoi(values[0]); err == nil && level >= 1 && level <= 5 {
			return query.Where("categories.level = ?", level), true
		}
	}
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Filter level harus 1–5 atau sk_direksi"})
	return nil, false
}
