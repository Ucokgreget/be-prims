package request

type LoginRequest struct {
	NPP         string `json:"npp"`
	BadgeNumber string `json:"badge_number"`
	Password    string `json:"password"`
}
