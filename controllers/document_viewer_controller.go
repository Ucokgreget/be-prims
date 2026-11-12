package controllers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"be-prims/audit"
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/storage"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

// ---- BE-08: viewer PDF in-browser ----
//
// Backend tidak pernah mengirim direct public URL MinIO. Frontend meminta sesi
// viewer (token pendek + payload watermark), lalu menarik biner PDF dari
// endpoint stream yang mem-pipe object dari MinIO.

var wibZone = time.FixedZone("WIB", 7*60*60)

// Disimpan sebagai variabel agar handler stream bisa diuji tanpa MinIO hidup.
var (
	openObject = storage.Open
	statObject = storage.Stat
)

// viewWatermarkMeta: teks yang dirender frontend sebagai dynamic watermark.
type viewWatermarkMeta struct {
	CompanyName string `json:"company_name"`
	BadgeNumber string `json:"badge_number"`
	UserName    string `json:"user_name"`
	AccessTime  string `json:"access_time"`
	ClientIP    string `json:"client_ip"`
}

type viewSessionData struct {
	ViewToken     string            `json:"view_token"`
	ExpiresIn     int64             `json:"expires_in"`
	StreamURL     string            `json:"stream_url"`
	WatermarkMeta viewWatermarkMeta `json:"watermark_meta"`
}

// viewerIdentity: identitas peminta yang dipakai untuk otorisasi +
// pembentukan watermark, terlepas dari sumbernya (JWT API atau view token).
type viewerIdentity struct {
	UserID      uint
	Role        string
	CompanyID   uint
	Name        string
	BadgeNumber string
	ClientIP    string
}

// ---- POST /api/v1/portal/documents/:uuid/view-session ----

// CreateDocumentViewSession menerbitkan token sesi viewer (short-lived) beserta
// metadata watermark. Guard akses identik dengan endpoint detail (BE-07):
// 404 lintas tenant, 403 bila dokumen private dan user tidak di document_shared.
func CreateDocumentViewSession(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)

		document, ok := authorizePortalDocument(c, db, viewerIdentity{
			UserID:    claims.UserID,
			Role:      claims.Role,
			CompanyID: claims.CompanyID,
		})
		if !ok {
			return
		}

		if document.Attachment.Id == 0 {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Dokumen belum memiliki berkas PDF",
			})
			return
		}

		// Nama perusahaan diambil dari DB (bukan dari klaim) supaya perubahan
		// nama entitas langsung tercermin di watermark.
		var company models.Company
		if err := db.First(&company, claims.CompanyID).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		now := time.Now()
		clientIP := c.ClientIP()

		token, expiresIn, err := middleware.GenerateViewToken(middleware.ViewTokenClaims{
			DocumentUUID: document.UUID,
			UserID:       claims.UserID,
			CompanyID:    claims.CompanyID,
			Role:         claims.Role,
			BadgeNumber:  claims.BadgeNumber,
			UserName:     claims.Name,
			CompanyName:  company.Name,
			ClientIP:     clientIP,
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal membuat sesi viewer",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Sesi viewer dokumen berhasil dibuat",
			"data": viewSessionData{
				ViewToken: token,
				ExpiresIn: expiresIn,
				StreamURL: fmt.Sprintf("/api/v1/portal/documents/%s/stream", document.UUID),
				WatermarkMeta: viewWatermarkMeta{
					CompanyName: company.Name,
					BadgeNumber: claims.BadgeNumber,
					UserName:    claims.Name,
					AccessTime:  now.In(wibZone).Format("2006-01-02 15:04:05 MST"),
					ClientIP:    clientIP,
				},
			},
		})
	}
}

// ---- GET /api/v1/portal/documents/:uuid/stream ----

// StreamDocumentPdf mem-pipe biner PDF dari MinIO ke client.
//
// Autentikasi: ?view_token=<token viewer> (dipakai frontend/PDF.js karena
// iframe tidak bisa memasang header) ATAU Authorization: Bearer <jwt>.
// Identitas hanya dipakai untuk membentuk watermark; hak akses SELALU dicek
// ulang ke DB, sehingga share yang dicabut langsung berlaku walau token viewer
// belum kedaluwarsa.
func StreamDocumentPdf(db *gorm.DB, recorder *audit.Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := viewerIdentityFromRequest(c)
		if !ok {
			return
		}

		document, ok := authorizePortalDocument(c, db, identity)
		if !ok {
			return
		}

		key := storage.ObjectKeyFromFileUrl(document.Attachment.FilePath)
		if document.Attachment.Id == 0 || key == "" {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": "Dokumen belum memiliki berkas PDF",
			})
			return
		}

		ctx := c.Request.Context()

		// Stat dulu: dapat Content-Length + deteksi object hilang sebelum satu
		// byte pun dikirim (GetObject bersifat lazy, error baru muncul saat Read).
		info, err := statObject(ctx, key)
		if err != nil {
			if isObjectNotFound(err) {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
					"status":  "error",
					"message": "Berkas dokumen tidak ditemukan",
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}

		stream, err := openObject(ctx, key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return
		}
		defer stream.Close()

		header := c.Writer.Header()
		header.Set("Content-Type", "application/pdf")
		header.Set("Content-Disposition", `inline; filename="view.pdf"`)
		header.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
		header.Set("Pragma", "no-cache")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Length", fmt.Sprintf("%d", info.Size))

		c.Status(http.StatusOK)

		// Audit dicatat sebelum mengalirkan data: hak akses sudah terbukti di titik
		// ini, dan client yang memutus koneksi di tengah stream tetap terlacak
		// (penting untuk fitur watermark/forensik). Penulisan berjalan asinkron
		// sehingga tidak menambah latensi pembacaan PDF.
		recordDocumentAccess(recorder, document, identity.UserID, clientIP(c, identity), audit.ActionView)

		if _, err := io.Copy(c.Writer, stream); err != nil {
			// Header sudah terkirim sehingga status tidak bisa diubah lagi.
			logAccessFailure(document, identity, err)
		}
	}
}

// viewerIdentityFromRequest memprioritaskan view_token (query lalu header
// X-View-Token), dengan Bearer JWT sebagai fallback sesuai kontrak endpoint.
func viewerIdentityFromRequest(c *gin.Context) (viewerIdentity, bool) {
	tokenString := c.Query("view_token")
	if tokenString == "" {
		tokenString = c.GetHeader("X-View-Token")
	}

	if tokenString != "" {
		claims, err := middleware.ParseViewToken(tokenString)
		if err != nil {
			viewerUnauthorized(c, "Sesi viewer tidak valid atau telah kedaluwarsa")
			return viewerIdentity{}, false
		}
		// Token sesi terikat ke satu dokumen: tolak pemakaian silang.
		if claims.DocumentUUID != c.Param("uuid") {
			viewerUnauthorized(c, "Sesi viewer tidak berlaku untuk dokumen ini")
			return viewerIdentity{}, false
		}
		return viewerIdentity{
			UserID:      claims.UserID,
			Role:        claims.Role,
			CompanyID:   claims.CompanyID,
			Name:        claims.UserName,
			BadgeNumber: claims.BadgeNumber,
			ClientIP:    claims.ClientIP,
		}, true
	}

	header := c.GetHeader("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		claims, err := middleware.ParseToken(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			viewerUnauthorized(c, "Unauthorized")
			return viewerIdentity{}, false
		}
		return viewerIdentity{
			UserID:      claims.UserID,
			Role:        claims.Role,
			CompanyID:   claims.CompanyID,
			Name:        claims.Name,
			BadgeNumber: claims.BadgeNumber,
			ClientIP:    c.ClientIP(),
		}, true
	}

	viewerUnauthorized(c, "Unauthorized")
	return viewerIdentity{}, false
}

func viewerUnauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"status":  "error",
		"message": message,
	})
}

// authorizePortalDocument mencari dokumen milik tenant pemakai lalu menegakkan
// guard dokumen private. Meng-abort response dan mengembalikan ok=false bila
// akses ditolak.
func authorizePortalDocument(c *gin.Context, db *gorm.DB, identity viewerIdentity) (*models.Document, bool) {
	var document models.Document
	err := db.
		Preload("Attachment").
		Where("uuid = ? AND company_id = ?", c.Param("uuid"), identity.CompanyID).
		Take(&document).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"status":  "error",
			"message": "Dokumen tidak ditemukan",
		})
		return nil, false
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Ada yang salah",
		})
		return nil, false
	}

	if document.IsPrivate && identity.Role != models.RoleAdminAnper {
		allowed, err := isDocumentSharedWith(db, document.Id, identity.UserID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Ada yang salah",
			})
			return nil, false
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"status":  "error",
				"message": "Akses ditolak. Anda tidak memiliki izin untuk melihat dokumen rahasia ini.",
			})
			return nil, false
		}
	}

	return &document, true
}

// recordDocumentAccess mencatat jejak akses berkas ke document_access_logs.
//
// Isolasi error ada di dalam audit.Recorder: fungsi ini tidak pernah
// mengembalikan error dan tidak pernah menunggu DB, sehingga kegagalan audit
// tidak bisa menggagalkan pembacaan PDF. AccessedAt sengaja tidak diisi di
// sini: recorder yang mencapnya saat Record dipanggil (waktu server saat aksi
// terjadi), bukan saat worker sempat memproses antrean.
func recordDocumentAccess(recorder *audit.Recorder, document *models.Document, userID uint, ip, action string) {
	recorder.Record(models.DocumentAccessLog{
		DocumentID: document.Id,
		UserID:     userID,
		Action:     action,
		IpAddress:  ip,
	})
}

// ipUnknown: nilai pengganti bila alamat IP klien sama sekali tidak bisa
// ditentukan (RemoteAddr tidak terbaca). AC mewajibkan kolom tidak kosong;
// nilai ini eksplisit agar tidak salah dibaca sebagai IP asli.
const ipUnknown = "unknown"

// clientIP returns alamat IP klien untuk audit trail.
//
// Saat stream dipanggil memakai ?view_token=, IP yang tersimpan di token
// berasal dari sesi sebelumnya sehingga bisa basi (token dipakai ulang dari
// jaringan lain). Untuk forensik, IP koneksi yang sedang melayani stream lebih
// akurat; token hanya dipakai sebagai fallback bila request tidak menyediakan
// remote address (mis. unit test tanpa RemoteAddr).
//
// Nilai pengganti "unknown" dipakai bila keduanya kosong: AC mewajibkan kolom
// tidak kosong/null, dan menuliskan angka IP palsu akan merusak nilai forensik.
func clientIP(c *gin.Context, identity viewerIdentity) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	if identity.ClientIP != "" {
		return identity.ClientIP
	}
	fmt.Fprintf(gin.DefaultErrorWriter, "viewer: IP klien tidak terdeteksi untuk user %d\n", identity.UserID)
	return ipUnknown
}

func logAccessFailure(document *models.Document, identity viewerIdentity, err error) {
	fmt.Fprintf(gin.DefaultErrorWriter,
		"viewer: stream dokumen %s terputus untuk user %d: %v\n", document.UUID, identity.UserID, err)
}

// isObjectNotFound: minio.ToErrorResponse tidak melakukan unwrap, jadi error
// yang dibungkus (mis. fmt.Errorf %w) tetap ditangani lewat errors.As.
func isObjectNotFound(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.StatusCode == http.StatusNotFound
	}
	return false
}
