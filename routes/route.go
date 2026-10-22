package routes

import (
	"be-prims/controllers"
	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(router *gin.Engine, db *gorm.DB) {
	api := router.Group("/api/v1")

	auth := api.Group("/auth")
	auth.POST("/login", controllers.Login(db))
	auth.GET("/me", middleware.AuthMiddleware(), controllers.Me(db))

	// User management — ADMIN_ANPER only, tenant-scoped
	admin := api.Group("",
		middleware.AuthMiddleware(),
		middleware.RequireRole(models.RoleAdminAnper),
	)
	admin.GET("/users", controllers.ListUsers(db))
	admin.POST("/users", controllers.CreateUser(db))
	admin.PATCH("/users/:id/status", controllers.UpdateUserStatus(db))
	admin.GET("/departments", controllers.ListDepartments(db))
}
