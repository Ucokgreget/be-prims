package routes

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"be-prims/audit"
	"be-prims/controllers"
	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(router *gin.Engine, db *gorm.DB, accessLog *audit.Recorder) error {
	if origin := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGIN")); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			origin != u.Scheme+"://"+u.Host || strings.Contains(u.Host, "*") {
			return errors.New("CORS_ALLOWED_ORIGIN harus satu origin HTTP(S) tanpa path, trailing slash, atau wildcard")
		}
		// Pasang di engine agar preflight ditangani sebelum Gin mengembalikan 404.
		router.Use(func(c *gin.Context) {
			c.Writer.Header().Add("Vary", "Origin")
			if c.GetHeader("Origin") == origin {
				c.Header("Access-Control-Allow-Origin", origin)
				if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
					c.Header("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE")
					c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
					c.AbortWithStatus(http.StatusNoContent)
					return
				}
			}
			c.Next()
		})
	}
	api := router.Group("/api/v1")

	api.GET("/company", controllers.GetAllCompany(db))
	api.GET("/company/:id", controllers.GetCompanyByID(db))

	auth := api.Group("/auth")
	auth.POST("/login", controllers.Login(db))
	auth.GET("/me", middleware.AuthMiddleware(), middleware.RequireActiveCompany(db), controllers.Me(db))

	superadmin := api.Group("/superadmin", middleware.AuthMiddleware(), middleware.RequireRole(models.RoleSuperAdmin))
	superadmin.GET("/companies", controllers.ListCompanies(db))
	superadmin.POST("/companies", controllers.CreateCompany(db))
	superadmin.PUT("/companies/:id", controllers.UpdateCompany(db))
	superadmin.PATCH("/companies/:id/status", controllers.UpdateCompanyStatus(db))

	superadmin.GET("/anper-admins", controllers.GetAllAdminAnper(db))
	superadmin.POST("/anper-admins", controllers.CreateAdminAnper(db))
	superadmin.PATCH("/anper-admins/:id/reset-password", controllers.AdminAnperResetPassword(db))
	superadmin.PATCH("/anper-admins/:id/status", controllers.UpdateAdminAnperStatus(db))

	superadmin.GET("/dashboard-stats", controllers.GetDashboardStatistic(db))
	superadmin.GET("/audit-logs", controllers.GetAuditLogs(db))

	// User management — ADMIN_ANPER only, tenant-scoped
	admin := api.Group("",
		middleware.AuthMiddleware(),
		middleware.RequireRole(models.RoleAdminAnper),
		middleware.RequireActiveCompany(db),
	)
	admin.GET("/users", controllers.ListUsers(db))
	admin.POST("/users", controllers.CreateUser(db))
	admin.PATCH("/users/:id/status", controllers.UpdateUserStatus(db))
	admin.GET("/departments", controllers.ListDepartments(db))

	admin.GET("/category", controllers.GetAllCategory(db))
	admin.GET("/category/:id", controllers.GetCategoryById(db))
	admin.POST("/category", controllers.CreateCategory(db))
	admin.PATCH("/category/:id", controllers.UpdateCategory(db))
	admin.DELETE("/category/:id", controllers.DeleteCategory(db))

	admin.POST("/document", controllers.CreateDocument(db))
	admin.GET("/document", controllers.GetAllDocumentByCompany(db))
	admin.GET("/document/:id", controllers.GetDocumentByIdByCompany(db))

	// Endpoint tiket Manajemen Dokumen Admin (mockup hal. 6 & 7)
	admin.GET("/documents", controllers.GetAllDocuments(db))
	// US-13-BE: data panel "Views" & "Views & Downloads" (mockup hal. 6 & 7).
	api.GET("/documents/:id/access-logs", middleware.AuthMiddleware(), middleware.RequireActiveCompany(db), controllers.GetDocumentAccessLogs(db))
	admin.GET("/admin/audit-logs", controllers.GetCompanyAuditLogs(db))

	admin.GET("/tags", controllers.GetTags(db))
	admin.POST("/tags", controllers.CreateTag(db))

	admin.GET("/users/by-jabatan", controllers.GetUsersByJabatan(db))

	// BE-08: stream biner PDF TIDAK memakai AuthMiddleware karena frontend
	// (iframe/PDF.js) mengirim kredensial lewat ?view_token=; handler stream
	// sendiri yang memvalidasi view token atau Bearer JWT.
	// US-10-BE: stream mencatat satu baris VIEW ke document_access_logs secara
	// asinkron (debounce 1 menit per dokumen+user) sebelum PDF dialirkan.
	api.GET("/portal/documents/:uuid/stream", controllers.StreamDocumentPdf(db, accessLog))

	// Portal publikasi karyawan internal — semua role yang login
	portal := api.Group("/portal", middleware.AuthMiddleware(), middleware.RequireActiveCompany(db))
	portal.GET("/categories", controllers.GetPortalCategories(db))
	portal.GET("/documents", controllers.GetPortalDocuments(db))
	// Didaftarkan berdampingan dengan ":uuid": Gin memprioritaskan segmen statis,
	// jadi "shared-with-me" tidak pernah jatuh ke handler detail.
	portal.GET("/documents/shared-with-me", controllers.GetSharedWithMeDocuments(db))
	portal.GET("/documents/:uuid", controllers.GetPortalDocumentDetail(db))
	portal.POST("/documents/:uuid/view-session", controllers.CreateDocumentViewSession(db))
	// US-11-BE: unduh PDF berstempel watermark identitas pengunduh (in-memory).
	// US-12-BE: setiap unduhan sukses mencatat satu baris DOWNLOAD secara
	// sinkron (tanpa debounce recorder VIEW).
	portal.GET("/documents/:uuid/download", controllers.DownloadDocumentPdf(db))
	return nil
}
