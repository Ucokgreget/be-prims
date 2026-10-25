package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"be-prims/models"
)

func tenantTestDB(t *testing.T) (*gorm.DB, *string) {
	t.Helper()
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=tenant_test"), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, Logger: logger.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	if err := db.Callback().Query().After("gorm:query").Register("test:capture", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
	}); err != nil {
		t.Fatal(err)
	}
	return db, &sql
}

func tenantContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(ClaimsKey, &AuthClaims{CompanyID: 7})
	return c
}

func TestTenantDBScopesDestWithoutModel(t *testing.T) {
	db, sql := tenantTestDB(t)
	var categories []models.Category
	TenantDB(tenantContext(), db).Find(&categories) // tanpa .Model()
	if !strings.Contains(*sql, "company_id = @p1") {
		t.Fatalf("tenant filter missing on Dest-only query: %s", *sql)
	}
}

func TestTenantDBSkipsModelWithoutCompanyID(t *testing.T) {
	db, sql := tenantTestDB(t)
	var tags []models.Tag
	TenantDB(tenantContext(), db).Find(&tags)
	if strings.Contains(*sql, "company_id") {
		t.Fatalf("model without company_id should not be filtered: %s", *sql)
	}
}
