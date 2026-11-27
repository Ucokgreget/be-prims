package routes

import (
	"be-prims/database"
	"be-prims/middleware"
	"be-prims/models"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAdminAnperManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "admin-anper-reset-test-secret")
	token := func(t *testing.T, actor uint, role string) string {
		t.Helper()
		signed, _, err := middleware.GenerateToken(middleware.AuthClaims{UserID: actor, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	call := func(t *testing.T, router *gin.Engine, bearer, path, body string, want int) []byte {
		t.Helper()
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/superadmin/anper-admins/"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("PATCH %s: status = %d, want %d: %s", path, w.Code, want, w.Body)
		}
		return w.Body.Bytes()
	}
	const body = `{"new_password":"password-baru"}`
	const inactive = `{"status":"inactive"}`
	superadmin := token(t, 7, models.RoleSuperAdmin)
	router := gin.New()
	SetupRouter(router, nil, nil)
	adminToken := token(t, 1, models.RoleAdminAnper)
	call(t, router, "", "1/reset-password", body, http.StatusUnauthorized)
	call(t, router, adminToken, "1/reset-password", body, http.StatusForbidden)
	call(t, router, superadmin, "0/reset-password", body, http.StatusBadRequest)
	call(t, router, superadmin, "1/reset-password", `{}`, http.StatusBadRequest)
	call(t, router, superadmin, "1/reset-password", fmt.Sprintf(`{"new_password":%q}`, strings.Repeat("é", 37)), http.StatusBadRequest)
	call(t, router, "", "1/status", inactive, http.StatusUnauthorized)
	call(t, router, adminToken, "1/status", inactive, http.StatusForbidden)
	call(t, router, superadmin, "0/status", inactive, http.StatusBadRequest)
	call(t, router, superadmin, "1/status", `{}`, http.StatusBadRequest)
	call(t, router, superadmin, "1/status", `{"status":"disabled"}`, http.StatusBadRequest)

	t.Run("SQL Server", func(t *testing.T) {
		dsn := os.Getenv("PRIMS_TEST_ADMIN_ANPER_SQLSERVER_URL")
		if dsn == "" {
			t.Skip("set PRIMS_TEST_ADMIN_ANPER_SQLSERVER_URL to a disposable empty SQL Server database")
		}
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		db, err := gorm.Open(sqlserver.Open(dsn), &gorm.Config{Logger: logger.Discard})
		must(err)
		sqlDB, err := db.DB()
		must(err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		must(database.AutoMigrate(db))
		company := models.Company{Name: "Test company", Code: "RESET"}
		must(db.Create(&company).Error)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("password-lama"), bcrypt.MinCost)
		must(err)
		admin := models.User{BadgeNumber: "test-admin", CompanyID: company.Id, Role: models.RoleAdminAnper, Password: string(oldHash)}
		other := models.User{BadgeNumber: "test-employee", CompanyID: company.Id, Role: models.RoleKaryawan, Password: string(oldHash)}
		must(db.Create(&admin).Error)
		must(db.Create(&other).Error)

		router := gin.New()
		SetupRouter(router, db, nil)
		t.Run("update status", func(t *testing.T) {
			call(t, router, superadmin, fmt.Sprintf("%d/status", other.Id), inactive, http.StatusNotFound)
			statusPath := fmt.Sprintf("%d/status", admin.Id)
			for _, status := range []string{models.StatusInactive, models.StatusInactive, models.StatusActive} {
				data := call(t, router, superadmin, statusPath, fmt.Sprintf(`{"status":%q}`, status), http.StatusOK)
				var response map[string]any
				if err := json.Unmarshal(data, &response); err != nil || len(response) != 2 ||
					response["success"] != true || response["message"] != "Status akun berhasil diperbarui." {
					t.Fatalf("unexpected status response: %s", data)
				}
				var account models.User
				if err := db.First(&account, admin.Id).Error; err != nil || account.Status != status {
					t.Fatalf("stored status = %q, want %q; error = %v", account.Status, status, err)
				}
			}
		})

		resetPath := fmt.Sprintf("%d/reset-password", admin.Id)
		call(t, router, superadmin, fmt.Sprintf("%d/reset-password", other.Id), body, http.StatusNotFound)
		call(t, router, superadmin, resetPath, body, http.StatusOK)
		var updated models.User
		must(db.First(&updated, admin.Id).Error)
		must(bcrypt.CompareHashAndPassword([]byte(updated.Password), []byte("password-baru")))

		must(db.Exec("ALTER TABLE audit_logs ADD CONSTRAINT ck_test_reject_actor CHECK (actor_id <> 8)").Error)
		call(t, router, token(t, 8, models.RoleSuperAdmin), resetPath, `{"new_password":"tidak-tersimpan"}`, http.StatusInternalServerError)
		var afterFailure, untouched models.User
		must(db.First(&afterFailure, admin.Id).Error)
		must(db.First(&untouched, other.Id).Error)
		if afterFailure.Password != updated.Password || untouched.Password != string(oldHash) || untouched.Status != models.StatusActive {
			t.Fatal("failed reset changed the password, or the employee account was modified")
		}
		var logs []models.AuditLog
		must(db.Find(&logs).Error)
		if len(logs) != 1 || logs[0].ActorID != 7 || logs[0].CreatedAt.IsZero() ||
			logs[0].Message != fmt.Sprintf("Superadmin mereset password Admin Anper ID %d", admin.Id) {
			t.Fatalf("unexpected audit logs: %+v", logs)
		}
	})
}
