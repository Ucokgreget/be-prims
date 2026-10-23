package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TenantMiddleware membatasi seluruh query pada perusahaan (Anper) pemanggil.
// Wajib dipasang SETELAH AuthMiddleware agar claims tersedia.
func TenantMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("db_tenant", TenantDB(c, db))
		c.Next()
	}
}

// TenantDB mengembalikan *gorm.DB dengan WHERE id_company = companyID pemanggil
// untuk model yang memiliki kolom company_id.
func TenantDB(c *gin.Context, db *gorm.DB) *gorm.DB {
	claims := Claims(c)
	// ponytail: NewDB: true untuk fresh Statement per query — mencegah scope leak antar-request.
	return db.Session(&gorm.Session{NewDB: true}).Scopes(func(tx *gorm.DB) *gorm.DB {
		if tx.Statement.Model == nil {
			return tx
		}
		if err := tx.Statement.Parse(tx.Statement.Model); err != nil {
			return tx
		}
		if _, ok := tx.Statement.Schema.FieldsByDBName["company_id"]; !ok {
			return tx // ponytail: model tanpa company_id tidak di-scope; tambahkan kolomnya bila perlu tenant
		}
		return tx.Where("company_id = ?", claims.CompanyID)
	})
}

// TenantModelDB adalah helper untuk query resource milik tenant di handler,
// tanpa mengambil dari context (dipakai bersama TenantMiddleware).
func TenantModelDB(c *gin.Context) *gorm.DB {
	if scoped, ok := c.Get("db_tenant"); ok {
		return scoped.(*gorm.DB)
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"status":  "error",
		"message": "Konteks tenant tidak tersedia",
	})
	return nil
}
