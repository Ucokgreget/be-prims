package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"be-prims/models"
	"be-prims/request"
	"be-prims/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

// The login dropdown does not need addresses, quotas, or usage figures.
type publicCompany struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Code      string    `json:"code"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func GetAllCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		companies := []publicCompany{}
		if err := db.WithContext(c.Request.Context()).Model(&models.Company{}).Order("id").Find(&companies).Error; err != nil {
			internalError(c)
			return
		}
		c.JSON(http.StatusOK, companies)
	}
}

func GetCompanyByID(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 63)
		if err != nil || id == 0 {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Company not found"})
			return
		}
		var company publicCompany
		err = db.WithContext(c.Request.Context()).Model(&models.Company{}).Where("id = ?", id).Take(&company).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Company not found"})
			return
		}
		if err != nil {
			internalError(c)
			return
		}
		c.JSON(http.StatusOK, company)
	}
}

type companyListItem struct {
	ID               uint    `json:"id"`
	Name             string  `json:"name"`
	Code             string  `json:"code"`
	Address          string  `json:"address"`
	StorageQuotaGB   int     `json:"storage_quota_gb"`
	StorageUsedBytes int64   `json:"storage_used_bytes"`
	StorageUsedGB    float64 `json:"storage_used_gb" gorm:"-"`
	TotalUsers       int64   `json:"total_users"`
	TotalDocuments   int64   `json:"total_documents"`
	Status           string  `json:"status"`
}

const bytesPerGB = 1 << 30 // The API's GB fields use 1024³ bytes, matching the example.

// Count only attachments registered to company-owned documents, including legacy object keys.
func companyStorageBytes(db *gorm.DB) *gorm.DB {
	return db.Table("documents AS d").Joins("JOIN attachments AS a ON a.document_id = d.id").
		Select("COALESCE(SUM(CAST(a.file_size AS bigint)), 0)")
}

func ListCompanies(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
		limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "10"))
		if limitErr != nil || limit < 1 || limit > 100 {
			companyError(c, http.StatusBadRequest, "limit harus bilangan bulat antara 1 dan 100")
			return
		}
		if pageErr != nil || page < 1 || page-1 > math.MaxInt32/limit {
			companyError(c, http.StatusBadRequest, "page tidak valid atau terlalu besar")
			return
		}
		q := db.WithContext(c.Request.Context())
		base := q.Table("companies AS c")
		if status := c.Query("status"); status != "" {
			if !validCompanyStatus(status) {
				companyError(c, http.StatusBadRequest, "Status harus active atau inactive.")
				return
			}
			base = base.Where("c.status = ?", status)
		}
		if search := strings.ToLower(strings.TrimSpace(c.Query("search"))); search != "" {
			pattern := "%" + auditSearchEscaper.Replace(search) + "%"
			base = base.Where("(LOWER(c.name) LIKE ? ESCAPE '~' OR LOWER(c.code) LIKE ? ESCAPE '~')", pattern, pattern)
		}
		var total int64
		if err := base.Session(&gorm.Session{}).Select("COUNT_BIG(*)").Find(&total).Error; err != nil {
			companyError(c, http.StatusInternalServerError, "Gagal mengambil daftar perusahaan.")
			return
		}
		items := []companyListItem{}
		if err := base.Select(`c.id, c.name, c.code, c.address, c.status, c.storage_quota_gb,
			(SELECT COUNT_BIG(*) FROM users AS u WHERE u.company_id = c.id) AS total_users,
			(SELECT COUNT_BIG(*) FROM documents AS d WHERE d.company_id = c.id) AS total_documents,
			(?) AS storage_used_bytes`, companyStorageBytes(q).Where("d.company_id = c.id")).
			Order("c.id").Offset((page - 1) * limit).Limit(limit).Find(&items).Error; err != nil {
			companyError(c, http.StatusInternalServerError, "Gagal mengambil daftar perusahaan.")
			return
		}
		for i := range items {
			items[i].StorageUsedGB = float64(items[i].StorageUsedBytes) / bytesPerGB
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": items,
			"pagination": gin.H{"page": page, "limit": limit, "total": total}})
	}
}

func CreateCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		company, ok := bindCompany(c, false)
		if !ok {
			return
		}
		err := db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&company).Error; err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
			defer cancel()
			return storage.CreateTenantFolder(ctx, company.Id)
		})
		if err != nil {
			companyWriteError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"success": true, "message": "Anak Perusahaan berhasil didaftarkan.",
			"data": gin.H{"id": company.Id, "name": company.Name, "code": company.Code}})
	}
}

func UpdateCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := companyID(c)
		if !ok {
			return
		}
		company, ok := bindCompany(c, true)
		if !ok {
			return
		}
		update := db.WithContext(c.Request.Context()).Model(&models.Company{}).Where("id = ?", id).
			Updates(map[string]any{"name": company.Name, "code": company.Code,
				"storage_quota_gb": company.StorageQuotaGB, "address": company.Address})
		companyUpdated(c, update, "Data perusahaan berhasil diperbarui.")
	}
}

func UpdateCompanyStatus(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := companyID(c)
		if !ok {
			return
		}
		var input struct {
			Status string `json:"status"`
		}
		if !bindCompanyJSON(c, &input) {
			return
		}
		if !validCompanyStatus(input.Status) {
			companyError(c, http.StatusBadRequest, "Status harus active atau inactive.")
			return
		}
		changes := map[string]any{"status": input.Status}
		if input.Status == models.StatusInactive {
			changes["auth_version"] = gorm.Expr("CASE WHEN status = ? THEN auth_version ELSE auth_version + 1 END", models.StatusInactive)
		}
		update := db.WithContext(c.Request.Context()).Model(&models.Company{}).Where("id = ?", id).Updates(changes)
		companyUpdated(c, update, "Status perusahaan berhasil diperbarui.")
	}
}

func validCompanyStatus(status string) bool {
	return status == models.StatusActive || status == models.StatusInactive
}

func companyID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 63)
	if err != nil || id == 0 {
		companyError(c, http.StatusBadRequest, "ID perusahaan tidak valid.")
		return 0, false
	}
	return id, true
}

func bindCompanyJSON(c *gin.Context, input any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil || decoder.Decode(new(any)) != io.EOF {
		companyError(c, http.StatusBadRequest, "Data perusahaan tidak valid.")
		return false
	}
	return true
}

func bindCompany(c *gin.Context, updating bool) (models.Company, bool) {
	var input request.CompanyRequest
	if !bindCompanyJSON(c, &input) {
		return models.Company{}, false
	}
	company := models.Company{Name: strings.TrimSpace(input.Name), Code: strings.TrimSpace(input.Code), Address: strings.TrimSpace(input.Address)}
	switch {
	case company.Name == "" || utf16Length(company.Name) > 150:
		companyError(c, http.StatusBadRequest, "Nama perusahaan wajib diisi, maksimal 150 karakter.")
	case company.Code == "" || utf16Length(company.Code) > 50:
		companyError(c, http.StatusBadRequest, "Kode perusahaan wajib diisi, maksimal 50 karakter.")
	case utf16Length(company.Address) > 255:
		companyError(c, http.StatusBadRequest, "Alamat perusahaan maksimal 255 karakter.")
	case (updating && input.StorageQuotaGB == nil) || (input.StorageQuotaGB != nil && (*input.StorageQuotaGB < 1 || *input.StorageQuotaGB > math.MaxInt64/bytesPerGB)):
		companyError(c, http.StatusBadRequest, "Kuota penyimpanan harus bilangan bulat positif dalam GB.")
	default:
		if input.StorageQuotaGB != nil {
			company.StorageQuotaGB = *input.StorageQuotaGB
		}
		return company, true
	}
	return models.Company{}, false
}

// SQL Server nvarchar(n) measures UTF-16 code units, not Unicode code points.
func utf16Length(s string) int { return len(utf16.Encode([]rune(s))) }

func companyWriteError(c *gin.Context, err error) {
	if errors.Is((sqlserver.Dialector{}).Translate(err), gorm.ErrDuplicatedKey) {
		companyError(c, http.StatusBadRequest, "Kode perusahaan sudah terdaftar")
		return
	}
	log.Printf("company: gagal menyimpan perubahan: %v", err)
	companyError(c, http.StatusInternalServerError, "Gagal menyimpan data perusahaan.")
}

func companyUpdated(c *gin.Context, result *gorm.DB, message string) {
	if result.Error != nil {
		companyWriteError(c, result.Error)
	} else if result.RowsAffected == 0 {
		companyError(c, http.StatusNotFound, "Perusahaan tidak ditemukan.")
	} else {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": message})
	}
}

func companyError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"success": false, "message": message})
}
