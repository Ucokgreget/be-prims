package request

type LoginRequest struct {
	BadgeNumber string `json:"badge_number" validate:"required"`
	Password    string `json:"password" validate:"required"`
}
