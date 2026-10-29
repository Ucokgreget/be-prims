package request

import (
	"mime/multipart"
	"time"
)

type CreateDocumentRequest struct {
	Title           string                `form:"title" validate:"required"`
	Description     string                `form:"description" validate:"required"`
	DocumentNumber  string                `form:"document_number" validate:"required"`
	PublicationDate time.Time             `form:"publication_date" time_format:"2006-01-02" validate:"required"`
	IsPrivate       bool                  `form:"is_private"`
	AllowDownload   bool                  `form:"allow_download"`
	CategoryID      uint                  `form:"category_id" validate:"required"`
	DepartmentID    []uint                `form:"department_id" validate:"required,dive,required"`
	TagID           []uint                `form:"tag_id" validate:"required,dive,required"`
	Attachments     *multipart.FileHeader `form:"attachment" validate:"required"`
	SharedTo        []uint                `form:"shared_to" validate:"omitempty,dive,gt=0"`
}
