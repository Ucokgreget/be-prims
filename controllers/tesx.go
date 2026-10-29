package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"be-prims/storage"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func CreateDocumentx(db *gorm.DB) gin.HandlerFunc {
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

		// Gin mem-parse field form kosong jadi [0]; anggap sebagai tidak diisi
		if len(input.SharedTo) == 1 && input.SharedTo[0] == 0 {
			input.SharedTo = nil
		}

		if err := validate.Struct(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}

		now := time.Now()

		// shared_to hanya berlaku untuk dokumen private
		if !input.IsPrivate {
			input.SharedTo = nil
		}

		// validasi attachment: hanya PDF, maks 25 MB
		if ext := strings.ToLower(filepath.Ext(input.Attachments.Filename)); ext != ".pdf" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Hanya file PDF yang diizinkan",
			})
			return
		}
		if input.Attachments.Size > 25<<20 {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"status":  "error",
				"message": "Ukuran file melebihi 25 MB",
			})
			return
		}

		if len(input.SharedTo) > 0 {
			var count int64
			if err := db.Model(&models.User{}).
				Where("id IN ? AND company_id = ?", input.SharedTo, claims.CompanyID).
				Count(&count).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Ada yang salah",
				})
				return
			}
			if count != int64(len(input.SharedTo)) {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"status":  "error",
					"message": "Ada user yang tidak ditemukan di company Anda",
				})
				return
			}
		}

		document := models.Document{
			UUID:            uuid.New().String(),
			Title:           input.Title,
			Description:     input.Description,
			DocumentNumber:  input.DocumentNumber,
			PublicationDate: input.PublicationDate,
			IsPrivate:       input.IsPrivate,
			CategoryID:      input.CategoryID,
			CompanyID:       claims.CompanyID,
			CreatedBy:       claims.UserID,
		}

		// Pattern: upload file SEBELUM transaksi DB (Minio tidak punya rollback).
		// Kalau transaksi gagal setelah upload, file dibuang di compensating action.
		ext := strings.ToLower(filepath.Ext(input.Attachments.Filename))
		objectKey := fmt.Sprintf("documents/%s_%d%s", document.UUID, now.Unix(), ext)

		if err := storage.Upload(c.Request.Context(), objectKey, input.Attachments); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal menyimpan file",
			})
			return
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
				if err := tx.Create(&tags).Error; err != nil {
					return err
				}
			}

			attachment := models.Attachment{
				DocumentID: document.Id,
				FileName:   input.Attachments.Filename,
				FilePath:   "/uploads/" + objectKey,
				FileSize:   input.Attachments.Size,
				CreatedAt:  now,
			}

			if err := tx.Create(&attachment).Error; err != nil {
				return err
			}

			if len(input.SharedTo) > 0 {
				sharedTo := make([]models.DocumentShared, len(input.SharedTo))
				for i, id := range input.SharedTo {
					sharedTo[i] = models.DocumentShared{DocumentID: document.Id, UserID: id, SharedBy: claims.UserID}
				}
				if err := tx.Create(&sharedTo).Error; err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			// compensating action: DB rollback, buang file yang sudah ter-upload
			storage.Delete(c.Request.Context(), []string{objectKey})
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal menyimpan dokumen",
			})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"status":  "success",
			"message": "Document berhasil dibuat",
			"data":    document,
		})

	}
}
