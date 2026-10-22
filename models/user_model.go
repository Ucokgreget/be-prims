package models

import "time"

// Role yang dikenali sistem (lihat ERD PRIMS).
const (
	RoleSuperAdmin = "SUPERADMIN"
	RoleAdminAnper = "ADMIN_ANPER"
	RoleKaryawan   = "KARYAWAN"
)

// Status akun user.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

type User struct {
	Id           uint      `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name" gorm:"size:150"`
	NPP          string    `json:"npp" gorm:"size:50;uniqueIndex;not null"`
	Password     string    `json:"-" gorm:"size:255;not null"`
	Jabatan      string    `json:"jabatan" gorm:"size:100"`
	Role         string    `json:"role" gorm:"size:20;default:'KARYAWAN'"`
	Status       string    `json:"status" gorm:"size:20;default:'active'"`
	DepartmentID *uint     `json:"department_id"`
	CompanyID    uint      `json:"company_id" gorm:"index"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	Department *Department `json:"department" gorm:"foreignKey:DepartmentID"`
	Company    *Company    `json:"company" gorm:"foreignKey:CompanyID"`
}

type Company struct {
	Id        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"size:150"`
	Code      string    `json:"code" gorm:"size:50;uniqueIndex"`
	Status    string    `json:"status" gorm:"size:20;default:'active'"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
