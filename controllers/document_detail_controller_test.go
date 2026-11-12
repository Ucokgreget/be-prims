package controllers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"be-prims/middleware"
	"be-prims/models"
)

// ponytail: DryRun + callback = kontrak query & status, bukan SQL Server hidup.
// Ganti dengan test integrasi kalau sudah ada DB nyata di CI.
func TestPortalDocumentDetailAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "detail-test-only-secret")

	const (
		companyID    = uint(11)
		adminID      = uint(1)
		karyawanID   = uint(2)
		ownerID      = uint(3)
		publicUUID   = "aaaaaaaa-0000-0000-0000-000000000001"
		privateUUID  = "bbbbbbbb-0000-0000-0000-000000000002"
		missingUUID  = "cccccccc-0000-0000-0000-000000000003"
		otherCompany = "dddddddd-0000-0000-0000-000000000004"
	)

	cases := []struct {
		name   string
		uuid   string
		userID uint
		role   string
		shared bool
		status int
	}{
		{"public-karyawan", publicUUID, karyawanID, models.RoleKaryawan, false, 200},
		{"public-admin", publicUUID, adminID, models.RoleAdminAnper, false, 200},
		{"private-admin", privateUUID, adminID, models.RoleAdminAnper, false, 200},
		{"private-shared-karyawan", privateUUID, karyawanID, models.RoleKaryawan, true, 200},
		{"private-unshared-karyawan", privateUUID, karyawanID, models.RoleKaryawan, false, 403},
		{"cross-tenant-404", otherCompany, adminID, models.RoleAdminAnper, false, 404},
		{"unknown-uuid-404", missingUUID, adminID, models.RoleAdminAnper, false, 404},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=detail_test"), &gorm.Config{
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

			// Query dokumen: wajib uuid + company_id JWT, lalu resolve sesuai skenario.
			if err := db.Callback().Query().After("gorm:query").Register("test:document", func(tx *gorm.DB) {
				if tx.Statement.Table != "documents" {
					return
				}
				sql := tx.Statement.SQL.String()
				if !strings.Contains(sql, "uuid") || !strings.Contains(sql, "company_id") {
					t.Fatalf("document query must filter uuid and company_id: %s; vars=%v", sql, tx.Statement.Vars)
				}
				if tx.Statement.Vars[1] != companyID {
					t.Fatalf("tenant filter uses %v, want JWT company %d", tx.Statement.Vars[1], companyID)
				}
				if tc.uuid == missingUUID || tc.uuid == otherCompany {
					tx.AddError(gorm.ErrRecordNotFound)
					tx.RowsAffected = 0
					return
				}
				document := models.Document{Id: 15, UUID: tc.uuid, CompanyID: companyID}
				if tc.uuid == privateUUID {
					document.IsPrivate = true
				}
				if dest, ok := tx.Statement.Dest.(*models.Document); ok {
					*dest = document
				}
				tx.RowsAffected = 1
			}); err != nil {
				t.Fatal(err)
			}

			// Count document_shared: hanya dijalankan untuk private non-admin.
			var shareQueries int
			if err := db.Callback().Query().After("gorm:query").Register("test:shared", func(tx *gorm.DB) {
				if tx.Statement.Table != "document_shareds" {
					return
				}
				shareQueries++
				if tx.Statement.Vars[0] != uint(15) || tx.Statement.Vars[1] != tc.userID {
					t.Fatalf("share check vars = %v, want [15 %d]", tx.Statement.Vars, tc.userID)
				}
				if dest, ok := tx.Statement.Dest.(*int64); ok {
					if tc.shared {
						*dest = 1
					} else {
						*dest = 0
					}
				}
				tx.RowsAffected = 1
			}); err != nil {
				t.Fatal(err)
			}

			token, _, err := middleware.GenerateToken(middleware.AuthClaims{
				UserID: tc.userID, CompanyID: companyID, Role: tc.role,
			})
			if err != nil {
				t.Fatal(err)
			}

			router := gin.New()
			router.Use(middleware.AuthMiddleware())
			router.GET("/portal/documents/:uuid", GetPortalDocumentDetail(db))

			req := httptest.NewRequest("GET", "/portal/documents/"+tc.uuid, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)

			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.status, response.Body.String())
			}

			wantShareQueries := 0
			if tc.uuid == privateUUID && tc.role != models.RoleAdminAnper {
				wantShareQueries = 1
			}
			if shareQueries != wantShareQueries {
				t.Fatalf("document_shared queries = %d, want %d", shareQueries, wantShareQueries)
			}

			if tc.status == 200 {
				var body struct {
					Status  string `json:"status"`
					Message string `json:"message"`
					Data    map[string]any
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Message != "Detail dokumen berhasil dimuat" {
					t.Fatalf("message = %q", body.Message)
				}
				// Kontrak spec: tidak boleh membocorkan kolom internal.
				for _, leak := range []string{"company_id", "created_by", "file_path"} {
					if strings.Contains(response.Body.String(), `"`+leak+`"`) {
						t.Fatalf("response leaks %q: %s", leak, response.Body.String())
					}
				}
				if _, ok := body.Data["document_number"]; !ok {
					t.Fatalf("document_number missing: %s", response.Body.String())
				}
			}

			if tc.name == "private-unshared-karyawan" &&
				!strings.Contains(response.Body.String(), "tidak memiliki izin") {
				t.Fatalf("403 message mismatch: %s", response.Body.String())
			}
			if tc.status == 404 && !strings.Contains(response.Body.String(), "Dokumen tidak ditemukan") {
				t.Fatalf("404 message mismatch: %s", response.Body.String())
			}
		})
	}
}
