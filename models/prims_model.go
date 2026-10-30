package models

import "time"

type Document struct {
	Id              uint       `json:"id" gorm:"primaryKey"`
	UUID            string     `json:"uuid" gorm:"varchar(255);unique"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	DocumentNumber  string     `json:"document_number"`
	PublicationDate time.Time  `json:"publication_date"`
	IsPrivate       bool       `json:"is_private"`
	AllowDownload   bool       `json:"allow_download"`
	CategoryID      uint       `json:"category_id"`
	CompanyID       uint       `json:"company_id" gorm:"index"`
	CreatedBy       uint       `json:"created_by"`
	UpdatedBy       *uint      `json:"updated_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       *time.Time `json:"updated_at"`

	Category    Category             `json:"category" gorm:"foreignKey:CategoryID"`
	Company     Company              `json:"company" gorm:"foreignKey:CompanyID"`
	Creator     User                 `json:"creator" gorm:"foreignKey:CreatedBy"`
	Updater     *User                `json:"updater" gorm:"foreignKey:UpdatedBy"`
	Attachment  Attachment           `json:"attachment" gorm:"foreignKey:DocumentID"`
	Tags        []DocumentTag        `json:"tags" gorm:"foreignKey:DocumentID"`
	Departments []DocumentDepartment `json:"departments" gorm:"foreignKey:DocumentID"`
}

type Category struct {
	Id          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Level       *int      `json:"level"` // null = tanpa level (SK Direksi); 1-5 = Pedoman..Catatan Mutu
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

type DocumentDepartment struct {
	Id           uint `json:"id" gorm:"primaryKey"`
	DocumentID   uint `json:"document_id"`
	DepartmentID uint `json:"department_id"`

	Department Department `json:"department" gorm:"foreignKey:DepartmentID"`
}

type Attachment struct {
	Id         uint      `json:"id" gorm:"primaryKey"`
	DocumentID uint      `json:"document_id"`
	FileName   string    `json:"file_name"`
	FilePath   string    `json:"file_path"`
	FileSize   int64     `json:"file_size"`
	CreatedAt  time.Time `json:"created_at"`
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

	Tag Tag `json:"tag" gorm:"foreignKey:TagID"`
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
