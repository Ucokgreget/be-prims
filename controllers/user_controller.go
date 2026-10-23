package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/request"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type userListItem struct {
	ID          uint   `json:"id"`
	BadgeNumber string `json:"badge_number"`
	Name        string `json:"name"`
	Jabatan     string `json:"jabatan"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Department  *struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	} `json:"department"`
	CreatedAt time.Time `json:"created_at"`
}

// ListUsers mengembalikan daftar karyawan dalam company pemanggil.
func ListUsers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
		search := c.Query("search")
		idDept := c.Query("id_department")
		status := c.Query("status")

		tx := db.Model(&models.User{}).Where("company_id = ?", claims.CompanyID)

		if search != "" {
			tx = tx.Where("name ILIKE ? OR npp ILIKE ?", "%"+search+"%", "%"+search+"%")
		}
		if idDept != "" {
			tx = tx.Where("department_id = ?", idDept)
		}
		if status != "" {
			tx = tx.Where("status = ?", status)
		}

		var total int64
		tx.Count(&total)

		var users []models.User
		tx.Preload("Department").
			Order("created_at DESC").
			Offset((page - 1) * limit).
			Limit(limit).
			Find(&users)

		items := make([]userListItem, len(users))
		for i, u := range users {
			items[i].ID = u.Id
			items[i].BadgeNumber = u.BadgeNumber
			items[i].Name = u.Name
			items[i].Jabatan = u.Jabatan
			items[i].Role = u.Role
			items[i].Status = u.Status
			items[i].CreatedAt = u.CreatedAt
			if u.Department != nil {
				items[i].Department = &struct {
					ID   uint   `json:"id"`
					Name string `json:"name"`
				}{u.Department.Id, u.Department.Name}
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Data karyawan berhasil diambil.",
			"data":    items,
			"pagination": gin.H{
				"current_page":  page,
				"per_page":      limit,
				"total_records": total,
				"total_pages":   (total + int64(limit) - 1) / int64(limit),
			},
		})
	}
}

// CreateUser mendaftarkan karyawan baru dalam company pemanggil.
func CreateUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var input request.CreateUserRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Data tidak valid: " + err.Error(),
			})
			return
		}

		// Validasi department exists dalam company ini
		var dept models.Department
		if err := db.Where("id = ? AND company_id = ?", input.DepartmentID, claims.CompanyID).
			First(&dept).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Departemen tidak ditemukan.",
			})
			return
		}

		// Cek duplikat NPP dalam company ini
		var exists int64
		db.Model(&models.User{}).
			Where("npp = ? AND company_id = ?", input.NPP, claims.CompanyID).
			Count(&exists)
		if exists > 0 {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"success":    false,
				"message":    "No. Badge (NPP) tersebut sudah terdaftar di sistem.",
				"error_code": "DUPLICATE_NPP",
			})
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"success":    false,
				"message":    "Gagal memproses password.",
				"error_code": "INTERNAL_ERROR",
			})
			return
		}

		user := models.User{
			BadgeNumber:  input.NPP,
			Name:         input.Name,
			Jabatan:      input.Jabatan,
			Password:     string(hash),
			Role:         input.Role,
			Status:       models.StatusActive,
			DepartmentID: &input.DepartmentID,
			CompanyID:    claims.CompanyID,
		}
		if err := db.Create(&user).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"success":    false,
				"message":    "Gagal menyimpan karyawan baru.",
				"error_code": "INTERNAL_ERROR",
			})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"success": true,
			"message": "Karyawan baru berhasil didaftarkan.",
			"data": gin.H{
				"id":            user.Id,
				"npp":           user.BadgeNumber,
				"name":          user.Name,
				"jabatan":       user.Jabatan,
				"role":          user.Role,
				"status":        user.Status,
				"id_department": user.DepartmentID,
				"created_at":    user.CreatedAt,
			},
		})
	}
}

// UpdateUserStatus mengubah status aktif/inactive karyawan dalam company pemanggil.
func UpdateUserStatus(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "ID pengguna tidak valid.",
				"error_code": "VALIDATION_ERROR",
			})
			return
		}

		var input request.UpdateUserStatusRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "Data tidak valid: " + err.Error(),
				"error_code": "VALIDATION_ERROR",
			})
			return
		}

		// Load user dengan tenant scoping — 404 kalau tidak ada atau beda company
		var user models.User
		if err := db.Where("id = ? AND company_id = ?", id, claims.CompanyID).
			First(&user).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"success":    false,
				"message":    "Pengguna tidak ditemukan.",
				"error_code": "NOT_FOUND",
			})
			return
		}

		// Cegah self-deactivate
		if user.Id == claims.UserID {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "Anda tidak dapat menonaktifkan akun Anda sendiri.",
				"error_code": "CANNOT_DEACTIVATE_SELF",
			})
			return
		}

		if err := db.Model(&user).Update("status", input.Status).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"success":    false,
				"message":    "Gagal memperbarui status.",
				"error_code": "INTERNAL_ERROR",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Status akun berhasil diperbarui menjadi " + input.Status + ".",
			"data": gin.H{
				"id":         user.Id,
				"status":     user.Status,
				"updated_at": user.UpdatedAt,
			},
		})
	}
}

// ListDepartments mengembalikan daftar departemen untuk dropdown.
func ListDepartments(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		var depts []models.Department
		db.Where("company_id = ?", claims.CompanyID).
			Order("name ASC").
			Find(&depts)

		type deptItem struct {
			ID          uint   `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		items := make([]deptItem, len(depts))
		for i, d := range depts {
			items[i] = deptItem{d.Id, d.Name, d.Description}
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Data departemen berhasil dimuat.",
			"data":    items,
		})
	}
}
