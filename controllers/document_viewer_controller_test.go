package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/minio/minio-go/v7"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"be-prims/audit"
	"be-prims/middleware"
	"be-prims/models"
)

const pdfFixture = "%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"

// stubObject meniru *minio.Object: Source + Close tanpa MinIO hidup.
type stubObject struct {
	*strings.Reader
	closed bool
}

func (s *stubObject) Close() error {
	s.closed = true
	return nil
}

// newViewerTestDB menyiapkan gorm DryRun SQL Server (pola sama dengan
// document_detail_controller_test.go) plus resolver dokumen/company/share.
func newViewerTestDB(t *testing.T,
	uuid string, isPrivate bool, shared bool, companyName string) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=viewer_test"), &gorm.Config{
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

	if err := db.Callback().Query().After("gorm:query").Register("test:document", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "documents":
			sql := tx.Statement.SQL.String()
			if !strings.Contains(sql, "uuid") || !strings.Contains(sql, "company_id") {
				t.Fatalf("query dokumen wajib filter uuid+company_id: %s; vars=%v", sql, tx.Statement.Vars)
			}
			doc := models.Document{
				Id: 15, UUID: uuid, IsPrivate: isPrivate, CompanyID: 11,
				Attachment: models.Attachment{
					Id: 7, DocumentID: 15, FileName: "laporan.pdf",
					FilePath: "/uploads/documents/" + uuid + "_1700000000.pdf", FileSize: int64(len(pdfFixture)),
				},
			}
			if dest, ok := tx.Statement.Dest.(*models.Document); ok {
				*dest = doc
			}
			tx.RowsAffected = 1
		case "companies":
			if dest, ok := tx.Statement.Dest.(*models.Company); ok {
				*dest = models.Company{Id: 11, Name: companyName}
			}
			tx.RowsAffected = 1
		}
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Callback().Query().After("gorm:query").Register("test:shared", func(tx *gorm.DB) {
		if tx.Statement.Table != "document_shareds" {
			return
		}
		if dest, ok := tx.Statement.Dest.(*int64); ok {
			if shared {
				*dest = 1
			} else {
				*dest = 0
			}
		}
		tx.RowsAffected = 1
	}); err != nil {
		t.Fatal(err)
	}

	return db
}

// newViewerRouter merangkai handler viewer dengan recorder audit asinkron.
// Recorder dikembalikan supaya test bisa Flush() sebelum memeriksa baris log.
func newViewerRouter(t *testing.T, db *gorm.DB) (*gin.Engine, *audit.Recorder) {
	t.Helper()
	recorder := audit.NewRecorder(db, audit.Options{})
	t.Cleanup(recorder.Close)

	router := gin.New()
	router.POST("/portal/documents/:uuid/view-session",
		middleware.AuthMiddleware(), CreateDocumentViewSession(db))
	router.GET("/portal/documents/:uuid/stream", StreamDocumentPdf(db, recorder))
	return router, recorder
}

func bearerToken(t *testing.T, userID uint, role string) string {
	t.Helper()
	token, _, err := middleware.GenerateToken(middleware.AuthClaims{
		UserID: userID, CompanyID: 11, Role: role,
		Name: "Staf SDM Demo", BadgeNumber: "1002",
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// stubStorage mengganti akses MinIO agar stream bisa diuji tanpa server.
func stubStorage(t *testing.T, statErr error) *stubObject {
	t.Helper()
	obj := &stubObject{Reader: strings.NewReader(pdfFixture)}

	originalOpen, originalStat := openObject, statObject
	openObject = func(_ context.Context, _ string) (io.ReadCloser, error) {
		return obj, nil
	}
	statObject = func(_ context.Context, _ string) (minio.ObjectInfo, error) {
		if statErr != nil {
			return minio.ObjectInfo{}, statErr
		}
		return minio.ObjectInfo{Size: int64(len(pdfFixture))}, nil
	}
	t.Cleanup(func() { openObject, statObject = originalOpen, originalStat })
	return obj
}

func TestCreateDocumentViewSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "viewer-test-only-secret")

	const (
		publicUUID  = "aaaaaaaa-0000-0000-0000-000000000001"
		privateUUID = "bbbbbbbb-0000-0000-0000-000000000002"
	)

	cases := []struct {
		name   string
		uuid   string
		role   string
		shared bool
		status int
	}{
		{"public-karyawan", publicUUID, models.RoleKaryawan, false, 200},
		{"private-admin", privateUUID, models.RoleAdminAnper, false, 200},
		{"private-shared-karyawan", privateUUID, models.RoleKaryawan, true, 200},
		{"private-unshared-karyawan", privateUUID, models.RoleKaryawan, false, 403},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isPrivate := tc.uuid == privateUUID
			db := newViewerTestDB(t, tc.uuid, isPrivate, tc.shared, "PT Sri Melamin Rejeki")
			router, _ := newViewerRouter(t, db)

			req := httptest.NewRequest("POST", "/portal/documents/"+tc.uuid+"/view-session", nil)
			req.Header.Set("Authorization", "Bearer "+bearerToken(t, 2, tc.role))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status != 200 {
				return
			}

			var body struct {
				Status  string `json:"status"`
				Message string `json:"message"`
				Data    struct {
					ViewToken string `json:"view_token"`
					ExpiresIn int64  `json:"expires_in"`
					StreamURL string `json:"stream_url"`
					Watermark struct {
						CompanyName string `json:"company_name"`
						BadgeNumber string `json:"badge_number"`
						UserName    string `json:"user_name"`
						AccessTime  string `json:"access_time"`
						ClientIP    string `json:"client_ip"`
					} `json:"watermark_meta"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}

			if body.Message != "Sesi viewer dokumen berhasil dibuat" {
				t.Fatalf("message = %q", body.Message)
			}
			if want := "/api/v1/portal/documents/" + tc.uuid + "/stream"; body.Data.StreamURL != want {
				t.Fatalf("stream_url = %q, want %q", body.Data.StreamURL, want)
			}
			// Token sesi harus berumur pendek (5-10 menit).
			if body.Data.ExpiresIn <= 0 || body.Data.ExpiresIn > 600 {
				t.Fatalf("expires_in = %d, want 1..600", body.Data.ExpiresIn)
			}

			// watermark_meta harus akurat: badge & nama dari klaim, perusahaan dari
			// relasi company, waktu berformat WIB, IP tidak kosong.
			wm := body.Data.Watermark
			if wm.BadgeNumber != "1002" || wm.UserName != "Staf SDM Demo" {
				t.Fatalf("watermark identitas = %+v", wm)
			}
			if wm.CompanyName != "PT Sri Melamin Rejeki" {
				t.Fatalf("company_name = %q", wm.CompanyName)
			}
			if wm.ClientIP == "" {
				t.Fatal("client_ip kosong")
			}
			if _, err := time.Parse("2006-01-02 15:04:05 MST", wm.AccessTime); err != nil {
				t.Fatalf("access_time %q bukan format WIB: %v", wm.AccessTime, err)
			}
			if !strings.HasSuffix(wm.AccessTime, "WIB") {
				t.Fatalf("access_time = %q, harus berakhiran WIB", wm.AccessTime)
			}

			// Token harus memuat dokumen + identitas dan tidak membocorkan file_path.
			claims, err := middleware.ParseViewToken(body.Data.ViewToken)
			if err != nil {
				t.Fatalf("view token tidak valid: %v", err)
			}
			if claims.DocumentUUID != tc.uuid || claims.UserID != 2 || claims.ClientIP == "" {
				t.Fatalf("view token claims = %+v", claims)
			}
			if strings.Contains(rec.Body.String(), "file_path") ||
				strings.Contains(rec.Body.String(), "documents/"+tc.uuid+"_") {
				t.Fatalf("response membocorkan path storage: %s", rec.Body.String())
			}
		})
	}
}

func TestCreateDocumentViewSessionCrossTenantNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "viewer-test-only-secret")

	// Query tenant lain tidak menemukan dokumen → 404, bukan 403.
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=viewer_test"), &gorm.Config{
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

	if err := db.Callback().Query().After("gorm:query").Register("test:nf", func(tx *gorm.DB) {
		if tx.Statement.Table == "documents" {
			tx.AddError(gorm.ErrRecordNotFound)
			tx.RowsAffected = 0
		}
	}); err != nil {
		t.Fatal(err)
	}

	router, _ := newViewerRouter(t, db)
	req := httptest.NewRequest("POST", "/portal/documents/other-company-uuid/view-session", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken(t, 1, models.RoleAdminAnper))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Dokumen tidak ditemukan") {
		t.Fatalf("status = %d body=%s, want 404 Dokumen tidak ditemukan", rec.Code, rec.Body.String())
	}
}

func TestStreamDocumentPdf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "viewer-test-only-secret")

	const (
		publicUUID  = "aaaaaaaa-0000-0000-0000-000000000001"
		privateUUID = "bbbbbbbb-0000-0000-0000-000000000002"
	)

	newViewToken := func(t *testing.T, docUUID string, userID uint, role string) string {
		t.Helper()
		token, _, err := middleware.GenerateViewToken(middleware.ViewTokenClaims{
			DocumentUUID: docUUID, UserID: userID, CompanyID: 11, Role: role,
			BadgeNumber: "1002", UserName: "Staf SDM Demo", ClientIP: "192.168.1.45",
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}

	t.Run("stream-ok-headers-and-body", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		obj := stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		req := httptest.NewRequest("GET",
			"/portal/documents/"+publicUUID+"/stream?view_token="+newViewToken(t, publicUUID, 2, models.RoleKaryawan), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
			t.Fatalf("Content-Type = %q", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
			t.Fatalf("Content-Disposition = %q, want inline", cd)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Fatalf("Cache-Control = %q, wajib memuat no-store", cc)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("X-Content-Type-Options = %q", rec.Header().Get("X-Content-Type-Options"))
		}
		if rec.Body.String() != pdfFixture {
			t.Fatalf("body bukan biner PDF: %q", rec.Body.String())
		}
		if !obj.closed {
			t.Fatal("object stream tidak ditutup")
		}
	})

	t.Run("stream-writes-audit-log", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, recorder := newViewerRouter(t, db)

		var logged []models.DocumentAccessLog
		if err := db.Callback().Create().After("gorm:create").Register("test:audit", func(tx *gorm.DB) {
			if tx.Statement.Table != "document_access_logs" {
				return
			}
			if entry, ok := tx.Statement.Dest.(*models.DocumentAccessLog); ok {
				logged = append(logged, *entry)
			}
		}); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest("GET",
			"/portal/documents/"+publicUUID+"/stream?view_token="+newViewToken(t, publicUUID, 2, models.RoleKaryawan), nil)
		req.RemoteAddr = "10.14.22.105:51234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		// Penulisan asinkron: tunggu worker selesai sebelum memeriksa.
		if !recorder.Flush(2 * time.Second) {
			t.Fatal("recorder tidak selesai dalam batas waktu")
		}

		if len(logged) != 1 {
			t.Fatalf("access log rows = %d, want 1", len(logged))
		}
		got := logged[0]
		// ip_address memakai IP koneksi stream, bukan IP yang tersimpan di
		// view token: token bisa dipakai ulang dari jaringan lain.
		if got.DocumentID != 15 || got.UserID != 2 || got.Action != "VIEW" || got.IpAddress != "10.14.22.105" {
			t.Fatalf("access log = %+v", got)
		}
		if got.AccessedAt.IsZero() {
			t.Fatal("accessed_at kosong")
		}
	})

	t.Run("stream-debounce-same-user-one-minute", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, recorder := newViewerRouter(t, db)

		var created int
		if err := db.Callback().Create().After("gorm:create").Register("test:audit-count", func(tx *gorm.DB) {
			if tx.Statement.Table == "document_access_logs" {
				created++
			}
		}); err != nil {
			t.Fatal(err)
		}

		token := newViewToken(t, publicUUID, 2, models.RoleKaryawan)
		for i := 0; i < 3; i++ {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest("GET",
				"/portal/documents/"+publicUUID+"/stream?view_token="+token, nil))
			if rec.Code != 200 {
				t.Fatalf("request %d: status = %d", i, rec.Code)
			}
		}
		if !recorder.Flush(2 * time.Second) {
			t.Fatal("recorder tidak selesai dalam batas waktu")
		}

		if created != 1 {
			t.Fatalf("access log rows = %d, want 1 (refresh dalam 1 menit harus di-debounce)", created)
		}
	})

	t.Run("stream-audit-failure-does-not-break-stream", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, recorder := newViewerRouter(t, db)

		if err := db.Callback().Create().Before("gorm:create").Register("test:audit-fail", func(tx *gorm.DB) {
			if tx.Statement.Table == "document_access_logs" {
				tx.AddError(errors.New("db access log down"))
			}
		}); err != nil {
			t.Fatal(err)
		}

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET",
			"/portal/documents/"+publicUUID+"/stream?view_token="+newViewToken(t, publicUUID, 2, models.RoleKaryawan), nil))
		if !recorder.Flush(2 * time.Second) {
			t.Fatal("recorder tidak selesai dalam batas waktu")
		}

		if rec.Code != 200 || rec.Body.String() != pdfFixture {
			t.Fatalf("kegagalan audit menggagalkan stream: status=%d body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("stream-bearer-jwt-fallback", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		req := httptest.NewRequest("GET", "/portal/documents/"+publicUUID+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+bearerToken(t, 1, models.RoleAdminAnper))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 200 || rec.Body.String() != pdfFixture {
			t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("stream-private-unshared-403", func(t *testing.T) {
		db := newViewerTestDB(t, privateUUID, true, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		req := httptest.NewRequest("GET",
			"/portal/documents/"+privateUUID+"/stream?view_token="+newViewToken(t, privateUUID, 2, models.RoleKaryawan), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 403 {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "tidak memiliki izin") {
			t.Fatalf("403 message mismatch: %s", rec.Body.String())
		}
	})

	t.Run("stream-private-shared-ok", func(t *testing.T) {
		db := newViewerTestDB(t, privateUUID, true, true, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		req := httptest.NewRequest("GET",
			"/portal/documents/"+privateUUID+"/stream?view_token="+newViewToken(t, privateUUID, 2, models.RoleKaryawan), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("stream-token-for-other-document-401", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		// Token diterbitkan untuk dokumen lain: harus ditolak sebelum ke MinIO.
		req := httptest.NewRequest("GET",
			"/portal/documents/"+publicUUID+"/stream?view_token="+newViewToken(t, privateUUID, 2, models.RoleKaryawan), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 401 || !strings.Contains(rec.Body.String(), "tidak berlaku untuk dokumen ini") {
			t.Fatalf("status = %d body=%s, want 401", rec.Code, rec.Body.String())
		}
	})

	t.Run("stream-no-credential-401", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", "/portal/documents/"+publicUUID+"/stream", nil))

		if rec.Code != 401 {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("stream-malformed-token-401", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		valid := newViewToken(t, publicUUID, 2, models.RoleKaryawan)
		req := httptest.NewRequest("GET", "/portal/documents/"+publicUUID+"/stream?view_token="+valid+"x", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 401 || !strings.Contains(rec.Body.String(), "tidak valid atau telah kedaluwarsa") {
			t.Fatalf("status = %d body=%s, want 401", rec.Code, rec.Body.String())
		}
	})

	t.Run("stream-expired-token-401", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, nil)
		router, _ := newViewerRouter(t, db)

		// Ditandatangani dengan secret yang benar, tetapi masa berlakunya lewat.
		expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.ViewTokenClaims{
			DocumentUUID: publicUUID, UserID: 2, CompanyID: 11, Role: models.RoleKaryawan,
			Purpose: "document_view",
			RegisteredClaims: jwt.RegisteredClaims{
				Audience:  jwt.ClaimStrings{"document-view"},
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			},
		}).SignedString([]byte("viewer-test-only-secret"))
		if err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest("GET", "/portal/documents/"+publicUUID+"/stream?view_token="+expired, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 401 {
			t.Fatalf("status = %d, want 401 untuk token kedaluwarsa", rec.Code)
		}
	})

	t.Run("stream-object-missing-404", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, minio.ErrorResponse{Code: "NoSuchKey", StatusCode: http.StatusNotFound})
		router, _ := newViewerRouter(t, db)

		req := httptest.NewRequest("GET",
			"/portal/documents/"+publicUUID+"/stream?view_token="+newViewToken(t, publicUUID, 2, models.RoleKaryawan), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Berkas dokumen tidak ditemukan") {
			t.Fatalf("status = %d body=%s, want 404", rec.Code, rec.Body.String())
		}
	})

	t.Run("stream-storage-error-500", func(t *testing.T) {
		db := newViewerTestDB(t, publicUUID, false, false, "PT Sri Melamin Rejeki")
		stubStorage(t, errors.New("dial tcp: connection refused"))
		router, _ := newViewerRouter(t, db)

		req := httptest.NewRequest("GET",
			"/portal/documents/"+publicUUID+"/stream?view_token="+newViewToken(t, publicUUID, 2, models.RoleKaryawan), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 500 {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// Token viewer tidak boleh diterima sebagai Bearer API, dan sebaliknya.
func TestViewTokenIsNotApiToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "viewer-test-only-secret")
	t.Setenv("VIEW_TOKEN_SECRET", "")

	viewToken, _, err := middleware.GenerateViewToken(middleware.ViewTokenClaims{
		DocumentUUID: "aaaaaaaa-0000-0000-0000-000000000001", UserID: 2, CompanyID: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := middleware.ParseToken(viewToken); err == nil {
		t.Fatal("view token diterima sebagai token API")
	}

	apiToken, _, err := middleware.GenerateToken(middleware.AuthClaims{UserID: 2, CompanyID: 11})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := middleware.ParseViewToken(apiToken); err == nil {
		t.Fatal("token API diterima sebagai view token")
	}
}
