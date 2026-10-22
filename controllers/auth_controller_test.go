package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func testDB(t *testing.T) (*gorm.DB, func()) {
	dsn := os.Getenv("AUTH_TEST_SQL_SERVER_URL")
	if dsn == "" {
		t.Skip("set AUTH_TEST_SQL_SERVER_URL untuk uji auth")
	}
	db, err := gorm.Open(sqlserver.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("auth_test_%d_", time.Now().UnixNano())},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	return tx, func() {
		tx.Rollback()
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}
}

func seedTestData(t *testing.T, db *gorm.DB) (coA, coB models.Company, userA, userB models.User) {
	coA = models.Company{Name: "Company A", Code: "CA", Status: "active"}
	coB = models.Company{Name: "Company B", Code: "CB", Status: "active"}
	db.Create(&coA)
	db.Create(&coB)

	hash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy" // nosemgrep: generic.secrets.security.detected-bcrypt-hash.detected-bcrypt-hash — test fixture hash untuk "secret"
	userA = models.User{Name: "A", NPP: "1001", Password: hash, Role: "KARYAWAN", Status: "active", CompanyID: coA.Id}
	userB = models.User{Name: "B", NPP: "2001", Password: hash, Role: "KARYAWAN", Status: "active", CompanyID: coB.Id}
	db.Create(&userA)
	db.Create(&userB)
	return
}

func TestLogin(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	coA, _, userA, _ := seedTestData(t, db)
	_ = coA

	os.Setenv("JWT_SECRET", "test-secret")
	defer os.Unsetenv("JWT_SECRET")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/login", Login(db))

	// 1. valid credentials → 200
	body := `{"npp":"1001","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login valid: got %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]any)
	tokenStr := data["token"].(string)
	if tokenStr == "" {
		t.Fatal("token kosong")
	}
	if data["expires_in"].(float64) != 28800 {
		t.Fatalf("expires_in: got %v, want 28800", data["expires_in"])
	}
	user := data["user"].(map[string]any)
	if user["npp"] != "1001" {
		t.Fatalf("npp: got %v", user["npp"])
	}
	if _, hasPassword := user["password"]; hasPassword {
		t.Fatal("response mengandung password field")
	}

	// 2. wrong password → 401
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(`{"npp":"1001","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d", w.Code)
	}

	// 3. badge_number alias → 200
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(`{"badge_number":"1001","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("badge_number alias: got %d", w.Code)
	}

	// 4. empty payload → 400
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty payload: got %d", w.Code)
	}

	// 5. inactive user → 403
	db.Model(&userA).Update("status", "inactive")
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(`{"npp":"1001","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("inactive user: got %d", w.Code)
	}
	db.Model(&userA).Update("status", "active")
}

func TestMe(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	_, _, userA, _ := seedTestData(t, db)

	os.Setenv("JWT_SECRET", "test-secret")
	defer os.Unsetenv("JWT_SECRET")

	claims := middleware.AuthClaims{
		UserID: userA.Id, NPP: userA.NPP, Name: userA.Name, Role: userA.Role,
		CompanyID: userA.CompanyID, DepartmentID: userA.DepartmentID,
	}
	token, _, err := middleware.GenerateToken(claims)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/me", middleware.AuthMiddleware(), Me(db))

	// valid token → 200
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("me valid: got %d, body: %s", w.Code, w.Body.String())
	}

	// no token → 401
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d", w.Code)
	}

	// expired token → 401
	expiredClaims := claims
	expiredClaims.RegisteredClaims = jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
	}
	expiredToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims).SignedString([]byte("test-secret"))
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired token: got %d", w.Code)
	}
}

func TestTenantIsolation(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	coA, coB, _, _ := seedTestData(t, db)

	// company A user sees only A's docs; company B user sees only B's
	docA := models.Document{UUID: "doc-a", Title: "A Doc", CategoryID: 1, DepartmentID: 1, CompanyID: coA.Id}
	docB := models.Document{UUID: "doc-b", Title: "B Doc", CategoryID: 1, DepartmentID: 1, CompanyID: coB.Id}
	db.Create(&docA)
	db.Create(&docB)

	gin.SetMode(gin.TestMode)
	claimsA := middleware.AuthClaims{UserID: 1, CompanyID: coA.Id}
	claimsB := middleware.AuthClaims{UserID: 2, CompanyID: coB.Id}

	router := gin.New()
	router.GET("/docs", middleware.AuthMiddleware(), func(c *gin.Context) {
		var claims *middleware.AuthClaims
		if c.GetHeader("X-As-Company") == "A" {
			claims = &claimsA
		} else {
			claims = &claimsB
		}
		c.Set(middleware.ClaimsKey, claims)
		var docs []models.Document
		middleware.TenantDB(c, db).Find(&docs)
		c.JSON(200, gin.H{"count": len(docs)})
	})

	token, _, _ := middleware.GenerateToken(claimsA)
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-As-Company", "A")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("docs as A: %d", w.Code)
	}
	var r map[string]any
	json.Unmarshal(w.Body.Bytes(), &r)
	if r["count"].(float64) != 1 {
		t.Fatalf("A sees %v docs, want 1", r["count"])
	}

	token, _, _ = middleware.GenerateToken(claimsB)
	req = httptest.NewRequest(http.MethodGet, "/docs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-As-Company", "B")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &r)
	if r["count"].(float64) != 1 {
		t.Fatalf("B sees %v docs, want 1", r["count"])
	}
}
