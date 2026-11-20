package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestCompanyAuditLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "company-audit-test-only-secret")
	db, _ := newAccessLogTestDB(t, accessLogFixture{})
	at := time.Date(2026, 11, 12, 7, 30, 0, 0, time.FixedZone("WIB", 7*3600))
	type statement struct {
		sql  string
		vars []any
	}
	var queries []statement
	if err := db.Callback().Query().After("gorm:query").Register("test:company_audit", func(tx *gorm.DB) {
		queries = append(queries, statement{tx.Statement.SQL.String(), append([]any(nil), tx.Statement.Vars...)})
		if containsVar(tx.Statement.Vars, uint(11)) {
			switch dest := tx.Statement.Dest.(type) {
			case *companyAuditSummary:
				*dest = companyAuditSummary{TotalActivities: 2, TotalDownloads: 2}
			case *[]companyAuditLogItem:
				*dest = append(*dest, companyAuditLogItem{
					ID: 4, Action: "DOWNLOAD", AccessedAt: at, IPAddress: "192.0.2.8",
					User:     companyAuditUser{ID: 7, Name: new("Karyawan"), BadgeNumber: new("1002"), Jabatan: new("Staf")},
					Document: accessLogDocument{Id: 15, UUID: "doc-uuid", Title: "Arsip", DocumentNumber: "SK/15"},
				})
			}
			tx.RowsAffected = 1
		}
	}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/v1/admin/audit-logs", middleware.AuthMiddleware(), middleware.RequireRole(models.RoleAdminAnper), GetCompanyAuditLogs(db))
	get := func(query, role string, company uint) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit-logs"+query, nil)
		if role != "" {
			token, _, err := middleware.GenerateToken(middleware.AuthClaims{UserID: 1, CompanyID: company, Role: role})
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	for _, tc := range []struct {
		query, role string
		status      int
	}{
		{"", "", 401}, {"", models.RoleKaryawan, 403}, {"", models.RoleSuperAdmin, 403},
		{"?action=DELETE", models.RoleAdminAnper, 400},
		{"?start_date=2026-02-30", models.RoleAdminAnper, 400}, {"?start_date=2026-11-13&end_date=2026-11-12", models.RoleAdminAnper, 400},
		{"?limit=101", models.RoleAdminAnper, 400}, {"?page=0", models.RoleAdminAnper, 400},
	} {
		if res := get(tc.query, tc.role, 11); res.Code != tc.status || len(queries) != 0 {
			t.Fatalf("%s %s: status=%d queries=%d; want %d and no queries; %s", tc.role, tc.query, res.Code, len(queries), tc.status, res.Body)
		}
	}

	res := get("?company_id=99&start_date=2026-11-12&end_date=2026-11-12&action=DOWNLOAD&search=SK%2F15%25&page=2&limit=1", models.RoleAdminAnper, 11)
	if res.Code != 200 || len(queries) != 2 || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d queries=%+v body=%s", res.Code, queries, res.Body)
	}
	if !strings.Contains(queries[0].sql, "COUNT_BIG(*) AS total_activities") || !strings.Contains(queries[0].sql, "AS total_views") || !strings.Contains(queries[0].sql, "AS total_downloads") {
		t.Fatalf("missing metrics: %s", queries[0].sql)
	}
	for _, q := range queries {
		for _, clause := range []string{
			"d.company_id = @p", "l.action = @p", "l.accessed_at >= @p", "l.accessed_at < @p",
			"(u.name LIKE @p", "OR u.badge_number LIKE @p", "OR d.title LIKE @p", "OR d.document_number LIKE @p",
			"JOIN documents AS d ON d.id = l.document_id", "LEFT JOIN users AS u ON u.id = l.user_id AND u.company_id = d.company_id",
		} {
			if !strings.Contains(q.sql, clause) {
				t.Fatalf("missing %q: %s", clause, q.sql)
			}
		}
		for _, value := range []any{uint(11), "DOWNLOAD", "%SK/15~%%", time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC), time.Date(2026, 11, 12, 17, 0, 0, 0, time.UTC)} {
			if !containsVar(q.vars, value) {
				t.Fatalf("missing bound value %v: %s; %v", value, q.sql, q.vars)
			}
		}
	}
	for _, clause := range []string{
		"ORDER BY l.accessed_at DESC, l.id DESC", "OFFSET 1 ROWS FETCH NEXT 1 ROWS ONLY",
		"d.document_number AS document_document_number",
	} {
		if !strings.Contains(queries[1].sql, clause) {
			t.Fatalf("missing %q: %s", clause, queries[1].sql)
		}
	}
	var response struct {
		Status, Message string
		Data            struct {
			Summary companyAuditSummary
			Logs    []companyAuditLogItem
			Meta    struct {
				Page, Limit, Total int
				TotalPages         int `json:"total_pages"`
			}
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil || response.Status != "success" || response.Message != "Data audit log berhasil diambil" ||
		response.Data.Meta.Page != 2 || response.Data.Meta.Limit != 1 || response.Data.Meta.Total != 2 || response.Data.Meta.TotalPages != 2 ||
		response.Data.Summary != (companyAuditSummary{TotalActivities: 2, TotalDownloads: 2}) ||
		len(response.Data.Logs) != 1 || response.Data.Logs[0].User.ID != 7 || response.Data.Logs[0].Document.UUID != "doc-uuid" ||
		!strings.Contains(res.Body.String(), `"accessed_at":"2026-11-12T00:30:00Z"`) {
		t.Fatalf("unexpected response: %s (%v)", res.Body, err)
	}

	queries = nil
	res = get("?company_id=11&action=", models.RoleAdminAnper, 22)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"logs":[]`) || len(queries) != 2 ||
		strings.Contains(queries[1].sql, "l.action = @p") || !containsVar(queries[0].vars, uint(22)) || !containsVar(queries[1].vars, uint(22)) {
		t.Fatalf("tenant must come from JWT: status=%d queries=%+v body=%s", res.Code, queries, res.Body)
	}
}
