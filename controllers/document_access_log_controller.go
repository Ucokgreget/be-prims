package controllers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"be-prims/audit"
	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- US-13-BE: GET /api/v1/documents/:id/access-logs ----

type accessLogDocument struct {
	Id             uint   `json:"id"`
	UUID           string `json:"uuid"`
	DocumentNumber string `json:"document_number"`
	Title          string `json:"title"`
}

// accessLogReader: satu karyawan unik yang pernah membaca (panel "Views").
type accessLogReader struct {
	Id          uint   `json:"id"`
	Name        string `json:"name"`
	BadgeNumber string `json:"badge_number"`
	Jabatan     string `json:"jabatan"`
}

// accessLogRow: hasil agregasi per karyawan dari DB (panel "Views & Downloads").
type accessLogRow struct {
	UserID       uint
	Name         string
	BadgeNumber  string
	IpAddress    string
	ViewedAt     *time.Time
	DownloadedAt *time.Time
}

type accessLogItem struct {
	UserID       uint       `json:"user_id"`
	Name         string     `json:"name"`
	BadgeNumber  string     `json:"badge_number"`
	IpAddress    string     `json:"ip_address"`
	ViewedAt     *time.Time `json:"viewed_at"`
	DownloadedAt *time.Time `json:"downloaded_at"`
}

type accessLogData struct {
	Document   accessLogDocument `json:"document"`
	Readers    []accessLogReader `json:"readers"`
	AccessLogs []accessLogItem   `json:"access_logs"`
}

// readersSQL: karyawan unik yang punya log VIEW pada dokumen ini.
//
// INNER JOIN users: log user yang sudah dihapus tetap tersimpan di tabel
// (kebutuhan forensik), tapi tidak bisa ditampilkan tanpa identitas.
const readersSQL = `
SELECT DISTINCT u.id, u.name, u.badge_number, u.jabatan
FROM document_access_logs l
INNER JOIN users u ON u.id = l.user_id
WHERE l.document_id = ? AND l.action = ?
ORDER BY u.name ASC`

// accessLogsSQL: satu baris per karyawan dengan waktu VIEW & DOWNLOAD terakhir.
//
// ip_address = IP akses TERAKHIR (VIEW atau DOWNLOAD mana pun yang paling
// baru), diambil lewat OUTER APPLY TOP 1. MAX(ip_address) seperti di draft
// tiket keliru: itu nilai terbesar secara leksikografis ("192.168.1.9" >
// "192.168.1.50"), bukan IP akses terakhir. Tie-break l.id DESC membuat hasil
// deterministik bila dua aksi tercatat pada timestamp yang sama.
const accessLogsSQL = `
SELECT
    u.id AS user_id,
    u.name,
    u.badge_number,
    last_ip.ip_address,
    agg.viewed_at,
    agg.downloaded_at
FROM (
    SELECT
        l.user_id,
        MAX(CASE WHEN l.action = ? THEN l.accessed_at END) AS viewed_at,
        MAX(CASE WHEN l.action = ? THEN l.accessed_at END) AS downloaded_at,
        MAX(l.accessed_at) AS last_accessed_at
    FROM document_access_logs l
    WHERE l.document_id = ? AND l.action IN (?, ?)
    GROUP BY l.user_id
) agg
INNER JOIN users u ON u.id = agg.user_id
OUTER APPLY (
    SELECT TOP 1 l2.ip_address
    FROM document_access_logs l2
    WHERE l2.document_id = ? AND l2.user_id = agg.user_id AND l2.action IN (?, ?)
    ORDER BY l2.accessed_at DESC, l2.id DESC
) last_ip
ORDER BY agg.last_accessed_at DESC, u.id ASC`

// GetDocumentAccessLogs menyajikan data panel "Views" dan "Views & Downloads"
// di halaman detail dokumen admin (mockup hal. 6 & 7).
//
// Role ADMIN_ANPER ditegakkan di route (RequireRole → 403). Dokumen dicari
// dengan id + company_id dari JWT: dokumen anper lain, id tidak valid, atau
// tidak ada sama-sama 404 sehingga keberadaan dokumen tenant lain tidak bocor.
func GetDocumentAccessLogs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			documentNotFound(c)
			return
		}

		var document models.Document
		err = db.
			Select("id", "uuid", "document_number", "title").
			Where("id = ? AND company_id = ?", id, claims.CompanyID).
			Take(&document).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			documentNotFound(c)
			return
		}
		if err != nil {
			internalError(c)
			return
		}

		readers := make([]accessLogReader, 0)
		if err := db.Raw(readersSQL, document.Id, audit.ActionView).Find(&readers).Error; err != nil {
			internalError(c)
			return
		}

		var rows []accessLogRow
		if err := db.Raw(accessLogsSQL,
			audit.ActionView, audit.ActionDownload,
			document.Id, audit.ActionView, audit.ActionDownload,
			document.Id, audit.ActionView, audit.ActionDownload,
		).Find(&rows).Error; err != nil {
			internalError(c)
			return
		}

		logs := make([]accessLogItem, len(rows))
		for i, r := range rows {
			logs[i] = accessLogItem{
				UserID:       r.UserID,
				Name:         r.Name,
				BadgeNumber:  r.BadgeNumber,
				IpAddress:    r.IpAddress,
				ViewedAt:     utcPtr(r.ViewedAt),
				DownloadedAt: utcPtr(r.DownloadedAt),
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Riwayat akses dokumen berhasil diambil",
			"data": accessLogData{
				Document: accessLogDocument{
					Id:             document.Id,
					UUID:           document.UUID,
					DocumentNumber: document.DocumentNumber,
					Title:          document.Title,
				},
				Readers:    readers,
				AccessLogs: logs,
			},
		})
	}
}

func documentNotFound(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
		"status":  "error",
		"message": "Dokumen tidak ditemukan",
	})
}

// utcPtr menormalkan waktu ke UTC (accessed_at disimpan sebagai
// datetimeoffset) agar kontrak response konsisten "....Z". nil tetap nil:
// karyawan yang belum pernah mengunduh mendapat downloaded_at: null.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
