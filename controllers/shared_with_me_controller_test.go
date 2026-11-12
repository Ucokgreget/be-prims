package controllers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sharedWithMeTestDB: DryRun + callback, sama seperti category/detail controller test.
// Membuktikan kontrak query & status tanpa SQL Server hidup.
func sharedWithMeTestDB(t *testing.T) (*gorm.DB, func(*gorm.DB)) {
	t.Helper()
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=shared_test"), &gorm.Config{
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

	// capture mengembalikan closure berisi SQL terakhir yang dieksekusi.
	var statements []*gorm.DB
	if err := db.Callback().Query().After("gorm:query").Register("test:capture", func(tx *gorm.DB) {
		statements = append(statements, tx)
	}); err != nil {
		t.Fatal(err)
	}
	return db, func(*gorm.DB) {
		if len(statements) > 0 {
			t.Logf("SQL: %s; vars=%v", statements[len(statements)-1].Statement.SQL.String(),
				statements[len(statements)-1].Statement.Vars)
		}
	}
}

func TestSharedWithMeQueryContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "shared-with-me-test-only-secret")

	const (
		companyID = uint(11)
		userID    = uint(2)
	)

	cases := []struct {
		name         string
		path         string
		wantPage     int
		wantLimit    int
		wantQ        []string // LIKE patterns yang wajib muncul di vars
		wantCategory any
	}{
		{"defaults", "/portal/documents/shared-with-me", 1, 10, nil, nil},
		{"search", "/portal/documents/shared-with-me?q=SK%2FDIR", 1, 10, []string{"%SK/DIR%"}, nil},
		{"category", "/portal/documents/shared-with-me?category_id=3", 1, 10, nil, "3"},
		{"page2", "/portal/documents/shared-with-me?page=2&limit=5", 2, 5, nil, nil},
		{"limit-clamped", "/portal/documents/shared-with-me?limit=500", 1, 100, nil, nil},
		{"limit-invalid", "/portal/documents/shared-with-me?limit=0", 1, 10, nil, nil},
		{"page-invalid", "/portal/documents/shared-with-me?page=-4", 1, 10, nil, nil},
		{"search+category", "/portal/documents/shared-with-me?q=Evaluasi&category_id=7", 1, 10, []string{"%Evaluasi%"}, "7"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dump := sharedWithMeTestDB(t)

			var tenantOK, personalOK bool
			var likeVars []string
			var categoryVar any
			if err := db.Callback().Query().After("gorm:query").Register("test:assert", func(tx *gorm.DB) {
				sql := tx.Statement.SQL.String()
				if !strings.Contains(sql, "document_shareds") {
					return
				}
				// AC: hanya baris milik user yang login.
				personalOK = strings.Contains(sql, "document_shareds.user_id = @p1") && tx.Statement.Vars[0] == userID
				// AC: isolasi tenant dari JWT, bukan dari input klien.
				tenantOK = strings.Contains(sql, "documents.company_id = @p2") && tx.Statement.Vars[1] == companyID
				likeVars, categoryVar = nil, nil
				for _, v := range tx.Statement.Vars {
					switch value := v.(type) {
					case string:
						if strings.HasPrefix(value, "%") {
							likeVars = append(likeVars, value)
							continue
						}
						categoryVar = value
					}
				}
			}); err != nil {
				t.Fatal(err)
			}

			token, _, err := middleware.GenerateToken(middleware.AuthClaims{
				UserID: userID, CompanyID: companyID, Role: models.RoleKaryawan,
			})
			if err != nil {
				t.Fatal(err)
			}

			router := gin.New()
			router.Use(middleware.AuthMiddleware())
			router.GET("/portal/documents/shared-with-me", GetSharedWithMeDocuments(db))

			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			dump(db)

			if response.Code != 200 {
				t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			if !personalOK {
				t.Fatalf("query must filter document_shareds.user_id by JWT user %d", userID)
			}
			if !tenantOK {
				t.Fatalf("query must filter documents.company_id by JWT company %d", companyID)
			}

			var body struct {
				Status  string           `json:"status"`
				Message string           `json:"message"`
				Data    json.RawMessage  `json:"data"`
				Meta    sharedWithMeMeta `json:"meta"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Status != "success" {
				t.Fatalf("status payload = %q", body.Status)
			}
			if body.Message != "Daftar dokumen Shared With Me berhasil diambil" {
				t.Fatalf("message = %q", body.Message)
			}
			// AC: kosong bukan error — array kosong, bukan null.
			if string(body.Data) != "[]" {
				t.Fatalf("data = %s, want []", body.Data)
			}
			if body.Meta.Total != 0 {
				t.Fatalf("total = %d, want 0", body.Meta.Total)
			}
			if body.Meta.Page != tc.wantPage || body.Meta.Limit != tc.wantLimit {
				t.Fatalf("meta page/limit = %d/%d, want %d/%d",
					body.Meta.Page, body.Meta.Limit, tc.wantPage, tc.wantLimit)
			}
			// q dipakai dua kali dalam satu statement (title OR document_number).
			if len(likeVars) != 2*len(tc.wantQ) {
				t.Fatalf("LIKE vars = %v, want %d entri", likeVars, 2*len(tc.wantQ))
			}
			for i, want := range tc.wantQ {
				if likeVars[2*i] != want || likeVars[2*i+1] != want {
					t.Fatalf("LIKE vars = %v, want %q twice", likeVars, want)
				}
			}
			if tc.wantCategory != nil && categoryVar != tc.wantCategory {
				t.Fatalf("category var = %v, want %v", categoryVar, tc.wantCategory)
			}
		})
	}
}

// TestSharedWithMeRequiresToken: AC — wajib Bearer token valid.
func TestSharedWithMeRequiresToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "shared-with-me-test-only-secret")

	db, _ := sharedWithMeTestDB(t)
	router := gin.New()
	router.Use(middleware.AuthMiddleware())
	router.GET("/portal/documents/shared-with-me", GetSharedWithMeDocuments(db))

	for _, tc := range []struct{ name, header string }{
		{"no-header", ""},
		{"wrong-scheme", "Basic abc"},
		{"garbage-token", "Bearer not-a-jwt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/portal/documents/shared-with-me", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != 401 {
				t.Fatalf("status = %d, want 401; body=%s", response.Code, response.Body.String())
			}
		})
	}
}
