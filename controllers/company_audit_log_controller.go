package controllers

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"be-prims/audit"
	"be-prims/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type companyAuditUser struct {
	ID          uint    `json:"id"`
	Name        *string `json:"name"`
	BadgeNumber *string `json:"badge_number"`
	Jabatan     *string `json:"jabatan"`
}

type companyAuditLogItem struct {
	ID         uint              `json:"id"`
	Action     string            `json:"action"`
	AccessedAt time.Time         `json:"accessed_at"`
	IPAddress  string            `json:"ip_address"`
	User       companyAuditUser  `json:"user" gorm:"embedded;embeddedPrefix:user_"`
	Document   accessLogDocument `json:"document" gorm:"embedded;embeddedPrefix:document_"`
}

type companyAuditSummary struct {
	TotalActivities int64 `json:"total_activities"`
	TotalViews      int64 `json:"total_views"`
	TotalDownloads  int64 `json:"total_downloads"`
}

var auditSearchEscaper = strings.NewReplacer("~", "~~", "%", "~%", "_", "~_", "[", "~[")

// GetCompanyAuditLogs returns individual audit events for the admin's company.
func GetCompanyAuditLogs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		companyID := middleware.Claims(c).CompanyID
		if companyID == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"status": "error", "message": "Perusahaan tidak valid"})
			return
		}
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

		base := db.WithContext(c.Request.Context()).Table("document_access_logs AS l").
			Joins("JOIN documents AS d ON d.id = l.document_id").
			Joins("LEFT JOIN users AS u ON u.id = l.user_id AND u.company_id = d.company_id").
			Where("d.company_id = ? AND l.action IN (?, ?)", companyID, audit.ActionView, audit.ActionDownload)
		if action := c.Query("action"); action != "" {
			if action != audit.ActionView && action != audit.ActionDownload {
				badFilter("action harus VIEW atau DOWNLOAD")
				return
			}
			base = base.Where("l.action = ?", action)
		}
		if search := strings.TrimSpace(c.Query("search")); search != "" {
			pattern := "%" + auditSearchEscaper.Replace(search) + "%"
			base = base.Where("(u.name LIKE ? ESCAPE '~' OR u.badge_number LIKE ? ESCAPE '~' OR d.title LIKE ? ESCAPE '~' OR d.document_number LIKE ? ESCAPE '~')",
				pattern, pattern, pattern, pattern)
		}

		var dates [2]*time.Time
		for i, key := range []string{"start_date", "end_date"} {
			if raw, present := c.GetQuery(key); present {
				day, err := time.ParseInLocation(time.DateOnly, raw, wibZone)
				if err != nil || day.UTC().Year() < 1 || day.Format(time.DateOnly) != raw {
					badFilter(key + " harus berformat YYYY-MM-DD")
					return
				}
				dates[i] = &day
			}
		}
		from, to := dates[0], dates[1]
		if from != nil && to != nil && to.Before(*from) {
			badFilter("end_date tidak boleh lebih awal dari start_date")
			return
		}
		if from != nil {
			base = base.Where("l.accessed_at >= ?", from.UTC())
		}
		if to != nil {
			base = base.Where("l.accessed_at < ?", to.AddDate(0, 0, 1).UTC())
		}

		var summary companyAuditSummary
		if err := base.Session(&gorm.Session{}).
			Select("COUNT_BIG(*) AS total_activities, COUNT_BIG(CASE WHEN l.action = ? THEN 1 END) AS total_views, COUNT_BIG(CASE WHEN l.action = ? THEN 1 END) AS total_downloads", audit.ActionView, audit.ActionDownload).
			Find(&summary).Error; err != nil {
			internalError(c)
			return
		}
		logs := []companyAuditLogItem{}
		if err := base.
			Select("l.id, l.action, l.accessed_at, l.ip_address, l.user_id, u.name AS user_name, u.badge_number AS user_badge_number, u.jabatan AS user_jabatan, d.id AS document_id, d.uuid AS document_uuid, d.document_number AS document_document_number, d.title AS document_title").
			Order("l.accessed_at DESC, l.id DESC").
			Offset((page - 1) * limit).Limit(limit).Find(&logs).Error; err != nil {
			internalError(c)
			return
		}
		for i := range logs {
			logs[i].AccessedAt = logs[i].AccessedAt.UTC()
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Data audit log berhasil diambil",
			"data": gin.H{
				"summary": summary, "logs": logs,
				"meta": gin.H{"page": page, "limit": limit, "total": summary.TotalActivities,
					"total_pages": (summary.TotalActivities + int64(limit) - 1) / int64(limit)},
			},
		})
	}
}
