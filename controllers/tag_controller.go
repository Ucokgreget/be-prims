package controllers

import (
	"net/http"
	"strings"

	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetTags: daftar tag master (global, lintas company).
func GetTags(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tags []models.Tag
		if err := db.Order("name ASC").Find(&tags).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   tags,
		})
	}
}

type CreateTagRequest struct {
	Name        string `form:"name" json:"name" validate:"required"`
	Description string `form:"description" json:"description"`
}

// CreateTag: buat tag baru; kalau nama sudah ada, kembalikan yang existing
// (upsert-by-name) supaya frontend aman submit dua kali.
func CreateTag(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input CreateTagRequest
		if err := c.ShouldBind(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}
		if err := validate.Struct(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}

		name := strings.TrimSpace(input.Name)

		var tag models.Tag
		if err := db.Where("LOWER(name) = LOWER(?)", name).First(&tag).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Ada yang salah",
				})
				return
			}
			// belum ada → buat baru
			tag = models.Tag{Name: name, Description: input.Description}
			if err := db.Create(&tag).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Ada yang salah",
				})
				return
			}
		}

		c.JSON(http.StatusCreated, gin.H{
			"status":  "success",
			"message": "Tag berhasil disimpan",
			"data":    tag,
		})
	}
}
