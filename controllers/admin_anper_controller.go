package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

type adminAnperListItem struct {
	ID          uint   `json:"id"`
	BadgeNumber string `json:"badge_number"`
	Name        string `json:"name"`
	Jabatan     string `json:"jabatan"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Company     *struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"company"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func GetAllAdminAnper(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if page < 1 {
			page = 1
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
		if limit < 1 {
			limit = 10
		}
		search := c.Query("search")
		idComp := c.Query("company_id")
		status := c.Query("status")

		tx := db.Model(&models.User{}).Where("role = ?", "ADMIN_ANPER")

		if search != "" {
			tx = tx.Where("LOWER(name) LIKE LOWER(?) OR LOWER(badge_number) LIKE LOWER(?)", "%"+search+"%", "%"+search+"%")
		}

		if idComp != "" {
			tx = tx.Where("company_id = ?", idComp)
		}

		if status != "" {
			tx = tx.Where("status = ?", status)
		}

		var total int64
		tx.Count(&total)

		var adminAnper []models.User
		tx.Preload("Company").
			Order("created_at DESC").
			Offset((page - 1) * limit).
			Limit(limit).
			Find(&adminAnper)

		items := make([]adminAnperListItem, len(adminAnper))
		for i, u := range adminAnper {
			items[i].ID = u.Id
			items[i].BadgeNumber = u.BadgeNumber
			items[i].Name = u.Name
			items[i].Jabatan = u.Jabatan
			items[i].Role = u.Role
			items[i].Status = u.Status
			items[i].LastLoginAt = u.LastLoginAt
			items[i].CreatedAt = u.CreatedAt
			if u.Company != nil {
				items[i].Company = &struct {
					ID   uint   `json:"id"`
					Name string `json:"name"`
					Code string `json:"code"`
				}{
					u.Company.Id,
					u.Company.Name,
					u.Company.Code,
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Data admin anper berhasil diambil",
			"data":    items,
			"pagination": gin.H{
				"current_page": page,
				"limit":        limit,
				"total":        total,
			},
		})

	}
}

func CreateAdminAnper(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input request.AdminAnperRequest

		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}

		if err := validate.Struct(input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}

		var company models.Company
		if err := db.Where("id = ? AND status = ?", input.CompanyID, "active").First(&company).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Company tidak ditemukan atau tidak aktif",
			})
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal memproses password",
			})
			return
		}

		admin := models.User{
			BadgeNumber: input.BadgeNumber,
			Name:        input.Name,
			Jabatan:     input.Jabatan,
			Password:    string(hash),
			Role:        "ADMIN_ANPER",
			Status:      models.StatusActive,
			CompanyID:   input.CompanyID,
		}

		if err := db.Create(&admin).Error; err != nil {
			if errors.Is(sqlserver.Dialector{}.Translate(err), gorm.ErrDuplicatedKey) {
				c.JSON(http.StatusConflict, gin.H{
					"status":  "error",
					"message": "Badge number sudah terdaftar",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal menyimpan data admin anper",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Admin anper berhasil ditambahkan",
			"data": gin.H{
				"id":           admin.Id,
				"badge_number": admin.BadgeNumber,
				"name":         admin.Name,
				"role":         admin.Role,
			},
		})
	}
}

func UpdateAdminAnperStatus(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "ID Admin Anper tidak valid"})
			return
		}

		var input request.UpdateUserStatusRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Status harus active atau inactive"})
			return
		}

		result := db.WithContext(c.Request.Context()).Model(&models.User{}).
			Where("id = ? AND role = ?", id, models.RoleAdminAnper).Update("status", input.Status)
		switch {
		case result.Error != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Gagal memperbarui status akun"})
		case result.RowsAffected == 0:
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Admin Anper tidak ditemukan"})
		default:
			c.JSON(http.StatusOK, gin.H{"success": true, "message": "Status akun berhasil diperbarui."})
		}
	}
}

func AdminAnperResetPassword(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "ID Admin Anper tidak valid"})
			return
		}

		var input request.AdminAnperResetPasswordRequest
		if err := c.ShouldBindJSON(&input); err != nil || validate.Struct(&input) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Invalid input"})
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Password baru maksimal 72 byte"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal memproses password"})
			return
		}

		actorID := middleware.Claims(c).UserID
		err = db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.User{}).Where("id = ? AND role = ?", id, models.RoleAdminAnper).
				UpdateColumn("password", string(hash))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			return tx.Create(&models.AuditLog{
				ActorID: actorID,
				Message: fmt.Sprintf("Superadmin mereset password Admin Anper ID %d", id),
			}).Error
		})
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Admin Anper tidak ditemukan"})
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal mereset password Admin Anper"})
		default:
			c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Password Admin Anper berhasil direset"})
		}
	}
}

func UpdateAdminAnper(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "ID Admin Anper tidak valid"})
			return
		}

		var input request.UpdateAdminAnperRequest
		if err := c.ShouldBindJSON(&input); err != nil || validate.Struct(&input) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Data akun Admin Anper tidak valid"})
			return
		}

		badge := strings.TrimSpace(input.BadgeNumber)
		name := strings.TrimSpace(input.Name)
		jabatan := strings.TrimSpace(input.Jabatan)
		if badge == "" || name == "" || jabatan == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "No. Badge, Nama, dan Jabatan wajib diisi"})
			return
		}

		var company models.Company
		if err := db.WithContext(c.Request.Context()).
			Where("id = ? AND status = ?", input.CompanyID, models.StatusActive).
			First(&company).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Anak perusahaan tidak ditemukan atau tidak aktif"})
			return
		}

		var dupCount int64
		if err := db.WithContext(c.Request.Context()).Model(&models.User{}).
			Where("company_id = ? AND badge_number = ? AND id <> ?", input.CompanyID, badge, id).
			Count(&dupCount).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal memeriksa duplikasi No. Badge"})
			return
		}
		if dupCount > 0 {
			c.JSON(http.StatusConflict, gin.H{"status": "error", "message": "No. Badge sudah terdaftar di perusahaan ini"})
			return
		}

		actorID := middleware.Claims(c).UserID
		err = db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.User{}).
				Where("id = ? AND role = ?", id, models.RoleAdminAnper).
				Updates(map[string]any{
					"company_id":   input.CompanyID,
					"badge_number": badge,
					"name":         name,
					"jabatan":      jabatan,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			return tx.Create(&models.AuditLog{
				ActorID: actorID,
				Message: fmt.Sprintf("Superadmin memperbarui data Admin Anper ID %d", id),
			}).Error
		})
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Admin Anper tidak ditemukan"})
			return
		case err != nil && errors.Is(sqlserver.Dialector{}.Translate(err), gorm.ErrDuplicatedKey):
			c.JSON(http.StatusConflict, gin.H{"status": "error", "message": "No. Badge sudah terdaftar di perusahaan ini"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal memperbarui data Admin Anper"})
			return
		}

		var updated models.User
		_ = db.WithContext(c.Request.Context()).Preload("Company").First(&updated, id).Error
		item := adminAnperListItem{
			ID:          updated.Id,
			BadgeNumber: updated.BadgeNumber,
			Name:        updated.Name,
			Jabatan:     updated.Jabatan,
			Role:        updated.Role,
			Status:      updated.Status,
			LastLoginAt: updated.LastLoginAt,
			CreatedAt:   updated.CreatedAt,
		}
		if updated.Company != nil {
			item.Company = &struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
				Code string `json:"code"`
			}{
				ID:   updated.Company.Id,
				Name: updated.Company.Name,
				Code: updated.Company.Code,
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Data akun Admin Anak Perusahaan berhasil diperbarui.",
			"data":    item,
		})
	}
}
