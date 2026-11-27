package controllers

import (
	"be-prims/middleware"
	"be-prims/models"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	mssql "github.com/microsoft/go-mssqldb"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCreateUserInsertResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, http.StatusCreated},
		{"duplicate badge", mssql.Error{Number: 2601}, http.StatusConflict},
		{"other write error", errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ponytail: DryRun covers HTTP error mapping and query count, not the live unique index; verify that with SQL Server integration.
			db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=user_test"), &gorm.Config{
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

			reads, writes := 0, 0
			if err := db.Callback().Query().After("gorm:query").Register("test:department", func(tx *gorm.DB) {
				reads++
				if dept, ok := tx.Statement.Dest.(*models.Department); ok {
					dept.Id, dept.CompanyID = 3, 2
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Create().After("gorm:create").Register("test:insert_result", func(tx *gorm.DB) {
				writes++
				tx.AddError(tc.err)
			}); err != nil {
				t.Fatal(err)
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set(middleware.ClaimsKey, &middleware.AuthClaims{CompanyID: 2})
			c.Request = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"badge_number":"B123","name":"Test User","jabatan":"Staff","id_department":3,"role":"KARYAWAN","password":"password123"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			CreateUser(db)(c)

			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body)
			}
			if reads != 1 || writes != 1 {
				t.Fatalf("reads = %d, writes = %d; want one department read and one insert", reads, writes)
			}
			if tc.want == http.StatusConflict && !strings.Contains(w.Body.String(), "No. Badge tersebut sudah terdaftar") {
				t.Fatalf("missing duplicate badge message: %s", w.Body)
			}
		})
	}
}
