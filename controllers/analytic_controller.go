package controllers

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"be-prims/audit"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type dashboardCompanyStorage struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	Code           string `json:"code"`
	StorageQuotaGB int64  `json:"storage_quota_gb"`
	UsedBytes      int64  `json:"used_bytes"`
}

func GetDashboardStatistic(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := db.WithContext(c.Request.Context())
		fail := func(message string) {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": message})
		}

		var totalCompanies, totalDocuments, totalUsers int64
		if err := q.Model(&models.Company{}).Where("status = ?", "active").Count(&totalCompanies).Error; err != nil {
			fail("Gagal menghitung total perusahaan")
			return
		}
		if err := q.Model(&models.Document{}).Count(&totalDocuments).Error; err != nil {
			fail("Gagal menghitung total dokumen")
			return
		}
		if err := q.Model(&models.User{}).Count(&totalUsers).Error; err != nil {
			fail("Gagal menghitung total pengguna")
			return
		}

		var usedBytes, totalQuotaGB int64
		if err := q.Model(&models.Attachment{}).
			Select("COALESCE(SUM(CAST(file_size AS bigint)), 0)").Find(&usedBytes).Error; err != nil {
			fail("Gagal menghitung total penyimpanan terpakai")
			return
		}
		if err := q.Model(&models.Company{}).
			Select("COALESCE(SUM(CAST(storage_quota_gb AS bigint)), 0)").Find(&totalQuotaGB).Error; err != nil {
			fail("Gagal menghitung total kuota penyimpanan")
			return
		}

		companiesStorage := []dashboardCompanyStorage{}
		if err := q.Table("companies AS c").
			Select("c.id, c.name, c.code, c.storage_quota_gb, COALESCE(SUM(CAST(a.file_size AS bigint)), 0) AS used_bytes").
			Joins("LEFT JOIN documents AS d ON d.company_id = c.id").
			Joins("LEFT JOIN attachments AS a ON a.document_id = d.id").
			Group("c.id, c.name, c.code, c.storage_quota_gb").Order("c.id").
			Find(&companiesStorage).Error; err != nil {
			fail("Gagal mengambil rincian penyimpanan perusahaan")
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data": gin.H{
				"total_companies": totalCompanies,
				"total_documents": totalDocuments,
				"total_users":     totalUsers,
				"storage_usage": gin.H{
					"used_bytes":     usedBytes,
					"total_quota_gb": totalQuotaGB,
				},
				"companies_storage": companiesStorage,
			},
		})
	}
}

type globalAuditLogItem struct {
	ID             uint      `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	ActionType     string    `json:"action_type"`
	IPAddress      string    `json:"ip_address"`
	CompanyName    string    `json:"company_name"`
	CompanyCode    string    `json:"company_code"`
	UserName       *string   `json:"user_name"`
	BadgeNumber    string    `json:"badge_number"`
	DocumentTitle  *string   `json:"document_title"`
	DocumentNumber *string   `json:"document_number"`
}

func GetAuditLogs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		badFilter := func(message string) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "error", "message": message})
		}
		page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
		limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "10"))
		if limitErr != nil || limit < 1 || limit > 100 {
			badFilter("limit harus bilangan bulat antara 1 dan 100")
			return
		}
		if pageErr != nil || page < 1 || page-1 > math.MaxInt32/limit {
			badFilter("page tidak valid atau terlalu besar")
			return
		}

		base := db.WithContext(c.Request.Context()).Table("document_access_logs AS al").
			Joins("JOIN users AS u ON u.id = al.user_id").
			Joins("JOIN companies AS c ON c.id = u.company_id").
			Joins("LEFT JOIN documents AS d ON d.id = al.document_id")
		if raw := c.Query("company_id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id < 1 {
				badFilter("company_id harus bilangan bulat positif")
				return
			}
			base = base.Where("c.id = ?", id)
		}
		if action := c.Query("action"); action != "" {
			if action != audit.ActionView && action != audit.ActionDownload {
				badFilter("action harus VIEW atau DOWNLOAD")
				return
			}
			base = base.Where("al.action = ?", action)
		}
		if search := strings.TrimSpace(c.Query("search")); search != "" {
			if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 200 {
				badFilter("search harus berupa teks dengan maksimal 200 karakter")
				return
			}
			pattern := "%" + auditSearchEscaper.Replace(search) + "%"
			base = base.Where("(LOWER(u.name) LIKE LOWER(?) ESCAPE '~' OR LOWER(d.title) LIKE LOWER(?) ESCAPE '~')", pattern, pattern)
		}

		var dates [2]time.Time
		for i, key := range []string{"start_date", "end_date"} {
			if raw := c.Query(key); raw != "" {
				day, err := time.ParseInLocation(time.DateOnly, raw, wibZone)
				if err != nil || day.UTC().Year() < 1 || day.Format(time.DateOnly) != raw {
					badFilter(key + " harus berformat YYYY-MM-DD")
					return
				}
				dates[i] = day
			}
		}
		from, to := dates[0], dates[1]
		if !from.IsZero() && !to.IsZero() && to.Before(from) {
			badFilter("end_date tidak boleh lebih awal dari start_date")
			return
		}
		if !from.IsZero() {
			base = base.Where("al.accessed_at >= ?", from.UTC())
		}
		if !to.IsZero() {
			base = base.Where("al.accessed_at < ?", to.AddDate(0, 0, 1).UTC())
		}

		var total int64
		if err := base.Session(&gorm.Session{}).Select("COUNT_BIG(*)").Find(&total).Error; err != nil {
			internalError(c)
			return
		}
		logs := []globalAuditLogItem{}
		if err := base.Select(`al.id, al.accessed_at AS created_at, al.action AS action_type, al.ip_address,
			c.name AS company_name, c.code AS company_code, u.name AS user_name, u.badge_number,
			d.title AS document_title, d.document_number`).
			Order("al.accessed_at DESC, al.id DESC").Offset((page - 1) * limit).Limit(limit).Find(&logs).Error; err != nil {
			internalError(c)
			return
		}
		for i := range logs {
			logs[i].CreatedAt = logs[i].CreatedAt.UTC()
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data": gin.H{"logs": logs, "meta": gin.H{
				"page": page, "limit": limit, "total": total, "total_pages": (total + int64(limit) - 1) / int64(limit),
			}},
		})
	}
}
