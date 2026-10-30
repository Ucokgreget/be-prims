package controllers

import (
	"net/http"
	"strconv"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DocumentListItem adalah row flat hasil JOIN — bentuknya persis kolom
// yang diminta mockup Admin (Hal. 6), bukan dump model Document.
type DocumentListItem struct {
	Id              uint   `json:"id"`
	UUID            string `json:"uuid"`
	Title           string `json:"title"`
	DocumentNumber  string `json:"document_number"`
	CategoryName    string `json:"category_name"`
	PublicationDate string `json:"publication_date"`
	IsPrivate       bool   `json:"is_private"`
	Visitors        int64  `json:"visitors"`
	CreatedAt       string `json:"created_at"`
	UploaderName    string `json:"uploader_name"`
}

type DocumentListMeta struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// GetAllDocuments: list dokumen admin — semua (public+private) milik company,
// dengan filter q, category_id, mode, dan pagination.
func GetAllDocuments(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		// pagination: page & limit dari query param, default 1/10, maks 100
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
		offset := (page - 1) * limit

		// filter dasar: multi-tenant dari JWT
		base := db.Model(&models.Document{}).
			Where("documents.company_id = ?", claims.CompanyID)

		// q: search judul / nomor dokumen
		if q := c.Query("q"); q != "" {
			like := "%" + q + "%"
			base = base.Where("documents.title LIKE ? OR documents.document_number LIKE ?", like, like)
		}

		// category_id
		if catID := c.Query("category_id"); catID != "" {
			base = base.Where("documents.category_id = ?", catID)
		}

		// mode: public/private
		switch c.Query("mode") {
		case "public":
			base = base.Where("documents.is_private = ?", false)
		case "private":
			base = base.Where("documents.is_private = ?", true)
		}

		// total sebelum pagination (untuk meta)
		var total int64
		if err := base.Count(&total).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		var items []DocumentListItem
		if err := base.
			Select("documents.id, documents.uuid, documents.title, documents.document_number, " +
				"categories.name AS category_name, documents.publication_date, documents.is_private, " +
				"COUNT(DISTINCT document_access_logs.user_id) AS visitors, " +
				"documents.created_at, users.name AS uploader_name").
			Joins("LEFT JOIN categories ON categories.id = documents.category_id").
			Joins("LEFT JOIN users ON users.id = documents.created_by").
			Joins("LEFT JOIN document_access_logs ON document_access_logs.document_id = documents.id").
			Group("documents.id, categories.name, users.name").
			Order("documents.created_at DESC").
			Offset(offset).Limit(limit).
			Scan(&items).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   items,
			"meta":   DocumentListMeta{Page: page, Limit: limit, Total: total},
		})
	}
}
