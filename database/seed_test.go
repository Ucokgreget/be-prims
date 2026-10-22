package database

import (
	"be-prims/models"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Jalankan bagian integrasi dengan SEED_TEST_SQL_SERVER_URL; tabel sementara selalu di-rollback.
func TestSeed(t *testing.T) {
	t.Run("password validation", func(t *testing.T) {
		// Seed tidak lagi gagal saat SEED_PASSWORD invalid; memakai default dev.
		for _, password := range []string{"", "short", strings.Repeat("x", 73)} {
			t.Setenv("SEED_PASSWORD", password)
			if got := ResolveSeedPassword(); got != "Demo-priv-root-123" {
				t.Fatalf("ResolveSeedPassword(%q) = %q, want default", password, got)
			}
		}
		t.Setenv("SEED_PASSWORD", "Valid-test-password-abc")
		if got := ResolveSeedPassword(); got != "Valid-test-password-abc" {
			t.Fatalf("ResolveSeedPassword() = %q, want env value", got)
		}
	})

	t.Run("SQL Server", func(t *testing.T) {
		dsn := os.Getenv("SEED_TEST_SQL_SERVER_URL")
		if dsn == "" {
			t.Skip("set SEED_TEST_SQL_SERVER_URL untuk uji SQL Server")
		}
		t.Setenv("SEED_PASSWORD", "Only-for-isolated-seed-test")

		db, err := gorm.Open(sqlserver.Open(dsn), &gorm.Config{
			NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("seed_test_%d_", time.Now().UnixNano())},
			Logger:         logger.Default.LogMode(logger.Silent),
		})
		if err != nil {
			t.Fatalf("connect SQL Server: %v", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { sqlDB.Close() })

		tx := db.Begin()
		if tx.Error != nil {
			t.Fatal(tx.Error)
		}
		t.Cleanup(func() {
			if err := tx.Rollback().Error; err != nil {
				t.Errorf("rollback tabel sementara: %v", err)
			}
		})

		records := []struct {
			model any
			want  int64
		}{
			{&models.Company{}, 2},
			{&models.Department{}, 4}, {&models.Category{}, 4}, {&models.Tag{}, 3},
			{&models.User{}, 5}, {&models.Document{}, 4}, {&models.Attachment{}, 4},
			{&models.DocumentTag{}, 4}, {&models.DocumentShared{}, 2}, {&models.DocumentAccessLog{}, 3},
		}
		var modelList []any
		for _, record := range records {
			modelList = append(modelList, record.model)
		}

		// Tabel log belum dibuat: kegagalan terakhir harus membatalkan seluruh seed.
		if err := tx.AutoMigrate(modelList[:len(modelList)-1]...); err != nil {
			t.Fatal(err)
		}
		if err := Seed(tx); err == nil {
			t.Fatal("seed seharusnya gagal tanpa tabel access log")
		}
		for _, model := range modelList[:len(modelList)-1] {
			var count int64
			if err := tx.Model(model).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rollback %T: count=%d, error=%v", model, count, err)
			}
		}
		if err := tx.AutoMigrate(&models.DocumentAccessLog{}); err != nil {
			t.Fatal(err)
		}
		if err := Seed(tx); err != nil {
			t.Fatal(err)
		}

		for npp, role := range map[string]string{
			"1000": models.RoleSuperAdmin, "1001": models.RoleAdminAnper,
			"1002": models.RoleKaryawan, "1003": models.RoleKaryawan,
			"2001": models.RoleKaryawan,
		} {
			var user models.User
			if err := tx.Where(models.User{NPP: npp}).First(&user).Error; err != nil {
				t.Fatal(err)
			}
			if user.Role != role {
				t.Fatalf("role %s: got=%q, want=%q", npp, user.Role, role)
			}
		}

		var admin models.User
		if err := tx.Where(models.User{NPP: "1001"}).First(&admin).Error; err != nil {
			t.Fatal(err)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(ResolveSeedPassword())); err != nil {
			t.Fatalf("password seed bukan hash bcrypt yang benar: %v", err)
		}
		originalHash := admin.Password
		if err := tx.Model(&admin).Update("name", "Nama diubah manual").Error; err != nil {
			t.Fatal(err)
		}
		t.Setenv("SEED_PASSWORD", "Different-isolated-test-password")
		if err := Seed(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.First(&admin, admin.Id).Error; err != nil {
			t.Fatal(err)
		}
		if admin.Name != "Nama diubah manual" || admin.Password != originalHash {
			t.Fatal("seed ulang mengubah data atau password yang sudah ada")
		}

		// MigrateFresh dengan SEED_PASSWORD="" memakai default dev, jadi harus sukses.
		t.Setenv("SEED_PASSWORD", "")
		if err := MigrateFresh(tx); err != nil {
			t.Fatalf("migrate-fresh gagal meski memakai default: %v", err)
		}
		// Re-count setelah migrate-fresh memperbarui ulang data (idempoten).
		for _, record := range records {
			var count int64
			if err := tx.Model(record.model).Count(&count).Error; err != nil || count != record.want {
				t.Fatalf("%T: count=%d, want=%d, error=%v", record.model, count, record.want, err)
			}
		}

		var document models.Document
		if err := tx.Preload("Category").Preload("Department").Where(models.Document{
			UUID: "00000000-0000-4000-8000-000000000001",
		}).First(&document).Error; err != nil {
			t.Fatal(err)
		}
		if document.Category.Id == 0 || document.Department.Id == 0 {
			t.Fatal("relasi document tidak valid")
		}
		t.Log("31 record dari 10 model: role, seed ulang, hash, relasi, dan rollback berhasil")
	})
}
