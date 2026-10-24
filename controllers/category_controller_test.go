package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCategoryTenantIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "category-test-only-secret")
	const body = `{"name":"Kategori","description":"Deskripsi","company_id":999}`
	cases := []struct {
		name, method, path, body string
		handler                  func(*gorm.DB) gin.HandlerFunc
		queryErr                 error
		status, queries, writes  int
	}{
		{"list", "GET", "/category", "", GetAllCategory, nil, 200, 1, 0},
		{"detail", "GET", "/category/3", "", GetCategoryById, nil, 200, 1, 0},
		{"create", "POST", "/category", body, CreateCategory, gorm.ErrRecordNotFound, 200, 1, 1},
		{"update", "PATCH", "/category/3", body, UpdateCategory, nil, 200, 1, 1},
		{"delete", "DELETE", "/category/3", "", DeleteCategory, nil, 200, 1, 1},
		{"duplicate", "POST", "/category", body, CreateCategory, nil, 409, 1, 0},
		{"create-query-error", "POST", "/category", body, CreateCategory, errors.New("database unavailable"), 500, 1, 0},
		{"detail-query-error", "GET", "/category/3", "", GetCategoryById, errors.New("database unavailable"), 500, 1, 0},
		{"detail-not-found", "GET", "/category/3", "", GetCategoryById, gorm.ErrRecordNotFound, 404, 1, 0},
		{"update-not-found", "PATCH", "/category/3", body, UpdateCategory, gorm.ErrRecordNotFound, 404, 1, 0},
		{"delete-not-found", "DELETE", "/category/3", "", DeleteCategory, gorm.ErrRecordNotFound, 404, 1, 0},
		{"create-invalid-body", "POST", "/category", `{}`, CreateCategory, nil, 400, 0, 0},
		{"update-invalid-body", "PATCH", "/category/3", `{}`, UpdateCategory, nil, 400, 0, 0},
		{"detail-invalid-id", "GET", "/category/1%20OR%201=1", "", GetCategoryById, nil, 400, 0, 0},
		{"update-zero-id", "PATCH", "/category/0", body, UpdateCategory, nil, 400, 0, 0},
		{"delete-negative-id", "DELETE", "/category/-1", "", DeleteCategory, nil, 400, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ponytail: DryRun checks SQL and handler flow, not a live SQL Server; add integration tests before deployment.
			db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=category_test"), &gorm.Config{
				DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })

			var companyID uint
			var queries, writes int
			assertTenant := func(tx *gorm.DB) {
				sql := tx.Statement.SQL.String()
				where := strings.Index(sql, " WHERE ")
				if where >= 0 {
					for i, value := range tx.Statement.Vars {
						if id, ok := value.(uint); ok && id == companyID &&
							strings.Contains(sql[where:], fmt.Sprintf("company_id = @p%d", i+1)) {
							return
						}
					}
				}
				t.Fatalf("missing tenant %d filter: %s; vars=%v", companyID, sql, tx.Statement.Vars)
			}
			query := func(tx *gorm.DB) {
				queries++
				assertTenant(tx)
				if tc.queryErr != nil {
					tx.AddError(tc.queryErr)
					return
				}
				category := models.Category{Id: 3, Name: "Kategori", Description: "Deskripsi", CompanyID: companyID}
				switch dest := tx.Statement.Dest.(type) {
				case *models.Category:
					*dest = category
				case *[]models.Category:
					*dest = []models.Category{category}
				}
				tx.RowsAffected = 1
			}
			write := func(tx *gorm.DB) {
				writes++
				if !strings.HasPrefix(tx.Statement.SQL.String(), "INSERT") {
					assertTenant(tx)
				}
				if got := tx.Statement.Dest.(*models.Category).CompanyID; got != companyID {
					t.Fatalf("company_id = %d, want JWT company %d", got, companyID)
				}
				tx.RowsAffected = 1
			}
			for _, err := range []error{
				db.Callback().Query().After("gorm:query").Register("test:query", query),
				db.Callback().Create().After("gorm:create").Register("test:write", write),
				db.Callback().Update().After("gorm:update").Register("test:write", write),
				db.Callback().Delete().After("gorm:delete").Register("test:write", write),
			} {
				if err != nil {
					t.Fatal(err)
				}
			}

			router := gin.New()
			router.Use(middleware.AuthMiddleware(), middleware.RequireRole(models.RoleAdminAnper))
			router.Handle(tc.method, "/category", tc.handler(db))
			router.Handle(tc.method, "/category/:id", tc.handler(db))
			// Reuse the same DB and router for different tenants to catch scope leaks.
			for _, id := range []uint{11, 22} {
				companyID, queries, writes = id, 0, 0
				token, _, err := middleware.GenerateToken(middleware.AuthClaims{CompanyID: id, Role: models.RoleAdminAnper})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				if response.Code != tc.status || queries != tc.queries || writes != tc.writes {
					t.Fatalf("tenant %d: status/queries/writes = %d/%d/%d, want %d/%d/%d; body=%s",
						id, response.Code, queries, writes, tc.status, tc.queries, tc.writes, response.Body.String())
				}
			}
		})
	}
}
