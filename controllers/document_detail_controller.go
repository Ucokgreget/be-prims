package controllers

import (
	"errors"
	"net/http"
	"time"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- GET /api/v1/portal/documents/:uuid ----

// DTO detail dokumen. Dibentuk eksplisit supaya kontrak response = spec BE-07:
// tanpa company_id/created_by/file_path, dan tanpa relasi yang tidak di-Preload.
type documentDetail struct {
	Id              uint               `json:"id"`
	UUID            string             `json:"uuid"`
	DocumentNumber  string             `json:"document_number"`
	Title           string             `json:"title"`
	Description     string             `json:"description"`
	PublicationDate time.Time          `json:"publication_date"`
	IsPrivate       bool               `json:"is_private"`
	AllowDownload   bool               `json:"allow_download"`
	Category        detailCategory     `json:"category"`
	Attachment      detailAttachment   `json:"attachment"`
	Departments     []detailDepartment `json:"departments"`
	Tags            []detailTag        `json:"tags"`
	CreatedAt       time.Time          `json:"created_at"`
}

type detailDepartment struct {
	Id           uint                 `json:"id"`
	DepartmentID uint                 `json:"department_id"`
	Department   detailDepartmentInfo `json:"department"`
}

type detailDepartmentInfo struct {
	Id   uint   `json:"id"`
	Name string `json:"name"`
}

type detailTag struct {
	Id    uint          `json:"id"`
	TagID uint          `json:"tag_id"`
	Tag   detailTagInfo `json:"tag"`
}

type detailTagInfo struct {
	Id   uint   `json:"id"`
	Name string `json:"name"`
}

type detailCategory struct {
	Id    uint   `json:"id"`
	Name  string `json:"name"`
	Level *int   `json:"level"`
}

type detailAttachment struct {
	Id        uint      `json:"id"`
	FileName  string    `json:"file_name"`
	FileSize  int64     `json:"file_size"`
	CreatedAt time.Time `json:"created_at"`
}

// GetPortalDocumentDetail: detail dokumen untuk portal (ADMIN_ANPER + KARYAWAN).
// Dua lapis guard: company_id dari JWT (404 kalau beda tenant) lalu izin dokumen
// private (403 kalau karyawan tidak terdaftar di document_shared).
func GetPortalDocumentDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var document models.Document
		err := db.
			Preload("Attachment").
			Preload("Category").
			Preload("Departments.Department").
			Preload("Tags.Tag").
			Where("uuid = ? AND company_id = ?", c.Param("uuid"), claims.CompanyID).
			Take(&document).Error

		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Dokumen tidak ditemukan",
			})
			return
		}
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		if document.IsPrivate && claims.Role != models.RoleAdminAnper {
			allowed, err := isDocumentSharedWith(db, document.Id, claims.UserID)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Ada yang salah",
				})
				return
			}
			if !allowed {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"status":  "error",
					"message": "Akses ditolak. Anda tidak memiliki izin untuk melihat dokumen rahasia ini.",
				})
				return
			}
		}

		departments := make([]detailDepartment, len(document.Departments))
		for i, d := range document.Departments {
			departments[i] = detailDepartment{
				Id:           d.Id,
				DepartmentID: d.DepartmentID,
				Department: detailDepartmentInfo{
					Id:   d.Department.Id,
					Name: d.Department.Name,
				},
			}
		}

		tags := make([]detailTag, len(document.Tags))
		for i, t := range document.Tags {
			tags[i] = detailTag{
				Id:    t.Id,
				TagID: t.TagID,
				Tag: detailTagInfo{
					Id:   t.Tag.Id,
					Name: t.Tag.Name,
				},
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Detail dokumen berhasil dimuat",
			"data": documentDetail{
				Id:              document.Id,
				UUID:            document.UUID,
				DocumentNumber:  document.DocumentNumber,
				Title:           document.Title,
				Description:     document.Description,
				PublicationDate: document.PublicationDate,
				IsPrivate:       document.IsPrivate,
				AllowDownload:   document.AllowDownload,
				Category: detailCategory{
					Id:    document.Category.Id,
					Name:  document.Category.Name,
					Level: document.Category.Level,
				},
				Attachment: detailAttachment{
					Id:        document.Attachment.Id,
					FileName:  document.Attachment.FileName,
					FileSize:  document.Attachment.FileSize,
					CreatedAt: document.Attachment.CreatedAt,
				},
				Departments: departments,
				Tags:        tags,
				CreatedAt:   document.CreatedAt,
			},
		})
	}
}

// ponytail: satu query COUNT; kalau nanti butuh daftar penerima share, ganti jadi
// query yang mengembalikan baris document_shared, bukan menambah tabel cache.
func isDocumentSharedWith(db *gorm.DB, documentID, userID uint) (bool, error) {
	var count int64
	err := db.Model(&models.DocumentShared{}).
		Where("document_id = ? AND user_id = ?", documentID, userID).
		Count(&count).Error
	return count > 0, err
}
