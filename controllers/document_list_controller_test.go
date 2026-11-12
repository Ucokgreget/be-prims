package controllers

import (
	"context"
	"log"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sqlRecorder: logger GORM yang menyimpan setiap SQL yang dieksekusi beserta
// variabelnya. Dipakai karena pada mode DryRun GORM tidak me-reset
// Statement.SQL antar eksekusi dalam satu session, sehingga callback Row/Query
// tidak bisa diandalkan untuk membaca SQL query kedua dan seterusnya.
type sqlRecorder struct {
	mu       sync.Mutex
	queries  []string
	withVars []string
}

func (r *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return r }

func (r *sqlRecorder) Info(context.Context, string, ...any) {}

func (r *sqlRecorder) Warn(context.Context, string, ...any) {}

func (r *sqlRecorder) Error(context.Context, string, ...any) {}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, sql)
}

func (r *sqlRecorder) find(substr string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.queries {
		if strings.Contains(strings.ToUpper(q), substr) {
			return q, true
		}
	}
	return "", false
}

// TestGetAllDocumentsVisitorsCountsViewOnly memastikan counter `visitors`
// (US-10-BE AC: aktivitas baca menaikkan angka visitors) hanya menghitung
// action VIEW dan tetap tenant-scoped.
//
// Batasan: DryRun GORM tidak bisa men-scan baris (ErrDryRunModeUnsupported),
// jadi kontrak yang diverifikasi adalah SQL yang dibangun, bukan status HTTP.
// Verifikasi end-to-end butuh SQL Server hidup (pola DryRun sama dengan
// category_controller_test.go).
func TestGetAllDocumentsVisitorsCountsViewOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "document-list-test-only-secret")

	rec := &sqlRecorder{}
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=document_list_test"), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Pada DryRun GORM tidak me-reset Statement.SQL setelah eksekusi (di mode
	// normal SQL di-reset). Akibatnya query kedua dalam satu handler (Count lalu
	// Scan) akan memakai SQL query pertama. Shim ini mengembalikan perilaku
	// produksi: bersihkan statement sebelum tiap eksekusi.
	resetStatement := func(tx *gorm.DB) {
		tx.Statement.SQL.Reset()
		tx.Statement.Vars = nil
	}
	if err := db.Callback().Query().Before("gorm:query").Register("test:reset", resetStatement); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().Before("gorm:row").Register("test:reset", resetStatement); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/documents", middleware.AuthMiddleware(), GetAllDocuments(db))

	req := httptest.NewRequest("GET", "/documents", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken(t, 1, models.RoleAdminAnper))
	router.ServeHTTP(httptest.NewRecorder(), req)

	visitorsSQL, ok := rec.find("AS VISITORS")
	if !ok {
		t.Fatalf("subquery visitors tidak pernah dibangun; queries=%v", rec.queries)
	}

	sql := strings.ToUpper(visitorsSQL)
	if !strings.Contains(sql, "COUNT(DISTINCT L.USER_ID)") {
		t.Fatalf("visitors wajib COUNT(DISTINCT user_id): %s", visitorsSQL)
	}
	if !strings.Contains(sql, "L.ACTION = 'VIEW'") {
		t.Fatalf("visitors wajib difilter action='VIEW' agar download tidak terhitung: %s", visitorsSQL)
	}
	if !strings.Contains(sql, "DOCUMENTS.COMPANY_ID") {
		t.Fatalf("query wajib tenant-scoped dari JWT: %s", visitorsSQL)
	}

	log.Printf("visitors SQL: %s", visitorsSQL)
}
