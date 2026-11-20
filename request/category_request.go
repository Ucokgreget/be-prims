package request

import "encoding/json"

type CreateCategoryRequest struct {
	Name        string  `json:"name" validate:"required"`
	Description *string `json:"description"`
	Level       *int    `json:"level" validate:"omitempty,min=1,max=5"`
	Status      *string `json:"status" validate:"omitempty,oneof=active inactive"`
}

type UpdateCategoryRequest struct {
	Name        *string         `json:"name" validate:"omitempty,min=1"`
	Description *string         `json:"description"`
	Level       json.RawMessage `json:"level"` // Omitted leaves it unchanged; null sets SK Direksi.
	Status      *string         `json:"status" validate:"omitempty,oneof=active inactive"`
}
