package database

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// MigrateFresh menghapus seluruh data; jangan panggil otomatis saat aplikasi mulai.
func MigrateFresh(db *gorm.DB) error {
	modelList := MigrateModels()

	if err := db.Transaction(func(tx *gorm.DB) error {
		log.Println("Dropping all tables...")
		if err := tx.Migrator().DropTable(DropModels()...); err != nil {
			return fmt.Errorf("drop tables: %w", err)
		}

		log.Println("Re-creating tables...")
		if err := tx.AutoMigrate(modelList...); err != nil {
			return fmt.Errorf("create tables: %w", err)
		}

		log.Println("Seeding data...")
		if err := Seed(tx); err != nil {
			return fmt.Errorf("seed data: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	log.Println("migrate-fresh selesai.")
	return nil
}
