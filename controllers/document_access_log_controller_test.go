package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"be-prims/middleware"
	"be-prims/models"
)

// accessLogFixture: data yang dikembalikan resolver DryRun untuk tiap query.
type accessLogFixture struct {
	documentFound bool
	readers       []accessLogReader
	rows          []accessLogRow
}

// capturedAccessLogSQL menyimpan SQL + vars dari query readers & access_logs.
type capturedAccessLogSQL struct {
	documentSQL, readersSQL, logsSQL    string
	documentVars, readersVars, logsVars []any
}

func newAccessLogTestDB(t *testing.T, fx accessLogFixture) (*gorm.DB, *capturedAccessLogSQL) {
	t.Helper()
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=access_log_test"), &gorm.Config{
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

	got := &capturedAccessLogSQL{}
	if err := db.Callback().Query().After("gorm:query").Register("test:access_logs", func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		vars := append([]any(nil), tx.Statement.Vars...)
		switch dest := tx.Statement.Dest.(type) {
		case *models.Document:
			got.documentSQL, got.documentVars = sql, vars
			if !fx.documentFound {
				tx.AddError(gorm.ErrRecordNotFound)
				return
			}
			*dest = models.Document{
				Id: 15, UUID: "7c8e9f10-1234-4567-89ab-cdef01234567",
				DocumentNumber: "SK/DIR/651/2026", Title: "IT Steering Comitee (ITSC) PT Sri Melamin Rejeki",
			}
			tx.RowsAffected = 1
		case *[]accessLogReader:
			got.readersSQL, got.readersVars = sql, vars
			*dest = append(*dest, fx.readers...)
			tx.RowsAffected = int64(len(fx.readers))
		case *[]accessLogRow:
			got.logsSQL, got.logsVars = sql, vars
			*dest = append(*dest, fx.rows...)
			tx.RowsAffected = int64(len(fx.rows))
		}
	}); err != nil {
		t.Fatal(err)
	}
	return db, got
}

func newAccessLogRouter(db *gorm.DB) *gin.Engine {
	router := gin.New()
	router.GET("/documents/:id/access-logs",
		middleware.AuthMiddleware(), middleware.RequireRole(models.RoleAdminAnper), GetDocumentAccessLogs(db))
	return router
}

func getAccessLogs(t *testing.T, router *gin.Engine, id, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/documents/"+id+"/access-logs", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken(t, 1, role))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestGetDocumentAccessLogsRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "access-log-test-only-secret")

	db, got := newAccessLogTestDB(t, accessLogFixture{documentFound: true})
	rec := getAccessLogs(t, newAccessLogRouter(db), "15", models.RoleKaryawan)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("KARYAWAN: status = %d, want 403", rec.Code)
	}
	if got.documentSQL != "" {
		t.Fatal("request KARYAWAN tidak boleh sampai ke query dokumen")
	}
}

func TestGetDocumentAccessLogsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "access-log-test-only-secret")

	t.Run("dokumen-anper-lain", func(t *testing.T) {
		db, got := newAccessLogTestDB(t, accessLogFixture{documentFound: false})
		rec := getAccessLogs(t, newAccessLogRouter(db), "99", models.RoleAdminAnper)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Dokumen tidak ditemukan") {
			t.Fatalf("status = %d body=%s, want 404 Dokumen tidak ditemukan", rec.Code, rec.Body.String())
		}
		// Isolasi tenant: company_id dari JWT (11) wajib ikut di WHERE.
		if !strings.Contains(got.documentSQL, "company_id") || !containsVar(got.documentVars, uint(11)) {
			t.Fatalf("query dokumen tanpa filter company_id JWT: %s vars=%v", got.documentSQL, got.documentVars)
		}
		if got.readersSQL != "" || got.logsSQL != "" {
			t.Fatal("log tidak boleh di-query bila dokumen bukan milik anper")
		}
	})

	t.Run("id-tidak-valid", func(t *testing.T) {
		db, got := newAccessLogTestDB(t, accessLogFixture{documentFound: true})
		rec := getAccessLogs(t, newAccessLogRouter(db), "abc", models.RoleAdminAnper)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if got.documentSQL != "" {
			t.Fatal("id tidak valid tidak boleh sampai ke DB")
		}
	})
}

func TestGetDocumentAccessLogsSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "access-log-test-only-secret")

	wib := time.FixedZone("WIB", 7*3600)
	viewed := time.Date(2026, 11, 12, 7, 15, 30, 0, wib)
	downloaded := time.Date(2026, 11, 12, 7, 20, 10, 0, wib)
	viewed2 := time.Date(2026, 11, 12, 7, 18, 0, 0, wib)

	db, got := newAccessLogTestDB(t, accessLogFixture{
		documentFound: true,
		readers: []accessLogReader{
			{Id: 3, Name: "XAIVIERA PUTRI", BadgeNumber: "1002", Jabatan: "Staf SDM"},
			{Id: 4, Name: "Ahmad Widodo santoso", BadgeNumber: "1003", Jabatan: "Staf Keuangan"},
		},
		rows: []accessLogRow{
			{UserID: 3, Name: "XAIVIERA PUTRI", BadgeNumber: "1002", IpAddress: "192.168.1.45",
				ViewedAt: &viewed, DownloadedAt: &downloaded},
			{UserID: 4, Name: "Ahmad Widodo santoso", BadgeNumber: "1003", IpAddress: "192.168.1.52",
				ViewedAt: &viewed2},
		},
	})

	rec := getAccessLogs(t, newAccessLogRouter(db), "15", models.RoleAdminAnper)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" || body.Message != "Riwayat akses dokumen berhasil diambil" {
		t.Fatalf("envelope salah: %+v", body)
	}

	var data map[string]any
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatal(err)
	}
	doc := data["document"].(map[string]any)
	if doc["id"].(float64) != 15 || doc["uuid"] != "7c8e9f10-1234-4567-89ab-cdef01234567" ||
		doc["document_number"] != "SK/DIR/651/2026" || doc["title"] == "" {
		t.Fatalf("document salah: %v", doc)
	}

	readers := data["readers"].([]any)
	if len(readers) != 2 || readers[0].(map[string]any)["jabatan"] != "Staf SDM" {
		t.Fatalf("readers salah: %v", readers)
	}

	logs := data["access_logs"].([]any)
	if len(logs) != 2 {
		t.Fatalf("access_logs = %d, want 2", len(logs))
	}
	first := logs[0].(map[string]any)
	if first["user_id"].(float64) != 3 || first["ip_address"] != "192.168.1.45" ||
		first["viewed_at"] != "2026-11-12T00:15:30Z" || first["downloaded_at"] != "2026-11-12T00:20:10Z" {
		t.Fatalf("baris pertama salah (timestamp wajib UTC Z): %v", first)
	}
	second := logs[1].(map[string]any)
	if v, ok := second["downloaded_at"]; !ok || v != nil {
		t.Fatalf("karyawan yang belum mengunduh wajib downloaded_at: null, got %v", second)
	}

	// Kontrak SQL: dibatasi ke dokumen ini, action eksplisit, IP dari akses
	// terakhir (bukan MAX leksikografis).
	if !containsVar(got.readersVars, uint(15)) || !containsVar(got.readersVars, "VIEW") {
		t.Fatalf("readers query vars = %v", got.readersVars)
	}
	if !containsVar(got.logsVars, uint(15)) || !containsVar(got.logsVars, "DOWNLOAD") {
		t.Fatalf("access_logs query vars = %v", got.logsVars)
	}
	upper := strings.ToUpper(got.logsSQL)
	if !strings.Contains(upper, "OUTER APPLY") || strings.Contains(upper, "MAX(CASE WHEN L.ACTION = @P1 THEN L.IP_ADDRESS") ||
		strings.Contains(upper, "MAX(L.IP_ADDRESS") {
		t.Fatalf("ip_address harus dari akses terakhir, bukan MAX(ip): %s", got.logsSQL)
	}
}

func TestGetDocumentAccessLogsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "access-log-test-only-secret")

	db, _ := newAccessLogTestDB(t, accessLogFixture{documentFound: true})
	rec := getAccessLogs(t, newAccessLogRouter(db), "15", models.RoleAdminAnper)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"readers":[]`) || !strings.Contains(rec.Body.String(), `"access_logs":[]`) {
		t.Fatalf("dokumen tanpa akses wajib readers: [] & access_logs: [] (bukan null): %s", rec.Body.String())
	}
}

func containsVar(vars []any, want any) bool {
	for _, v := range vars {
		if v == want {
			return true
		}
	}
	return false
}
