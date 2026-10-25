package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllDocumentByCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var document []models.Document

		claims := middleware.Claims(c)

		if err := db.Where("company_id = ?", claims.CompanyID).Find(&document).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   document,
		})

	}
}

func CreateDocumentByCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var input request.CreateDocumentRequest

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

		document := models.Document{
			Title:           input.Title,
			Description:     input.Description,
			PublicationDate: input.PublicationDate,
			IsPrivate:       input.IsPrivate,
			CategoryID:      input.CategoryID,
			CompanyID:       claims.CompanyID,
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&document).Error; err != nil {
				return err
			}
			depts := make([]models.DocumentDepartment, len(input.DepartmentID))
			for i, id := range input.DepartmentID {
				depts[i] = models.DocumentDepartment{DocumentID: document.Id, DepartmentID: id}
			}
			tags := make([]models.DocumentTag, len(input.TagID))
			for i, id := range input.TagID {
				tags[i] = models.DocumentTag{DocumentID: document.Id, TagID: id}
			}
			if len(depts) > 0 {
				if err := tx.Create(&depts).Error; err != nil {
					return err
				}
			}
			if len(tags) > 0 {
				return tx.Create(&tags).Error
			}
			return nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal menyimpan dokumen",
			})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"status": "success",
			"data":   document,
		})
	}
}
