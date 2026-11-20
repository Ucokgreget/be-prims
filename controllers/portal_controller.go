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
	Id              uint    `json:"id"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	Level           *int    `json:"level"` // null = SK Direksi
	Status          string  `json:"status"`
	CompanyID       uint    `json:"company_id"`
	ActiveDocuments int64   `json:"active_documents"`
}

// GetPortalCategories menampilkan kategori aktif dan jumlah dokumen publiknya.
func GetPortalCategories(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		query, ok := filterCategoryLevel(c, db.WithContext(c.Request.Context()).Model(&models.Category{}).
			Where("categories.company_id = ? AND categories.status = ?", claims.CompanyID, models.StatusActive))
		if !ok {
			return
		}
		items := []PortalCategoryItem{}
		if err := query.
			Select("categories.id, categories.name, categories.description, categories.level, categories.status, categories.company_id, "+
				"COUNT(documents.id) AS active_documents").
			Joins("LEFT JOIN documents ON documents.category_id = categories.id AND documents.company_id = categories.company_id AND documents.is_private = ?", false).
			Group("categories.id, categories.name, categories.description, categories.level, categories.status, categories.company_id").
			Order("categories.level ASC, categories.name ASC").
			Find(&items).Error; err != nil {
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

// GetPortalDocuments menampilkan dokumen publik dari kategori aktif.
// Filter: q / category_id / tag_id (multi, OR) / department_id / year / level.
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

		base, ok := filterCategoryLevel(c, db.WithContext(c.Request.Context()).Model(&models.Document{}).
			Joins("JOIN categories ON categories.id = documents.category_id AND categories.company_id = documents.company_id").
			Where("documents.company_id = ? AND documents.is_private = ? AND categories.status = ?", claims.CompanyID, false, models.StatusActive))
		if !ok {
			return
		}

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

		items := []PortalDocumentItem{}
		if err := base.
			Select("documents.id, documents.uuid, documents.title, documents.document_number, " +
				"documents.publication_date, categories.name AS category_name").
			Order("documents.publication_date DESC").
			Offset((page - 1) * limit).Limit(limit).
			Find(&items).Error; err != nil {
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
