package models

import "time"

// Role yang dikenali sistem (lihat ERD PRIMS).
const (
	RoleSuperAdmin = "SUPERADMIN"
	RoleAdminAnper = "ADMIN_ANPER"
	RoleKaryawan   = "KARYAWAN"
)

// Status akun dan perusahaan.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

type User struct {
	Id           uint      `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name" gorm:"size:150"`
	BadgeNumber  string    `json:"badge_number" gorm:"size:50;not null;uniqueIndex:uk_badge_company"`
	Password     string    `json:"-" gorm:"size:255;not null"`
	Jabatan      string    `json:"jabatan" gorm:"size:100"`
	Role         string    `json:"role" gorm:"size:20;default:'KARYAWAN'"`
	Status       string    `json:"status" gorm:"size:20;default:'active'"`
	DepartmentID *uint     `json:"department_id"`
	CompanyID    uint      `json:"company_id" gorm:"index;uniqueIndex:uk_badge_company"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	Department *Department `json:"department" gorm:"foreignKey:DepartmentID"`
	Company    *Company    `json:"company" gorm:"foreignKey:CompanyID"`
}

// AuditLog menyimpan aktivitas administratif, terpisah dari log akses dokumen.
type AuditLog struct {
	ID        uint      `gorm:"primaryKey"`
	ActorID   uint      `gorm:"not null"`
	Message   string    `gorm:"size:255;not null"`
	CreatedAt time.Time `gorm:"not null"`
}

type Company struct {
	Id             uint      `json:"id" gorm:"primaryKey"`
	Name           string    `json:"name" gorm:"size:150;not null"`
	Code           string    `json:"code" gorm:"size:50;not null;uniqueIndex"`
	StorageQuotaGB int       `json:"storage_quota_gb" gorm:"not null;default:50;check:storage_quota_gb > 0"`
	Address        string    `json:"address" gorm:"size:255"`
	Status         string    `json:"status" gorm:"size:20;default:'active'"`
	AuthVersion    int64     `json:"-" gorm:"not null;default:0"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
