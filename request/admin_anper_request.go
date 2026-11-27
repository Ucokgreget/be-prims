package request

type AdminAnperRequest struct {
	CompanyID   uint   `json:"company_id" validate:"required"`
	BadgeNumber string `json:"badge_number" validate:"required"`
	Name        string `json:"name" validate:"required"`
	Jabatan     string `json:"jabatan" validate:"required"`
	Password    string `json:"password" validate:"required"`
}

type AdminAnperResetPasswordRequest struct {
	NewPassword string `json:"new_password" validate:"required"`
}

type UpdateAdminAnperRequest struct {
	CompanyID   uint   `json:"company_id" validate:"required"`
	BadgeNumber string `json:"badge_number" validate:"required"`
	Name        string `json:"name" validate:"required"`
	Jabatan     string `json:"jabatan" validate:"required"`
}
