package controllers

import (
	"be-prims/models"
	"be-prims/request"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var company []models.Company

		if err := db.Find(&company).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}
		c.JSON(http.StatusOK, company)
	}
}

func GetCompanyByID(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		var company models.Company

		if err := db.First(&company, id).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Company not found",
			})
			return
		}
		c.JSON(http.StatusOK, company)
	}
}

func CreateCompany(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input request.CreateCompanyRequest

		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}

		if err := validate.Struct(&input); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Invalid input",
			})
			return
		}

		company := models.Company{
			Name: input.Name,
			Code: input.Code,
		}

		if err := db.Create(&company).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}
		c.JSON(http.StatusOK, input)
	}
}
