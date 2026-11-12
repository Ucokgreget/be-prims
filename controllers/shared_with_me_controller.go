package controllers

import (
	"net/http"
	"strconv"
	"time"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- GET /api/v1/portal/documents/shared-with-me ----

// Sub-objek response memakai pointer supaya hasil LEFT JOIN yang NULL tampil
// sebagai null, bukan objek kosong {id:0,name:""} yang menyesatkan klien.
type sharedWithMeCategory struct {
	Id   uint   `json:"id"`
	Name string `json:"name"`
}

type sharedWithMeUser struct {
	Id   uint   `json:"id"`
	Name string `json:"name"`
}

type sharedWithMeItem struct {
	Id              uint                  `json:"id"`
	UUID            string                `json:"uuid"`
	DocumentNumber  string                `json:"document_number"`
	Title           string                `json:"title"`
	PublicationDate *string               `json:"publication_date"`
	AllowDownload   bool                  `json:"allow_download"`
	Category        *sharedWithMeCategory `json:"category"`
	SharedBy        *sharedWithMeUser     `json:"shared_by"`
	SharedAt        *time.Time            `json:"shared_at"`
}

type sharedWithMeMeta struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// sharedWithMeRow: hasil JOIN flat, dipetakan ke sharedWithMeItem setelah scan.
// publication_date dikonversi di SQL (CONVERT style 23) agar keluar "YYYY-MM-DD"
// sesuai kontrak; kolom aslinya datetimeoffset.
type sharedWithMeRow struct {
	Id              uint
	UUID            string
	DocumentNumber  string
	Title           string
	PublicationDate *string
	AllowDownload   bool
	CategoryID      *uint
	CategoryName    *string
	SharedByID      *uint
	SharedByName    *string
	SharedAt        *time.Time
}

// GetSharedWithMeDocuments: daftar dokumen rahasia yang dibagikan langsung
// (Direct Share) kepada user yang sedang login.
//
// Dua filter wajib dan tidak boleh dilonggarkan:
//   - document_shareds.user_id = claims.UserID  → user tidak melihat share orang lain,
//     termasuk bila ia ADMIN_ANPER (endpoint ini personal, bukan panel admin).
//   - documents.company_id = claims.CompanyID   → isolasi tenant antar anak perusahaan.
func GetSharedWithMeDocuments(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if page <= 0 {
			page = 1
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
		if limit > 100 {
			limit = 100
		}
		if limit <= 0 {
			limit = 10
		}

		base := db.Model(&models.DocumentShared{}).
			Joins("INNER JOIN documents ON documents.id = document_shareds.document_id").
			Joins("LEFT JOIN categories ON categories.id = documents.category_id").
			Joins("LEFT JOIN users AS shared_users ON shared_users.id = document_shareds.shared_by").
			Where("document_shareds.user_id = ? AND documents.company_id = ?", claims.UserID, claims.CompanyID)

		// q: judul ATAU nomor dokumen
		if q := c.Query("q"); q != "" {
			like := "%" + q + "%"
			base = base.Where("documents.title LIKE ? OR documents.document_number LIKE ?", like, like)
		}

		// category_id
		if catID := c.Query("category_id"); catID != "" {
			base = base.Where("documents.category_id = ?", catID)
		}

		// total sebelum pagination (untuk meta). Count() mengembalikan clause SELECT/ORDER BY.
		var total int64
		if err := base.Count(&total).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		var rows []sharedWithMeRow
		if err := base.
			Select("documents.id, documents.uuid, documents.document_number, documents.title, " +
				"CONVERT(varchar(10), documents.publication_date, 23) AS publication_date, " +
				"documents.allow_download, " +
				"categories.id AS category_id, categories.name AS category_name, " +
				"shared_users.id AS shared_by_id, shared_users.name AS shared_by_name, " +
				"document_shareds.shared_at").
			Order("document_shareds.shared_at DESC").
			Offset((page - 1) * limit).Limit(limit).
			Find(&rows).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		// make(...) bukan nil: kontrak mengharuskan data: [] saat kosong, bukan null.
		items := make([]sharedWithMeItem, 0, len(rows))
		for _, r := range rows {
			item := sharedWithMeItem{
				Id:              r.Id,
				UUID:            r.UUID,
				DocumentNumber:  r.DocumentNumber,
				Title:           r.Title,
				PublicationDate: r.PublicationDate,
				AllowDownload:   r.AllowDownload,
				SharedAt:        r.SharedAt,
			}
			if r.CategoryID != nil && r.CategoryName != nil {
				item.Category = &sharedWithMeCategory{Id: *r.CategoryID, Name: *r.CategoryName}
			}
			if r.SharedByID != nil && r.SharedByName != nil {
				item.SharedBy = &sharedWithMeUser{Id: *r.SharedByID, Name: *r.SharedByName}
			}
			items = append(items, item)
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Daftar dokumen Shared With Me berhasil diambil",
			"data":    items,
			"meta":    sharedWithMeMeta{Page: page, Limit: limit, Total: total},
		})
	}
}
