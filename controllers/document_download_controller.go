package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"

	"be-prims/audit"
	"be-prims/middleware"
	"be-prims/models"
	"be-prims/pdfstamp"
	"be-prims/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- US-11-BE: GET /api/v1/portal/documents/:uuid/download ----

// maxDownloadBytes: batas atas buffer in-memory. Upload dibatasi 25 MB
// (document_controller.go); sisa ruang untuk overhead object lama.
const maxDownloadBytes = 32 << 20

const downloadNotFoundMessage = "Data tidak ditemukan"

// downloadLogTimeout membatasi INSERT log DOWNLOAD agar DB yang lambat tidak
// menahan pengiriman berkas tanpa batas.
const downloadLogTimeout = 3 * time.Second

// stampPdf disimpan sebagai variabel agar handler bisa diuji tanpa pdfcpu
// memproses PDF sungguhan.
var stampPdf = pdfstamp.Stamp

// DownloadDocumentPdf mengirim PDF yang sudah distempel watermark identitas
// pengunduh di pojok kanan atas setiap halaman.
//
// Urutan guard: tenant (404) → dokumen private (403) → allow_download (403).
// Guard private didahulukan agar user yang tidak berhak tidak bisa mengetahui
// pengaturan unduh dokumen rahasia.
//
// Berkas di MinIO hanya dibaca; stempel diproses di buffer memori. Jika
// stempel gagal, tidak ada fallback ke berkas asli (Data Loss Prevention).
//
// US-12-BE: setiap unduhan sukses mencatat satu baris DOWNLOAD di
// document_access_logs. Lihat recordDownload.
func DownloadDocumentPdf(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := middleware.Claims(c)
		if claims.Role != models.RoleAdminAnper && claims.Role != models.RoleKaryawan {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"status": "error", "message": "Tidak diizinkan untuk mengunduh dokumen"})
			return
		}

		document, ok := authorizePortalDocument(c, db, viewerIdentity{
			UserID:    claims.UserID,
			CompanyID: claims.CompanyID,
		}, downloadNotFoundMessage, true)
		if !ok {
			return
		}

		if !document.AllowDownload {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"status":  "error",
				"message": "Dokumen ini diatur sebagai 'Hanya Baca' (View Only) dan tidak diizinkan untuk diunduh.",
			})
			return
		}

		key := storage.ObjectKeyFromFileUrl(document.Attachment.FilePath)
		if document.Attachment.Id == 0 || key == "" {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"status":  "error",
				"message": downloadNotFoundMessage,
			})
			return
		}

		// Nama perusahaan dari DB (bukan klaim) agar perubahan nama anper
		// langsung tercermin di stempel.
		var company models.Company
		if err := db.WithContext(c.Request.Context()).First(&company, document.CompanyID).Error; err != nil {
			internalError(c)
			return
		}

		ctx := c.Request.Context()

		original, err := readObject(c, key)
		if err != nil {
			return
		}

		// Satu titik waktu untuk stempel DAN accessed_at log (US-12-BE): jam di
		// PDF yang bocor bisa dicocokkan persis dengan baris audit.
		downloadedAt := time.Now().UTC().Truncate(time.Second)

		var stamped bytes.Buffer
		stamped.Grow(len(original) + 16<<10)
		err = stampPdf(ctx, bytes.NewReader(original), &stamped, pdfstamp.Identity{
			BadgeNumber: claims.BadgeNumber,
			Name:        claims.Name,
			CompanyName: company.Name,
			DownloadAt:  downloadedAt.In(wibZone),
		})
		if err != nil {
			fmt.Fprintf(gin.DefaultErrorWriter,
				"download: stempel dokumen %s gagal untuk user %d: %v\n", document.UUID, claims.UserID, err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Gagal memproses berkas dokumen",
			})
			return
		}

		if err := recordDownload(ctx, db, models.DocumentAccessLog{
			DocumentID: document.Id,
			UserID:     claims.UserID,
			Action:     audit.ActionDownload,
			AccessedAt: downloadedAt,
			IpAddress:  c.ClientIP(),
		}); err != nil {
			log.Printf("audit: gagal mencatat log download untuk doc_id %d user %d: %v", document.Id, claims.UserID, err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Gagal mencatat audit unduhan"})
			return
		}

		header := c.Writer.Header()
		header.Set("Content-Type", "application/pdf")
		header.Set("Content-Disposition", attachmentDisposition(document.Title))
		header.Set("Content-Length", fmt.Sprintf("%d", stamped.Len()))
		header.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
		header.Set("Pragma", "no-cache")
		header.Set("X-Content-Type-Options", "nosniff")

		c.Status(http.StatusOK)

		if _, err := stamped.WriteTo(c.Writer); err != nil {
			fmt.Fprintf(gin.DefaultErrorWriter,
				"download: kirim dokumen %s terputus untuk user %d: %v\n", document.UUID, claims.UserID, err)
		}
	}
}

// recordDownload menyimpan baris DOWNLOAD secara sinkron (US-12-BE).
//
// Sengaja tidak memakai audit.Recorder milik VIEW: recorder itu men-debounce
// aksi yang sama per menit dan membuang entri saat antrean penuh, sedangkan AC
// mewajibkan SETIAP unduhan sukses tercatat.
//
// Dipanggil setelah seluruh guard lolos dan stempel selesai, tepat sebelum
// byte pertama dikirim: request yang ditolak (403/404) atau gagal diproses
// tidak pernah sampai ke sini. Context diturunkan dari request, sehingga bila
// klien membatalkan sebelum titik ini INSERT ikut batal dan tidak ada baris.
//
// Gagal mencatat berarti berkas tidak dikirim. Log membuktikan server
// mengizinkan pengiriman, bukan bahwa klien menyimpan seluruh berkas.
func recordDownload(ctx context.Context, db *gorm.DB, entry models.DocumentAccessLog) error {
	if entry.IpAddress == "" {
		entry.IpAddress = ipUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, downloadLogTimeout)
	defer cancel()
	return db.WithContext(ctx).Create(&entry).Error
}

// readObject membaca seluruh object MinIO ke memori. Meng-abort response dan
// mengembalikan error bila gagal.
func readObject(c *gin.Context, key string) ([]byte, error) {
	stream, err := openObject(c.Request.Context(), key)
	if err != nil {
		downloadObjectError(c, err)
		return nil, err
	}
	defer stream.Close()

	// GetObject MinIO bersifat lazy: NoSuchKey baru muncul saat Read.
	data, err := io.ReadAll(io.LimitReader(stream, maxDownloadBytes+1))
	if err != nil {
		downloadObjectError(c, err)
		return nil, err
	}
	if len(data) > maxDownloadBytes {
		internalError(c)
		return nil, fmt.Errorf("object %s melebihi %d byte", key, maxDownloadBytes)
	}
	return data, nil
}

func downloadObjectError(c *gin.Context, err error) {
	if isObjectNotFound(err) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"status": "error", "message": downloadNotFoundMessage})
		return
	}
	internalError(c)
}

func internalError(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"status":  "error",
		"message": "Ada yang salah",
	})
}

// attachmentDisposition memakai judul, bukan nama lampiran di storage.
func attachmentDisposition(title string) string {
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, title))
	if name == "" || name == "." || name == ".." {
		name = "dokumen"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
