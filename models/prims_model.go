package models

import "time"

type Document struct {
	Id              uint      `json:"id" gorm:"primaryKey"`
	UUID            string    `json:"uuid" gorm:"varchar(255);unique"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	PublicationDate time.Time `json:"publication_date"`
	IsPrivate       bool      `json:"is_private"`
	CategoryID      uint      `json:"category_id"`
	DepartmentID    uint      `json:"department_id"`
	CompanyID       uint      `json:"company_id" gorm:"index"`

	Category   Category   `json:"category" gorm:"foreignKey:CategoryID"`
	Department Department `json:"department" gorm:"foreignKey:DepartmentID"`
}

type Category struct {
	Id          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CompanyID   uint      `json:"company_id" gorm:"index"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Department struct {
	Id          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CompanyID   uint      `json:"company_id" gorm:"index"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Attachment struct {
	Id         uint      `json:"id" gorm:"primaryKey"`
	DocumentID uint      `json:"document_id"`
	FileName   string    `json:"file_name"`
	FilePath   string    `json:"file_path"`
	CreatedAt  time.Time `json:"created_at"`

	Document Document `json:"document" gorm:"foreignKey:DocumentID"`
}

type Tag struct {
	Id          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DocumentTag struct {
	Id         uint `json:"id" gorm:"primaryKey"`
	DocumentID uint `json:"document_id"`
	TagID      uint `json:"tag_id"`
}

type DocumentShared struct {
	Id         uint      `json:"id" gorm:"primaryKey"`
	DocumentID uint      `json:"document_id"`
	UserID     uint      `json:"user_id"`
	SharedBy   uint      `json:"shared_by"`
	SharedAt   time.Time `json:"shared_at"`

	Document     Document `json:"document" gorm:"foreignKey:DocumentID"`
	User         User     `json:"user" gorm:"foreignKey:UserID"`
	SharedByUser User     `json:"shared_by_user" gorm:"foreignKey:SharedBy"`
}

type DocumentAccessLog struct {
	Id         uint      `json:"id" gorm:"primaryKey"`
	DocumentID uint      `json:"document_id"`
	UserID     uint      `json:"user_id"`
	Action     string    `json:"action"`
	AccessedAt time.Time `json:"accessed_at"`
	IpAddress  string    `json:"ip_address"`
}
