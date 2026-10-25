package request

import (
	"mime/multipart"
	"time"
)

type CreateDocumentRequest struct {
	Title           string                `form:"title" validate:"required"`
	Description     string                `form:"description" validate:"required"`
	PublicationDate time.Time             `form:"publication_date" validate:"required"`
	IsPrivate       bool                  `form:"is_private" validate:"required"`
	CategoryID      uint                  `form:"category_id" validate:"required"`
	DepartmentID    []uint                `form:"department_id" validate:"required,dive,required"`
	TagID           []uint                `form:"tag_id" validate:"required,dive,required"`
	Attachment      *multipart.FileHeader `form:"attachment" validate:"required"`
	SharedTo        []uint                `form:"shared_to" validate:"required,dive,required"`
}
