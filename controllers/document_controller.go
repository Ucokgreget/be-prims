package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/storage"
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func GetAllDocumentByCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var document []models.Document

		if err := db.
			Preload("Attachment").
			Preload("Category").
			Preload("Tags", func(tx *gorm.DB) *gorm.DB { return tx.Preload("Tag") }).
			Preload("Departments", func(tx *gorm.DB) *gorm.DB { return tx.Preload("Department") }).
			Where("company_id = ?", claims.CompanyID).
			Find(&document).Error; err != nil {
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

func GetDocumentByIdByCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)
		id := c.Param("id")

		var document models.Document

		if err := db.
			Preload("Attachment").
			Preload("Category").
			Preload("Tags", func(tx *gorm.DB) *gorm.DB { return tx.Preload("Tag") }).
			Preload("Departments", func(tx *gorm.DB) *gorm.DB { return tx.Preload("Department") }).
			Where("id = ? AND company_id = ?", id, claims.CompanyID).
			Find(&document).Error; err != nil {
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

var (
	errCompanyQuotaExceeded = errors.New("company storage quota exceeded")
	errCompanyUploadSession = errors.New("company upload session revoked")
)

func checkUploadQuota(tx *gorm.DB, claims *middleware.AuthClaims, size int64) error {
	// Serialize quota checks and attachment inserts for this company only.
	var company models.Company
	if err := tx.Raw(`SELECT status, auth_version, storage_quota_gb
		FROM companies WITH (UPDLOCK, HOLDLOCK) WHERE id = ?`, claims.CompanyID).Take(&company).Error; err != nil {
		return err
	}
	if company.Status != models.StatusActive || company.AuthVersion != claims.CompanyAuthVersion {
		return errCompanyUploadSession
	}
	var used int64
	if err := companyStorageBytes(tx).Where("d.company_id = ?", claims.CompanyID).Find(&used).Error; err != nil {
		return err
	}
	if size > int64(company.StorageQuotaGB)*bytesPerGB-used {
		return errCompanyQuotaExceeded
	}
	return nil
}

func CreateDocument(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		input, ok := bindDocumentUpload(c)
		if form := c.Request.MultipartForm; form != nil {
			defer form.RemoveAll()
		}
		if !ok {
			return
		}

		if len(input.SharedTo) > 0 {
			var count int64
			if err := db.Model(&models.User{}).
				Where("id IN ? AND company_id = ?", input.SharedTo, claims.CompanyID).
				Count(&count).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Gagal memeriksa pengguna",
				})
				return
			}
			if count != int64(len(input.SharedTo)) {
				documentUploadError(c, http.StatusBadRequest, "invalid_fields", "Penerima akses tidak valid.",
					gin.H{"shared_to": "Gunakan ID pengguna yang berbeda dan terdaftar di perusahaan Anda."})
				return
			}

		}

		now := time.Now()

		document := models.Document{
			UUID:            uuid.New().String(),
			Title:           input.Title,
			Description:     input.Description,
			DocumentNumber:  input.DocumentNumber,
			PublicationDate: input.PublicationDate,
			IsPrivate:       input.IsPrivate,
			AllowDownload:   input.AllowDownload,
			CategoryID:      input.CategoryID,
			CompanyID:       claims.CompanyID,
			CreatedBy:       claims.UserID,
			CreatedAt:       now,
		}

		objectKey := storage.TenantPrefix(claims.CompanyID) + document.UUID + ".pdf"
		var uploadErr error
		ctx := c.Request.Context()
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := checkUploadQuota(tx, claims, input.Attachments.Size); err != nil {
				return err
			}
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

			uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			uploadErr = storage.Upload(uploadCtx, objectKey, input.Attachments)
			return uploadErr
		})
		if err != nil {
			switch {
			case errors.Is(err, errCompanyQuotaExceeded):
				documentUploadError(c, http.StatusConflict, "storage_quota_exceeded", "Kuota penyimpanan perusahaan tidak mencukupi.", nil)
			case errors.Is(err, errCompanyUploadSession):
				documentUploadError(c, http.StatusUnauthorized, "session_revoked", "Sesi tidak berlaku. Silakan login kembali.", nil)
			case uploadErr != nil:
				// The transaction did not attempt COMMIT. An ambiguous PUT can be cleaned up safely.
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				storage.Delete(cleanupCtx, []string{objectKey})
				documentUploadError(c, http.StatusInternalServerError, "upload_failed", "Gagal mengunggah file", nil)
			default:
				// Do not delete an uploaded object if the outcome of COMMIT is unknown.
				log.Printf("document: gagal menyimpan %s: %v", document.UUID, err)
				documentUploadError(c, http.StatusInternalServerError, "save_failed", "Gagal menyimpan dokumen", nil)
			}
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"status":  "success",
			"message": "Data dokumen berhasil dibuat",
			"data":    document,
		})
	}
}
