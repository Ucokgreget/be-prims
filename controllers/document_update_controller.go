package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/storage"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gorm.io/gorm"
)

type documentSharedItem struct {
	ID       uint      `json:"id"`
	UserID   uint      `json:"user_id"`
	SharedBy uint      `json:"shared_by"`
	SharedAt time.Time `json:"shared_at"`
}

type documentDetailResponse struct {
	models.Document
	SharedTo       []uint               `json:"shared_to"`
	DocumentShared []documentSharedItem `json:"document_shared"`
}

func buildDocumentDetailResponse(db *gorm.DB, doc models.Document) (documentDetailResponse, error) {
	var rows []models.DocumentShared
	if err := db.Where("document_id = ?", doc.Id).Find(&rows).Error; err != nil {
		return documentDetailResponse{}, err
	}
	sharedIDs := make([]uint, 0, len(rows))
	sharedItems := make([]documentSharedItem, 0, len(rows))
	for _, row := range rows {
		sharedIDs = append(sharedIDs, row.UserID)
		sharedItems = append(sharedItems, documentSharedItem{
			ID:       row.Id,
			UserID:   row.UserID,
			SharedBy: row.SharedBy,
			SharedAt: row.SharedAt,
		})
	}
	return documentDetailResponse{
		Document:       doc,
		SharedTo:       sharedIDs,
		DocumentShared: sharedItems,
	}, nil
}

type updateDocumentJSONInput struct {
	Title           *string `json:"title"`
	DocumentNumber  *string `json:"document_number"`
	Description     *string `json:"description"`
	CategoryID      *uint   `json:"category_id"`
	PublicationDate *string `json:"publication_date"`
	IsPrivate       *bool   `json:"is_private"`
	AllowDownload   *bool   `json:"allow_download"`
	DepartmentID    *[]uint `json:"department_id"`
	SharedTo        *[]uint `json:"shared_to"`
}

func dedupePositiveUints(vals []uint) []uint {
	seen := make(map[uint]struct{}, len(vals))
	out := make([]uint, 0, len(vals))
	for _, v := range vals {
		if v == 0 {
			continue
		}
		if _, exists := seen[v]; !exists {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func parseUintSlice(raw []string) []uint {
	out := make([]uint, 0, len(raw))
	for _, s := range raw {
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err == nil && n > 0 {
			out = append(out, uint(n))
		}
	}
	return dedupePositiveUints(out)
}

func UpdateDocument(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)
		docID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || docID == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "ID dokumen tidak valid.",
			})
			return
		}

		var doc models.Document
		if err := db.Preload("Attachment").
			Where("id = ? AND company_id = ?", docID, claims.CompanyID).
			First(&doc).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
					"status":  "error",
					"message": "Dokumen tidak ditemukan.",
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal memuat dokumen.",
			})
			return
		}

		var input updateDocumentJSONInput
		var replacementFile *multipart.FileHeader

		mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if mediaType == binding.MIMEMultipartPOSTForm {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDocumentBodyBytes)
			if err := c.Request.ParseMultipartForm(2 << 20); err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"status":  "error",
					"message": "Data multipart tidak valid atau terlalu besar.",
				})
				return
			}
			if c.Request.MultipartForm != nil {
				defer c.Request.MultipartForm.RemoveAll()
				vals := c.Request.MultipartForm.Value
				if v, ok := vals["title"]; ok && len(v) > 0 {
					s := strings.TrimSpace(v[0])
					input.Title = &s
				}
				if v, ok := vals["document_number"]; ok && len(v) > 0 {
					s := strings.TrimSpace(v[0])
					input.DocumentNumber = &s
				}
				if v, ok := vals["description"]; ok && len(v) > 0 {
					s := strings.TrimSpace(v[0])
					input.Description = &s
				}
				if v, ok := vals["category_id"]; ok && len(v) > 0 {
					if cid, err := strconv.ParseUint(strings.TrimSpace(v[0]), 10, 64); err == nil && cid > 0 {
						u := uint(cid)
						input.CategoryID = &u
					}
				}
				if v, ok := vals["publication_date"]; ok && len(v) > 0 {
					s := strings.TrimSpace(v[0])
					input.PublicationDate = &s
				}
				if v, ok := vals["is_private"]; ok && len(v) > 0 {
					b := strings.EqualFold(strings.TrimSpace(v[0]), "true")
					input.IsPrivate = &b
				}
				if v, ok := vals["allow_download"]; ok && len(v) > 0 {
					b := strings.EqualFold(strings.TrimSpace(v[0]), "true")
					input.AllowDownload = &b
				}
				if v, ok := vals["department_id"]; ok {
					ids := parseUintSlice(v)
					input.DepartmentID = &ids
				}
				if v, ok := vals["shared_to"]; ok {
					ids := parseUintSlice(v)
					input.SharedTo = &ids
				}
				if files := c.Request.MultipartForm.File["attachment"]; len(files) > 0 {
					replacementFile = files[0]
				}
			}
		} else {
			if err := c.ShouldBindJSON(&input); err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"status":  "error",
					"message": "Format data pembaruan dokumen tidak valid.",
				})
				return
			}
		}

		if replacementFile != nil {
			if replacementFile.Size > maxDocumentPDFBytes {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
					"status":  "error",
					"message": "Ukuran PDF melebihi batas maksimal 25 MiB.",
				})
				return
			}
			if replacementFile.Size == 0 || !strings.EqualFold(filepath.Ext(replacementFile.Filename), ".pdf") {
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
					"status":  "error",
					"message": "Berkas harus berupa PDF yang valid.",
				})
				return
			}
			f, err := replacementFile.Open()
			if err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Berkas unggahan tidak dapat dibaca.",
				})
				return
			}
			var header [5]byte
			_, readErr := io.ReadFull(f, header[:])
			_ = f.Close()
			if readErr != nil || string(header[:]) != "%PDF-" {
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
					"status":  "error",
					"message": "Isi berkas tidak memiliki signature PDF.",
				})
				return
			}
		}

		nextPrivate := doc.IsPrivate
		if input.IsPrivate != nil {
			nextPrivate = *input.IsPrivate
		}

		var cleanedShared []uint
		shouldUpdateShared := !nextPrivate || input.SharedTo != nil
		if nextPrivate && input.SharedTo != nil {
			cleanedShared = dedupePositiveUints(*input.SharedTo)
			if len(cleanedShared) > 0 {
				var count int64
				if err := db.Model(&models.User{}).
					Where("id IN ? AND company_id = ?", cleanedShared, claims.CompanyID).
					Count(&count).Error; err != nil {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
						"status":  "error",
						"message": "Gagal memverifikasi penerima akses.",
					})
					return
				}
				if count != int64(len(cleanedShared)) {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
						"status":  "error",
						"message": "Sebagian penerima akses tidak terdaftar di perusahaan Anda.",
					})
					return
				}
			}
		}

		updates := map[string]any{}
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			updates["title"] = strings.TrimSpace(*input.Title)
		}
		if input.DocumentNumber != nil && strings.TrimSpace(*input.DocumentNumber) != "" {
			updates["document_number"] = strings.TrimSpace(*input.DocumentNumber)
		}
		if input.Description != nil {
			updates["description"] = strings.TrimSpace(*input.Description)
		}
		if input.CategoryID != nil && *input.CategoryID > 0 {
			updates["category_id"] = *input.CategoryID
		}
		if input.PublicationDate != nil && strings.TrimSpace(*input.PublicationDate) != "" {
			rawDate := strings.TrimSpace(*input.PublicationDate)
			if idx := strings.IndexByte(rawDate, 'T'); idx > 0 {
				rawDate = rawDate[:idx]
			}
			parsedDate, err := time.Parse("2006-01-02", rawDate)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"status":  "error",
					"message": "Format tanggal terbit tidak valid (gunakan YYYY-MM-DD).",
				})
				return
			}
			updates["publication_date"] = parsedDate
		}
		if input.IsPrivate != nil {
			updates["is_private"] = *input.IsPrivate
		}
		if input.AllowDownload != nil {
			updates["allow_download"] = *input.AllowDownload
		}
		now := time.Now()
		updatedBy := claims.UserID
		updates["updated_by"] = updatedBy
		updates["updated_at"] = now

		ctx := c.Request.Context()
		var newObjectKey string
		oldObjectKey := storage.ObjectKeyFromFileUrl(doc.Attachment.FilePath)
		if replacementFile != nil {
			newObjectKey = storage.TenantPrefix(claims.CompanyID) + doc.UUID + "_" + strconv.FormatInt(now.Unix(), 10) + ".pdf"
		}

		var uploadErr error
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if replacementFile != nil {
				deltaSize := replacementFile.Size - doc.Attachment.FileSize
				if deltaSize > 0 {
					if err := checkUploadQuota(tx, claims, deltaSize); err != nil {
						return err
					}
				}
			}

			if err := tx.Model(&models.Document{}).Where("id = ? AND company_id = ?", doc.Id, claims.CompanyID).Updates(updates).Error; err != nil {
				return err
			}

			if input.DepartmentID != nil {
				deptIDs := dedupePositiveUints(*input.DepartmentID)
				if len(deptIDs) > 0 {
					if err := tx.Where("document_id = ?", doc.Id).Delete(&models.DocumentDepartment{}).Error; err != nil {
						return err
					}
					rows := make([]models.DocumentDepartment, len(deptIDs))
					for i, did := range deptIDs {
						rows[i] = models.DocumentDepartment{DocumentID: doc.Id, DepartmentID: did}
					}
					if err := tx.Create(&rows).Error; err != nil {
						return err
					}
				}
			}

			if shouldUpdateShared {
				if err := tx.Where("document_id = ?", doc.Id).Delete(&models.DocumentShared{}).Error; err != nil {
					return err
				}
				if nextPrivate && len(cleanedShared) > 0 {
					sharedRows := make([]models.DocumentShared, len(cleanedShared))
					for i, uid := range cleanedShared {
						sharedRows[i] = models.DocumentShared{
							DocumentID: doc.Id,
							UserID:     uid,
							SharedBy:   claims.UserID,
							SharedAt:   now,
						}
					}
					if err := tx.Create(&sharedRows).Error; err != nil {
						return err
					}
				}
			}

			if replacementFile != nil {
				if err := storage.Upload(ctx, newObjectKey, replacementFile); err != nil {
					uploadErr = err
					return err
				}
				if doc.Attachment.Id > 0 {
					if err := tx.Model(&models.Attachment{}).Where("id = ?", doc.Attachment.Id).Updates(map[string]any{
						"file_name": replacementFile.Filename,
						"file_path": newObjectKey,
						"file_size": replacementFile.Size,
					}).Error; err != nil {
						return err
					}
				} else {
					att := models.Attachment{
						DocumentID: doc.Id,
						FileName:   replacementFile.Filename,
						FilePath:   newObjectKey,
						FileSize:   replacementFile.Size,
					}
					if err := tx.Create(&att).Error; err != nil {
						return err
					}
				}
			}

			return nil
		})
		if err != nil {
			if replacementFile != nil && uploadErr == nil && newObjectKey != "" {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				storage.Delete(cleanupCtx, []string{newObjectKey})
				cancel()
			}
			switch {
			case errors.Is(err, errCompanyQuotaExceeded):
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"status": "error", "message": "Kuota penyimpanan perusahaan telah penuh."})
			case errors.Is(err, errCompanyUploadSession):
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"status": "error", "message": middleware.InactiveCompanyMessage})
			default:
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal memperbarui dokumen."})
			}
			return
		}

		if replacementFile != nil && oldObjectKey != "" && oldObjectKey != newObjectKey {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			storage.Delete(cleanupCtx, []string{oldObjectKey})
			cancel()
		}

		var refreshed models.Document
		_ = db.Preload("Attachment").
			Preload("Category").
			Preload("Tags", func(tx *gorm.DB) *gorm.DB { return tx.Preload("Tag") }).
			Preload("Departments", func(tx *gorm.DB) *gorm.DB { return tx.Preload("Department") }).
			Where("id = ? AND company_id = ?", doc.Id, claims.CompanyID).
			First(&refreshed).Error

		detailResp, respErr := buildDocumentDetailResponse(db, refreshed)
		if respErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"status":  "success",
				"message": "Dokumen berhasil diperbarui.",
				"data":    refreshed,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Dokumen berhasil diperbarui.",
			"data":    detailResp,
		})
	}
}
