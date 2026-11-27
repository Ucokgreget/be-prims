package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

func TestGetDashboardStatisticStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=dashboard_test"), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	var queries []string
	if err := db.Callback().Query().After("gorm:query").Register("test:dashboard", func(tx *gorm.DB) {
		sql := strings.ToLower(strings.NewReplacer(`"`, "", "[", "", "]", "").Replace(tx.Statement.SQL.String()))
		queries = append(queries, sql)
		switch dest := tx.Statement.Dest.(type) {
		case *int64:
			switch {
			case strings.Contains(sql, "from companies where status"):
				if len(tx.Statement.Vars) != 1 || tx.Statement.Vars[0] != "active" {
					t.Fatalf("unexpected company count filter: %v", tx.Statement.Vars)
				}
				*dest = 1
				tx.RowsAffected = 1
			case strings.Contains(sql, "sum(cast(file_size as bigint))"):
				*dest = 3_000_000_000
			case strings.Contains(sql, "sum(cast(storage_quota_gb as bigint))"):
				*dest = 75
			}
		case *[]dashboardCompanyStorage:
			*dest = []dashboardCompanyStorage{
				{ID: 1, Name: "Anper A", Code: "A", StorageQuotaGB: 50, UsedBytes: 3_000_000_000},
				{ID: 2, Name: "Anper B", Code: "B", StorageQuotaGB: 25},
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/dashboard", GetDashboardStatistic(db))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	var body struct {
		Data struct {
			TotalCompanies int64 `json:"total_companies"`
			StorageUsage   struct {
				UsedBytes    int64 `json:"used_bytes"`
				TotalQuotaGB int64 `json:"total_quota_gb"`
			} `json:"storage_usage"`
			CompaniesStorage []dashboardCompanyStorage `json:"companies_storage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, decode error = %v", rec.Code, rec.Body, err)
	}
	data := body.Data
	if data.TotalCompanies != 1 || data.StorageUsage.UsedBytes != 3_000_000_000 || data.StorageUsage.TotalQuotaGB != 75 ||
		len(data.CompaniesStorage) != 2 || data.CompaniesStorage[0].UsedBytes != 3_000_000_000 || data.CompaniesStorage[1].UsedBytes != 0 {
		t.Fatalf("unexpected dashboard response: %s", rec.Body)
	}
	if len(queries) != 6 ||
		queries[3] != "select coalesce(sum(cast(file_size as bigint)), 0) from attachments" ||
		queries[4] != "select coalesce(sum(cast(storage_quota_gb as bigint)), 0) from companies" ||
		!strings.Contains(queries[5], "coalesce(sum(cast(a.file_size as bigint)), 0) as used_bytes from companies as c left join documents as d on d.company_id = c.id left join attachments as a on a.document_id = d.id group by c.id, c.name, c.code, c.storage_quota_gb") {
		t.Fatalf("unexpected dashboard queries: %v", queries)
	}
}
