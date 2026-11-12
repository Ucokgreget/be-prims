package routes

import (
	"be-prims/audit"
	"be-prims/controllers"
	"be-prims/middleware"
	"be-prims/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(router *gin.Engine, db *gorm.DB, accessLog *audit.Recorder) {
	api := router.Group("/api/v1")

	api.GET("/company", controllers.GetAllCompany(db))
	api.GET("/company/:id", controllers.GetCompanyByID(db))

	auth := api.Group("/auth")
	auth.POST("/login", controllers.Login(db))
	auth.GET("/me", middleware.AuthMiddleware(), controllers.Me(db))

	// User management — ADMIN_ANPER only, tenant-scoped
	admin := api.Group("",
		middleware.AuthMiddleware(),
		middleware.RequireRole(models.RoleAdminAnper),
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
	portal := api.Group("/portal", middleware.AuthMiddleware())
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
}
