package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"be-prims/middleware"
	"be-prims/models"
	"be-prims/pdfstamp"
)

const stampedFixture = "%PDF-1.7 stamped"

// withAllowDownload menimpa kolom allow_download pada dokumen hasil resolver
// newViewerTestDB (yang selalu mengembalikan zero value = false).
func withAllowDownload(t *testing.T, db *gorm.DB, allow bool) {
	t.Helper()
	if err := db.Callback().Query().After("test:document").Register("test:allow_download", func(tx *gorm.DB) {
		if dest, ok := tx.Statement.Dest.(*models.Document); ok && tx.Statement.Table == "documents" {
			dest.AllowDownload = allow
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// stubStamp mengganti pdfcpu; mengembalikan pointer identitas yang diterima.
func stubStamp(t *testing.T, stampErr error) *pdfstamp.Identity {
	t.Helper()
	got := &pdfstamp.Identity{}
	original := stampPdf
	stampPdf = func(_ context.Context, src io.ReadSeeker, w io.Writer, id pdfstamp.Identity) error {
		*got = id
		if stampErr != nil {
			return stampErr
		}
		if b, _ := io.ReadAll(src); string(b) != pdfFixture {
			t.Errorf("stamper menerima bukan berkas asli: %q", b)
		}
		_, err := io.WriteString(w, stampedFixture)
		return err
	}
	t.Cleanup(func() { stampPdf = original })
	return got
}

// captureAccessLogs merekam setiap INSERT ke document_access_logs pada DB
// DryRun. insertErr != nil mensimulasikan kegagalan DB saat INSERT.
func captureAccessLogs(t *testing.T, db *gorm.DB, insertErr error) *[]models.DocumentAccessLog {
	t.Helper()
	rows := &[]models.DocumentAccessLog{}
	if err := db.Callback().Create().After("gorm:create").Register("test:access_log", func(tx *gorm.DB) {
		entry, ok := tx.Statement.Dest.(*models.DocumentAccessLog)
		if !ok {
			return
		}
		if insertErr != nil {
			tx.AddError(insertErr)
			return
		}
		*rows = append(*rows, *entry)
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func newDownloadRouter(t *testing.T, db *gorm.DB) *gin.Engine {
	t.Helper()
	router := gin.New()
	router.GET("/portal/documents/:uuid/download", middleware.AuthMiddleware(), DownloadDocumentPdf(db))
	return router
}

func doDownload(t *testing.T, router *gin.Engine, uuid, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/portal/documents/"+uuid+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken(t, 2, role))
	req.RemoteAddr = "192.168.1.50:51234"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body bukan JSON: %s", rec.Body.String())
	}
	if body.Status != "error" {
		t.Fatalf("status body = %q, want error", body.Status)
	}
	return body.Message
}

func TestDownloadDocumentPdfAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "download-test-only-secret")

	const viewOnlyMsg = "Dokumen ini diatur sebagai 'Hanya Baca' (View Only) dan tidak diizinkan untuk diunduh."

	cases := []struct {
		name     string
		private  bool
		shared   bool
		allow    bool
		role     string
		status   int
		wantMsg  string
		wantFile bool
	}{
		{"allowed-karyawan", false, false, true, models.RoleKaryawan, 200, "", true},
		{"view-only-karyawan", false, false, false, models.RoleKaryawan, 403, viewOnlyMsg, false},
		{"view-only-admin-tetap-boleh", false, false, false, models.RoleAdminAnper, 200, "", true},
		{"private-shared-karyawan", true, true, true, models.RoleKaryawan, 200, "", true},
		{"private-unshared-karyawan", true, false, true, models.RoleKaryawan, 403,
			"Akses ditolak. Anda tidak memiliki izin untuk melihat dokumen rahasia ini.", false},
		{"private-admin", true, false, false, models.RoleAdminAnper, 200, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const uuid = "cccccccc-0000-0000-0000-000000000003"
			db := newViewerTestDB(t, uuid, tc.private, tc.shared, "PT Sri Melamin Rejeki")
			withAllowDownload(t, db, tc.allow)
			stubStorage(t, nil)
			got := stubStamp(t, nil)
			logs := captureAccessLogs(t, db, nil)

			rec := doDownload(t, newDownloadRouter(t, db), uuid, tc.role)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if !tc.wantFile {
				if msg := errorMessage(t, rec); msg != tc.wantMsg {
					t.Fatalf("message = %q, want %q", msg, tc.wantMsg)
				}
				if len(*logs) != 0 {
					t.Fatalf("unduhan ditolak tidak boleh tercatat: %+v", *logs)
				}
				return
			}

			// US-12-BE: tepat satu baris DOWNLOAD, sinkron dengan stempel.
			if len(*logs) != 1 {
				t.Fatalf("log DOWNLOAD = %d baris, want 1", len(*logs))
			}
			entry := (*logs)[0]
			if entry.Action != "DOWNLOAD" || entry.DocumentID != 15 || entry.UserID != 2 ||
				entry.IpAddress != "192.168.1.50" {
				t.Fatalf("isi log salah: %+v", entry)
			}
			if !entry.AccessedAt.Equal(got.DownloadAt) {
				t.Fatalf("accessed_at %v tidak sinkron dengan stempel %v", entry.AccessedAt, got.DownloadAt)
			}
			if time.Since(entry.AccessedAt) > time.Minute {
				t.Fatalf("accessed_at bukan waktu server saat unduh: %v", entry.AccessedAt)
			}

			if rec.Body.String() != stampedFixture {
				t.Fatalf("body bukan hasil stempel: %q", rec.Body.String())
			}
			h := rec.Header()
			if h.Get("Content-Type") != "application/pdf" {
				t.Fatalf("Content-Type = %q", h.Get("Content-Type"))
			}
			if h.Get("Content-Length") != "16" {
				t.Fatalf("Content-Length = %q, want ukuran hasil stempel", h.Get("Content-Length"))
			}
			disp, params, err := mime.ParseMediaType(h.Get("Content-Disposition"))
			if err != nil || disp != "attachment" || params["filename"] != "laporan.pdf" {
				t.Fatalf("Content-Disposition = %q", h.Get("Content-Disposition"))
			}

			if got.BadgeNumber != "1002" || got.Name != "Staf SDM Demo" ||
				got.CompanyName != "PT Sri Melamin Rejeki" || got.GeneratedBy != defaultPublicURL {
				t.Fatalf("identitas stempel salah: %+v", *got)
			}
			if got.DownloadAt.Location() != wibZone {
				t.Fatalf("waktu stempel harus WIB, got %v", got.DownloadAt.Location())
			}
		})
	}
}

func TestDownloadDocumentPdfStampFailureNoFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "download-test-only-secret")

	const uuid = "dddddddd-0000-0000-0000-000000000004"
	db := newViewerTestDB(t, uuid, false, false, "PT Sri Melamin Rejeki")
	withAllowDownload(t, db, true)
	stubStorage(t, nil)
	stubStamp(t, errors.New("pdf rusak"))
	logs := captureAccessLogs(t, db, nil)

	rec := doDownload(t, newDownloadRouter(t, db), uuid, models.RoleKaryawan)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if len(*logs) != 0 {
		t.Fatalf("unduhan gagal tidak boleh tercatat: %+v", *logs)
	}
	if rec.Header().Get("Content-Type") == "application/pdf" {
		t.Fatal("berkas asli tidak boleh dikirim saat stempel gagal")
	}
	if msg := errorMessage(t, rec); msg != "Gagal memproses berkas dokumen" {
		t.Fatalf("message = %q", msg)
	}
}

func TestDownloadDocumentPdfObjectMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "download-test-only-secret")

	const uuid = "eeeeeeee-0000-0000-0000-000000000005"
	db := newViewerTestDB(t, uuid, false, false, "PT Sri Melamin Rejeki")
	withAllowDownload(t, db, true)
	stubStamp(t, nil)
	logs := captureAccessLogs(t, db, nil)

	original := openObject
	openObject = func(_ context.Context, _ string) (io.ReadCloser, error) {
		return &errReader{err: minio.ErrorResponse{Code: "NoSuchKey", StatusCode: 404}}, nil
	}
	t.Cleanup(func() { openObject = original })

	rec := doDownload(t, newDownloadRouter(t, db), uuid, models.RoleKaryawan)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if len(*logs) != 0 {
		t.Fatalf("berkas hilang tidak boleh tercatat: %+v", *logs)
	}
}

// US-12-BE: setiap unduhan tercatat, tanpa debounce seperti VIEW.
func TestDownloadDocumentPdfLogsEveryDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "download-test-only-secret")

	const uuid = "ffffffff-0000-0000-0000-000000000006"
	db := newViewerTestDB(t, uuid, false, false, "PT Sri Melamin Rejeki")
	withAllowDownload(t, db, true)
	stubStamp(t, nil)
	logs := captureAccessLogs(t, db, nil)
	router := newDownloadRouter(t, db)

	for i := 0; i < 3; i++ {
		stubStorage(t, nil) // reader baru per unduhan
		if rec := doDownload(t, router, uuid, models.RoleKaryawan); rec.Code != http.StatusOK {
			t.Fatalf("unduhan %d: status = %d", i+1, rec.Code)
		}
	}
	if len(*logs) != 3 {
		t.Fatalf("log DOWNLOAD = %d baris, want 3 (satu per unduhan)", len(*logs))
	}
}

// Kegagalan INSERT audit tidak boleh menggagalkan pengiriman berkas.
func TestDownloadDocumentPdfLogFailureStillSendsFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "download-test-only-secret")

	const uuid = "abababab-0000-0000-0000-000000000007"
	db := newViewerTestDB(t, uuid, false, false, "PT Sri Melamin Rejeki")
	withAllowDownload(t, db, true)
	stubStorage(t, nil)
	stubStamp(t, nil)
	captureAccessLogs(t, db, errors.New("db down"))

	rec := doDownload(t, newDownloadRouter(t, db), uuid, models.RoleKaryawan)
	if rec.Code != http.StatusOK || rec.Body.String() != stampedFixture {
		t.Fatalf("status = %d body=%q, want 200 + berkas", rec.Code, rec.Body.String())
	}
}

// Anper guard: dokumen tenant lain → 404 dan tidak ada log DOWNLOAD.
func TestDownloadDocumentPdfCrossTenantNotLogged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "download-test-only-secret")

	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=download_test"), &gorm.Config{
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
			sql := tx.Statement.SQL.String()
			if !strings.Contains(sql, "company_id") {
				t.Errorf("query dokumen wajib filter company_id: %s", sql)
			}
			tx.AddError(gorm.ErrRecordNotFound)
		}
	}); err != nil {
		t.Fatal(err)
	}
	stubStorage(t, nil)
	stubStamp(t, nil)
	logs := captureAccessLogs(t, db, nil)

	rec := doDownload(t, newDownloadRouter(t, db), "dokumen-anper-lain", models.RoleAdminAnper)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(*logs) != 0 {
		t.Fatalf("akses lintas anper tidak boleh tercatat: %+v", *logs)
	}
}

func TestAttachmentDisposition(t *testing.T) {
	cases := map[string]string{
		"laporan.pdf":            "laporan.pdf",
		`a"b.pdf`:                `a"b.pdf`,
		"../../etc/passwd":       "passwd.pdf",
		`C:\docs\SK Direksi.pdf`: "SK Direksi.pdf",
		"":                       "dokumen.pdf",
		"Laporan Ü.pdf":          "Laporan Ü.pdf",
	}
	for in, want := range cases {
		_, params, err := mime.ParseMediaType(attachmentDisposition(in))
		if err != nil || params["filename"] != want {
			t.Errorf("attachmentDisposition(%q) → %q (err %v), want filename %q",
				in, attachmentDisposition(in), err, want)
		}
	}
}

// errReader meniru *minio.Object yang error saat Read (GetObject lazy).
type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }
func (e *errReader) Close() error             { return nil }
