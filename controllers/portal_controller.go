package controllers

import (
	"net/http"
	"strconv"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- GET /api/v1/portal/categories ----

// PortalCategoryItem: kategori + jumlah dokumen aktif (publik) di kategori itu.
type PortalCategoryItem struct {
	Id              uint   `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Level           *int   `json:"level"` // null = tanpa level (SK Direksi)
	CompanyID       uint   `json:"company_id"`
	ActiveDocuments int64  `json:"active_documents"`
}

// GetPortalCategories: master kategori milik company + levelnya,
// beserta jumlah dokumen aktif (is_private = false) per kategori.
func GetPortalCategories(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var items []PortalCategoryItem
		if err := db.Model(&models.Category{}).
			Select("categories.id, categories.name, categories.description, categories.level, categories.company_id, "+
				"COUNT(documents.id) AS active_documents").
			Joins("LEFT JOIN documents ON documents.category_id = categories.id AND documents.is_private = ?", false).
			Where("categories.company_id = ?", claims.CompanyID).
			Group("categories.id").
			Order("categories.level ASC, categories.name ASC").
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
		})
	}
}

// ---- GET /api/v1/portal/documents ----

// PortalDocumentItem: metadata kartu dokumen sesuai mockup Hal. 1-2.
type PortalDocumentItem struct {
	Id              uint   `json:"id"`
	UUID            string `json:"uuid"`
	Title           string `json:"title"`
	DocumentNumber  string `json:"document_number"`
	PublicationDate string `json:"publication_date"`
	CategoryName    string `json:"category_name"`
}

type PortalDocumentMeta struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// GetPortalDocuments: katalog dokumen publik (is_private = false WAJIB),
// filter q / category_id / tag_id (multi, OR) / department_id / year.
func GetPortalDocuments(db *gorm.DB) gin.HandlerFunc {
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

		base := db.Model(&models.Document{}).
			Where("documents.company_id = ? AND documents.is_private = ?", claims.CompanyID, false)

		if q := c.Query("q"); q != "" {
			like := "%" + q + "%"
			base = base.Where("documents.title LIKE ? OR documents.document_number LIKE ?", like, like)
		}
		if catID := c.Query("category_id"); catID != "" {
			base = base.Where("documents.category_id = ?", catID)
		}
		if year := c.Query("year"); year != "" {
			base = base.Where("YEAR(documents.publication_date) = ?", year)
		}

		// tag_id: multi (tag_id=1&tag_id=2), OR — punya minimal satu tag
		if tagIDs := c.QueryArray("tag_id"); len(tagIDs) > 0 {
			base = base.Where(
				"documents.id IN (SELECT document_id FROM document_tags WHERE tag_id IN ?)", tagIDs)
		}
		// department_id
		if deptID := c.Query("department_id"); deptID != "" {
			base = base.Where(
				"documents.id IN (SELECT document_id FROM document_departments WHERE department_id = ?)", deptID)
		}

		var total int64
		if err := base.Count(&total).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		var items []PortalDocumentItem
		if err := base.
			Select("documents.id, documents.uuid, documents.title, documents.document_number, " +
				"documents.publication_date, categories.name AS category_name").
			Joins("LEFT JOIN categories ON categories.id = documents.category_id").
			Order("documents.publication_date DESC").
			Offset((page - 1) * limit).Limit(limit).
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
			"meta":   PortalDocumentMeta{Page: page, Limit: limit, Total: total},
		})
	}
}
