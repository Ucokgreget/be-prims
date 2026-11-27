package routes

import (
	"be-prims/middleware"
	"be-prims/models"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

func TestGlobalAuditLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "global-audit-test-secret")
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=global_audit_test"), &gorm.Config{
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

	type query struct {
		sql  string
		vars []any
	}
	var queries []query
	if err := db.Callback().Query().After("gorm:query").Register("test:global_audit", func(tx *gorm.DB) {
		sql := strings.ToLower(strings.Join(strings.Fields(tx.Statement.SQL.String()), " "))
		queries = append(queries, query{sql, append([]any(nil), tx.Statement.Vars...)})
		if total, ok := tx.Statement.Dest.(*int64); ok {
			*total = 11
		}
	}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	SetupRouter(router, db, nil)
	call := func(role, params string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/superadmin/audit-logs"+params, nil)
		if role != "" {
			token, _, err := middleware.GenerateToken(middleware.AuthClaims{Role: role, CompanyID: 99})
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("role=%q params=%q: got %d, want %d: %s", role, params, rec.Code, wantStatus, rec.Body)
		}
		return rec
	}
	call("", "", http.StatusUnauthorized)
	call(models.RoleAdminAnper, "", http.StatusForbidden)
	for _, invalid := range []string{"?company_id=-1", "?action=DELETE", "?start_date=2026-11-28&end_date=2026-11-27", "?limit=101"} {
		call(models.RoleSuperAdmin, invalid, http.StatusBadRequest)
	}
	if len(queries) != 0 {
		t.Fatalf("unexpected queries for rejected requests: %v", queries)
	}

	rec := call(models.RoleSuperAdmin, "?company_id=7&action=DOWNLOAD&start_date=2026-11-27&end_date=2026-11-27&search=A%25&page=2", http.StatusOK)
	var body struct {
		Data struct {
			Logs []json.RawMessage
			Meta struct {
				Page       int
				Limit      int
				Total      int
				TotalPages int `json:"total_pages"`
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if m := body.Data.Meta; body.Data.Logs == nil || m.Page != 2 || m.Limit != 10 || m.Total != 11 || m.TotalPages != 2 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %s", rec.Body)
	}

	from := time.Date(2026, 11, 26, 17, 0, 0, 0, time.UTC)
	wantVars := []any{int64(7), "DOWNLOAD", "%A~%%", "%A~%%", from, from.Add(24 * time.Hour)}
	if len(queries) != 2 || !strings.HasPrefix(queries[0].sql, "select count_big(*) from ") || strings.Contains(queries[0].sql, "offset") ||
		!strings.Contains(queries[1].sql, "al.accessed_at as created_at, al.action as action_type") ||
		!strings.Contains(queries[1].sql, "order by al.accessed_at desc, al.id desc offset 10 rows fetch next 10 rows only") {
		t.Fatalf("unexpected queries: %v", queries)
	}
	for _, q := range queries {
		if !reflect.DeepEqual(q.vars, wantVars) ||
			!strings.Contains(q.sql, "join companies as c on c.id = u.company_id left join documents as d on d.id = al.document_id") ||
			!strings.Contains(q.sql, "(lower(u.name) like lower(") || !strings.Contains(q.sql, "or lower(d.title) like lower(") ||
			!strings.Contains(q.sql, "al.accessed_at >=") || !strings.Contains(q.sql, "al.accessed_at < ") {
			t.Fatalf("unexpected filters: %s, %v", q.sql, q.vars)
		}
	}
}

func TestGlobalAuditLogsSQLServer(t *testing.T) {
	dsn := os.Getenv("PRIMS_TEST_SQLSERVER_URL")
	if dsn == "" {
		t.Skip("set PRIMS_TEST_SQLSERVER_URL to an empty, disposable SQL Server test database")
	}
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "global-audit-integration-secret")
	db, err := gorm.Open(sqlserver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	tx := db.WithContext(t.Context()).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback audit fixture: %v", err)
		}
	}()

	for _, statement := range []string{
		`CREATE TABLE companies (id bigint PRIMARY KEY, name nvarchar(150), code nvarchar(50));
		 CREATE TABLE users (id bigint PRIMARY KEY, company_id bigint, name nvarchar(150), badge_number nvarchar(50));
		 CREATE TABLE documents (id bigint PRIMARY KEY, title nvarchar(255), document_number nvarchar(50));
		 CREATE TABLE document_access_logs (id bigint PRIMARY KEY, user_id bigint, document_id bigint, action nvarchar(20), accessed_at datetimeoffset, ip_address nvarchar(45));`,
		`INSERT INTO companies VALUES (7, N'Anper A', N'A'), (8, N'Anper B', N'B');
		 INSERT INTO users VALUES (1, 7, N'Ayu', N'A01'), (2, 8, N'Ayu', N'B01');
		 INSERT INTO documents VALUES (3, N'Panduan', N'D03');
		 INSERT INTO document_access_logs VALUES
		 (10, 1, 3, N'DOWNLOAD', '2026-11-27T16:59:59+00:00', N'192.0.2.1'),
		 (11, 1, 4, N'DOWNLOAD', '2026-11-27T16:59:59+00:00', N'192.0.2.1'),
		 (12, 1, 3, N'DOWNLOAD', '2026-11-27T17:00:00+00:00', N'192.0.2.1'),
		 (13, 2, 3, N'DOWNLOAD', '2026-11-27T16:59:59+00:00', N'192.0.2.2'),
		 (14, 1, 3, N'VIEW', '2026-11-27T16:59:59+00:00', N'192.0.2.1');`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}

	router := gin.New()
	SetupRouter(router, tx, nil)
	token, _, err := middleware.GenerateToken(middleware.AuthClaims{Role: models.RoleSuperAdmin})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		id                  float64
		docTitle, docNumber any
	}{{11, nil, nil}, {10, "Panduan", "D03"}} {
		page := i + 1
		req := httptest.NewRequest(http.MethodGet, "/api/v1/superadmin/audit-logs?company_id=7&action=DOWNLOAD&start_date=2026-11-27&end_date=2026-11-27&search=aYU&limit=1&page="+strconv.Itoa(page), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var body struct {
			Data struct {
				Logs []map[string]any
				Meta map[string]int
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("page %d: status=%d body=%s decode error=%v", page, rec.Code, rec.Body, err)
		}
		wantLogs := []map[string]any{{
			"id": want.id, "created_at": "2026-11-27T16:59:59Z", "action_type": "DOWNLOAD", "ip_address": "192.0.2.1",
			"company_name": "Anper A", "company_code": "A", "user_name": "Ayu", "badge_number": "A01",
			"document_title": want.docTitle, "document_number": want.docNumber,
		}}
		wantMeta := map[string]int{"page": page, "limit": 1, "total": 2, "total_pages": 2}
		if !reflect.DeepEqual(body.Data.Logs, wantLogs) || !reflect.DeepEqual(body.Data.Meta, wantMeta) {
			t.Fatalf("page %d: unexpected response: %s", page, rec.Body)
		}
	}
}
