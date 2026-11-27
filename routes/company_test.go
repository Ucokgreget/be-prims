package routes

import (
	"be-prims/audit"
	"be-prims/database"
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/storage"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

const companiesURL = "/api/v1/superadmin/companies"

func TestCompanyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "company-test-secret")
	router := gin.New()
	SetupRouter(router, nil, nil)
	for _, operation := range []struct{ method, path string }{
		{"GET", companiesURL}, {"POST", companiesURL}, {"PUT", companiesURL + "/1"}, {"PATCH", companiesURL + "/1/status"},
	} {
		for _, trial := range []struct {
			role string
			want int
		}{{"", 401}, {models.RoleKaryawan, 403}, {models.RoleAdminAnper, 403}} {
			var token string
			if trial.role != "" {
				token, _, _ = middleware.GenerateToken(middleware.AuthClaims{Role: trial.role, CompanyID: 1})
			}
			r := requestCompanyRoute(router, operation.method, operation.path, token, "application/json", nil)
			if r.Code != trial.want {
				t.Fatalf("%s %s role=%s: %d, want %d: %s", operation.method, operation.path, trial.role, r.Code, trial.want, r.Body)
			}
		}
	}
}

// Requires an empty test database and a disposable MinIO bucket via MINIO_*.
func TestCompanyLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("PRIMS_TEST_SQLSERVER_URL")
	if dsn == "" {
		t.Skip("set PRIMS_TEST_SQLSERVER_URL and MINIO_* for the SQL Server/MinIO integration test")
	}
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "company-integration-test-secret")
	db, err := gorm.Open(sqlserver.Open(dsn), &gorm.Config{})
	mustCompanyOK(t, err)
	sqlDB, err := db.DB()
	mustCompanyOK(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Start with the previous company schema, on any database collation.
	mustCompanyOK(t, db.Exec(`CREATE TABLE companies (id bigint IDENTITY(1,1) PRIMARY KEY, name nvarchar(150), code nvarchar(50), status nvarchar(20) DEFAULT 'active', created_at datetimeoffset, updated_at datetimeoffset);
		CREATE UNIQUE INDEX idx_companies_code ON companies(code);
		INSERT INTO companies (name,code) VALUES ('Legacy','LeGaCy');`).Error)
	for range 2 {
		mustCompanyOK(t, database.AutoMigrate(db))
	}
	mustCompanyOK(t, storage.Init())
	recorder := audit.NewRecorder(db, audit.Options{})
	t.Cleanup(recorder.Close)
	router := gin.New()
	SetupRouter(router, db, recorder)

	call := func(method, path, token, body string, want int) []byte {
		t.Helper()
		r := requestCompanyRoute(router, method, path, token, "application/json", strings.NewReader(body))
		if r.Code != want {
			t.Fatalf("%s %s: status=%d, want %d: %s", method, path, r.Code, want, r.Body)
		}
		return r.Body.Bytes()
	}
	var legacy models.Company
	mustCompanyOK(t, db.Where("code = ?", "legacy").Take(&legacy).Error)
	if legacy.StorageQuotaGB != 50 || legacy.AuthVersion != 0 || legacy.Name != "Legacy" {
		t.Fatalf("legacy company not preserved: %+v", legacy)
	}
	admin, _, err := middleware.GenerateToken(middleware.AuthClaims{Role: models.RoleSuperAdmin, CompanyID: legacy.Id})
	mustCompanyOK(t, err)
	for _, body := range []string{
		`{"name":" ","code":"A"}`, `{"name":"Anper","code":"A","storage_quota_gb":0}`,
		`{"name":"Anper","code":"A","status":"active"}`, `{"name":"Anper","code":"A"} {}`,
	} {
		call("POST", companiesURL, admin, body, 400)
	}
	body := call("POST", companiesURL, admin, `{"name":"Duplicate","code":"LEGACY"}`, 400)
	if !strings.Contains(string(body), "Kode perusahaan sudah terdaftar") {
		t.Fatal(string(body))
	}
	var created struct {
		Success bool
		Data    struct{ ID uint }
	}
	mustCompanyOK(t, json.Unmarshal(call("POST", companiesURL, admin, `{"name":"Anper QA","code":"QATEST","storage_quota_gb":1,"address":"Palembang"}`, 201), &created))
	id := created.Data.ID
	if !created.Success || id == 0 {
		t.Fatalf("unexpected created company: %+v", created)
	}
	marker, err := storage.Stat(t.Context(), storage.TenantPrefix(id))
	mustCompanyOK(t, err)
	if marker.Size != 0 {
		t.Fatalf("tenant marker size: %d", marker.Size)
	}
	path := fmt.Sprintf("%s/%d", companiesURL, id)
	dropdown := call("GET", "/api/v1/company", "", "", 200)
	if !strings.Contains(string(dropdown), "QATEST") || strings.Contains(string(dropdown), "storage_quota_gb") || strings.Contains(string(dropdown), "Palembang") {
		t.Fatal(string(dropdown))
	}
	call("PUT", path, admin, `{"name":"Anper QA","code":"lEgAcY","storage_quota_gb":1}`, 400)
	call("PUT", path, admin, `{"name":"Anper Baru","code":"QANEW","storage_quota_gb":2,"address":""}`, 200)

	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	mustCompanyOK(t, err)
	user := models.User{Name: "Admin QA", BadgeNumber: "qa-100", Password: string(hash), Role: models.RoleAdminAnper, CompanyID: id}
	mustCompanyOK(t, db.Create(&user).Error)
	category := models.Category{Name: "Cat QA", CompanyID: id}
	department := models.Department{Name: "Dept QA", CompanyID: id}
	tag := models.Tag{Name: "Tag QA"}
	mustCompanyOK(t, db.Create(&category).Error)
	mustCompanyOK(t, db.Create(&department).Error)
	mustCompanyOK(t, db.Create(&tag).Error)

	loginBody := fmt.Sprintf(`{"badge_number":"qa-100","password":"test-password","company_id":%d}`, id)
	login := func() string {
		t.Helper()
		var res struct{ Data struct{ Token string } }
		mustCompanyOK(t, json.Unmarshal(call("POST", "/api/v1/auth/login", "", loginBody, 200), &res))
		return res.Data.Token
	}
	employee := login()
	const pdf = "%PDF-1.0\n"
	var uploadBody bytes.Buffer
	m := multipart.NewWriter(&uploadBody)
	for name, value := range map[string]string{
		"title": "QA PDF", "description": "QA Document", "document_number": "QA-1", "publication_date": "2026-01-02",
		"category_id": fmt.Sprint(category.Id), "department_id": fmt.Sprint(department.Id), "tag_id": fmt.Sprint(tag.Id),
	} {
		mustCompanyOK(t, m.WriteField(name, value))
	}
	part, err := m.CreateFormFile("attachment", "qa.pdf")
	mustCompanyOK(t, err)
	_, err = io.WriteString(part, pdf)
	mustCompanyOK(t, err)
	mustCompanyOK(t, m.Close())
	upload := func() *httptest.ResponseRecorder {
		return requestCompanyRoute(router, "POST", "/api/v1/document", employee, m.FormDataContentType(), bytes.NewReader(uploadBody.Bytes()))
	}
	uploaded := upload()
	if uploaded.Code != 201 {
		t.Fatalf("upload: %d %s", uploaded.Code, uploaded.Body)
	}
	var document struct{ Data models.Document }
	mustCompanyOK(t, json.Unmarshal(uploaded.Body.Bytes(), &document))
	var attachment models.Attachment
	mustCompanyOK(t, db.Where("document_id = ?", document.Data.Id).Take(&attachment).Error)
	if attachment.FilePath != "/uploads/"+storage.TenantPrefix(id)+document.Data.UUID+".pdf" {
		t.Fatal(attachment.FilePath)
	}
	// Simulate a large existing attachment via its stored byte count; no giant fixture needed.
	mustCompanyOK(t, db.Model(&attachment).Update("file_size", 3<<30).Error)
	if r := upload(); r.Code != 409 || !strings.Contains(r.Body.String(), "storage_quota_exceeded") {
		t.Fatalf("quota: %d %s", r.Code, r.Body)
	}
	var list struct {
		Data []struct {
			ID           uint
			StorageQuota int     `json:"storage_quota_gb"`
			Bytes        int64   `json:"storage_used_bytes"`
			GB           float64 `json:"storage_used_gb"`
			Users        int64   `json:"total_users"`
			Documents    int64   `json:"total_documents"`
			Address      string
		}
		Pagination struct{ Total, Page, Limit int }
	}
	mustCompanyOK(t, json.Unmarshal(call("GET", companiesURL+"?search=ANPER+BARU&status=active&page=1&limit=1", admin, "", 200), &list))
	if list.Pagination.Total != 1 || list.Pagination.Page != 1 || list.Pagination.Limit != 1 || len(list.Data) != 1 {
		t.Fatalf("list pagination: %+v", list)
	}
	if c := list.Data[0]; c.ID != id || c.StorageQuota != 2 || c.Bytes != 3<<30 || c.GB != 3 || c.Users != 1 || c.Documents != 1 || c.Address != "" {
		t.Fatalf("list aggregates: %+v", c)
	}

	// Only one PDF fits. The two requests must not both pass the same quota check.
	mustCompanyOK(t, db.Model(&attachment).Update("file_size", 2<<30-len(pdf)).Error)
	statuses := make(chan int, 2)
	for range 2 {
		go func() { statuses <- upload().Code }()
	}
	first, second := <-statuses, <-statuses
	if !((first == 201 && second == 409) || (first == 409 && second == 201)) {
		t.Fatalf("concurrent quota checks returned %d and %d", first, second)
	}

	docPath := "/api/v1/portal/documents/" + document.Data.UUID
	var viewSession struct {
		Data struct {
			Token string `json:"view_token"`
		}
	}
	mustCompanyOK(t, json.Unmarshal(call("POST", docPath+"/view-session", employee, "", 200), &viewSession))
	viewPath := docPath + "/stream?view_token=" + viewSession.Data.Token
	if data := call("GET", viewPath, "", "", 200); !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("expected original PDF, got %q", data)
	}
	call("PATCH", path+"/status", admin, `{"status":"inactive"}`, 200)
	var inactive models.Company
	mustCompanyOK(t, db.First(&inactive, id).Error)
	call("PATCH", path+"/status", admin, `{"status":"inactive"}`, 200)
	var same models.Company
	mustCompanyOK(t, db.First(&same, id).Error)
	if inactive.AuthVersion != 1 || same.AuthVersion != 1 {
		t.Fatalf("unexpected revocation versions: %d, %d", inactive.AuthVersion, same.AuthVersion)
	}
	if body := call("POST", "/api/v1/auth/login", "", loginBody, 403); !bytes.Contains(body, []byte(middleware.InactiveCompanyMessage)) {
		t.Fatal(string(body))
	}
	call("GET", "/api/v1/auth/me", employee, "", 403)
	call("GET", docPath+"/stream", employee, "", 403)
	call("GET", viewPath, "", "", 403)
	inactiveList := call("GET", companiesURL+"?status=inactive", admin, "", 200)
	if !bytes.Contains(inactiveList, []byte(`"code":"QANEW"`)) {
		t.Fatal(string(inactiveList))
	}
	call("PATCH", path+"/status", admin, `{"status":"active"}`, 200)
	call("GET", "/api/v1/auth/me", employee, "", 401)
	call("GET", viewPath, "", "", 401)
	fresh := login()
	call("GET", "/api/v1/auth/me", fresh, "", 200)
	call("GET", docPath+"/stream", fresh, "", 200)
}

func mustCompanyOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requestCompanyRoute(router *gin.Engine, method, path, token, contentType string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}
