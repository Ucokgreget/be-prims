package database

import (
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Connect() *gorm.DB {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatal("env gak ketemu")
	}

	dbUrl := os.Getenv("SQL_SERVER_URL")

	if dbUrl == "" {
		log.Fatal("SQL_SERVER_URL not set")
	}

	dbLogger := logger.New(
		log.New(os.Stdout, "", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
			ParameterizedQueries:      true,
		},
	)

	db, err := gorm.Open(sqlserver.Open(dbUrl), &gorm.Config{Logger: dbLogger})
	if err != nil {
		log.Fatal(err)
	}

	return db
}
