package middleware

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TenantDB mengembalikan *gorm.DB dengan WHERE company_id = companyID pemanggil
// untuk model yang memiliki kolom company_id. Wajib ada claims dari AuthMiddleware.
// Model tanpa company_id, serta query Raw/Exec/Table, tidak di-scope.
func TenantDB(c *gin.Context, db *gorm.DB) *gorm.DB {
	claims := Claims(c)
	// ponytail: Context + NewDB: query ikut cancel/timeout request; NewDB untuk fresh
	// Statement per query — mencegah scope leak antar-request.
	return db.Session(&gorm.Session{NewDB: true, Context: c.Request.Context()}).
		Scopes(func(tx *gorm.DB) *gorm.DB {
			model := tx.Statement.Model
			if model == nil {
				model = tx.Statement.Dest // Find(&slice)/Delete(&m) tanpa .Model() tetap ke-filter
			}
			if model == nil {
				return tx
			}
			if err := tx.Statement.Parse(model); err != nil {
				return tx
			}
			if _, ok := tx.Statement.Schema.FieldsByDBName["company_id"]; !ok {
				return tx
			}
			return tx.Where("company_id = ?", claims.CompanyID)
		})
}
