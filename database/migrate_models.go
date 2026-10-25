package database

import (
	"be-prims/models"

	"gorm.io/gorm"
)

// MigrateModels mengembalikan daftar model untuk AutoMigrate.
// Urutan mengikuti ketergantungan foreign key agar migrasi berhasil.
func MigrateModels() []any {
	return []any{
		&models.Company{},
		&models.Department{}, &models.Category{}, &models.Tag{},
		&models.User{},
		&models.Document{}, &models.Attachment{}, &models.DocumentDepartment{}, &models.DocumentTag{},
		&models.DocumentShared{}, &models.DocumentAccessLog{},
	}
}

// DropModels mengembalikan model dalam urutan terbalik agar FK tidak menghalangi drop.
func DropModels() []any {
	models := MigrateModels()
	for i, j := 0, len(models)-1; i < j; i, j = i+1, j-1 {
		models[i], models[j] = models[j], models[i]
	}
	return models
}

// AutoMigrate menyiapkan skema tabel secara idempotent
// (hanya menambah kolom/index baru; tidak menghapus).
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(MigrateModels()...)
}
