package main

import (
	"be-prims/database"
	"be-prims/routes"
	"be-prims/storage"
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	db := database.Connect()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate-fresh":
			if err := database.MigrateFresh(db); err != nil {
				log.Fatalf("migrate-fresh gagal: %v", err)
			}
			return
		case "seed":
			if err := database.Seed(db); err != nil {
				log.Fatalf("seed gagal: %v", err)
			}
			return
		}
	}

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("auto-migration gagal: %v", err)
	}

	if err := storage.Init(); err != nil {
		log.Fatalf("init storage gagal: %v", err)
	}

	router := gin.Default()
	routes.SetupRouter(router, db)

	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "Hello, World!",
		})
	})

	router.Run(":8080")
}
