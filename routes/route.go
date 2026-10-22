package routes

import (
	"be-prims/controllers"
	"be-prims/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(router *gin.Engine, db *gorm.DB) {
	api := router.Group("/api/v1")

	auth := api.Group("/auth")
	auth.POST("/login", controllers.Login(db))
	auth.GET("/me", middleware.AuthMiddleware(), controllers.Me(db))
}
