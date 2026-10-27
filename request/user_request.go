package request

type CreateUserRequest struct {
	BadgeNumber  string `json:"badge_number" binding:"required,max=50,alphanum"`
	Name         string `json:"name" binding:"required,max=150"`
	Jabatan      string `json:"jabatan" binding:"required,max=100"`
	DepartmentID uint   `json:"id_department" binding:"required"`
	Role         string `json:"role" binding:"required,oneof=ADMIN_ANPER KARYAWAN"`
	Password     string `json:"password" binding:"required,min=8"`
}

type UpdateUserStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active inactive"`
}
