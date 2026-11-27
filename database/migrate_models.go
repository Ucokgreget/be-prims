package database

import (
	"be-prims/models"
	"fmt"

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
		&models.DocumentShared{}, &models.DocumentAccessLog{}, &models.AuditLog{},
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

// AutoMigrate menyiapkan skema secara idempotent tanpa menghapus data.
func AutoMigrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(MigrateModels()...); err != nil {
			return err
		}
		// Unik case-insensitive bahkan pada database dengan collation case-sensitive.
		// GORM tidak mengubah NULL menjadi NOT NULL pada kolom lama.
		err := tx.Exec(`
IF COLUMNPROPERTY(OBJECT_ID('companies'), 'name', 'AllowsNull') = 1
    ALTER TABLE companies ALTER COLUMN name nvarchar(150) NOT NULL;
IF EXISTS (SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('companies') AND name = 'code'
    AND (is_nullable = 1 OR collation_name <> 'Latin1_General_100_CI_AS'))
BEGIN
    DROP INDEX idx_companies_code ON companies;
    ALTER TABLE companies ALTER COLUMN code nvarchar(50) COLLATE Latin1_General_100_CI_AS NOT NULL;
    CREATE UNIQUE INDEX idx_companies_code ON companies (code);
END`).Error
		if err != nil {
			return fmt.Errorf("migrasi nama/kode perusahaan (periksa NULL dan duplikat kode tanpa membedakan huruf besar/kecil): %w", err)
		}
		return nil
	})
}
