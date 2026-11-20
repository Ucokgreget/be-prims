package controllers

import (
	"be-prims/request"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

const (
	maxDocumentPDFBytes  = 25 << 20
	maxDocumentBodyBytes = maxDocumentPDFBytes + 1<<20 // Ruang untuk field dan overhead multipart.
)

func documentUploadError(c *gin.Context, status int, code, message string, fields gin.H) bool {
	body := gin.H{"status": "error", "code": code, "message": message}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	c.AbortWithStatusJSON(status, body)
	return false
}

func bindDocumentUpload(c *gin.Context) (input request.CreateDocumentRequest, ok bool) {
	r := c.Request
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != binding.MIMEMultipartPOSTForm {
		return input, documentUploadError(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "Gunakan Content-Type multipart/form-data.", nil)
	}
	if params["boundary"] == "" {
		return input, documentUploadError(c, http.StatusBadRequest, "invalid_multipart", "Boundary multipart tidak ditemukan.", nil)
	}

	r.Body = http.MaxBytesReader(c.Writer, r.Body, maxDocumentBodyBytes)
	if r.ContentLength > maxDocumentBodyBytes {
		return input, documentUploadError(c, http.StatusRequestEntityTooLarge, "request_too_large", "Total unggahan (berkas dan data form) melebihi 26 MiB.", nil)
	}
	if err = r.ParseMultipartForm(2 << 20); err == nil {
		// A multipart reader can stop at the closing boundary before the HTTP body ends.
		_, err = io.Copy(io.Discard, r.Body)
	}
	if err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return input, documentUploadError(c, http.StatusRequestEntityTooLarge, "request_too_large", "Total unggahan (berkas dan data form) melebihi 26 MiB.", nil)
		}
		if errors.Is(err, multipart.ErrMessageTooLarge) {
			return input, documentUploadError(c, http.StatusRequestEntityTooLarge, "multipart_too_large", "Data form multipart terlalu besar atau memiliki terlalu banyak bagian.", nil)
		}
		if _, diskError := errors.AsType[*os.PathError](err); diskError {
			return input, documentUploadError(c, http.StatusInternalServerError, "upload_read_failed", "Berkas unggahan tidak dapat diproses.", nil)
		}
		return input, documentUploadError(c, http.StatusBadRequest, "invalid_multipart", "Data multipart rusak atau tidak lengkap.", nil)
	}

	if len(r.MultipartForm.File["attachment"]) > 1 {
		return input, documentUploadError(c, http.StatusBadRequest, "invalid_fields", "Kirim satu berkas PDF.", gin.H{"attachment": "Hanya satu berkas yang diizinkan."})
	}
	if err := c.ShouldBindWith(&input, binding.FormMultipart); err != nil {
		if _, invalidDate := errors.AsType[*time.ParseError](err); invalidDate {
			return input, documentUploadError(c, http.StatusBadRequest, "invalid_fields", "Format tanggal tidak valid.", gin.H{"publication_date": "Gunakan format YYYY-MM-DD."})
		}
		return input, documentUploadError(c, http.StatusBadRequest, "invalid_fields", "Format data form tidak valid. Gunakan angka untuk ID dan true/false untuk is_private atau allow_download.", nil)
	}
	if len(input.SharedTo) == 1 && input.SharedTo[0] == 0 {
		input.SharedTo = nil
	}
	if err := validate.Struct(&input); err != nil {
		fields := gin.H{}
		for _, issue := range err.(validator.ValidationErrors) {
			name, _, _ := strings.Cut(issue.Field(), "[")
			field, _ := reflect.TypeFor[request.CreateDocumentRequest]().FieldByName(name)
			message := "Nilai tidak valid."
			if issue.Tag() == "required" && name == issue.Field() {
				message = "Wajib diisi."
			}
			fields[field.Tag.Get("form")] = message
		}
		return input, documentUploadError(c, http.StatusBadRequest, "invalid_fields", "Periksa data form unggahan.", fields)
	}
	if !input.IsPrivate {
		input.SharedTo = nil
	}

	file := input.Attachments
	if file.Size > maxDocumentPDFBytes {
		return input, documentUploadError(c, http.StatusRequestEntityTooLarge, "file_too_large", "Ukuran PDF melebihi batas maksimal 25 MiB.", gin.H{"attachment": "Maksimal 25 MiB."})
	}
	if file.Size == 0 {
		return input, documentUploadError(c, http.StatusBadRequest, "empty_attachment", "Berkas PDF kosong.", gin.H{"attachment": "Pilih berkas PDF yang tidak kosong."})
	}
	if !strings.EqualFold(filepath.Ext(file.Filename), ".pdf") {
		return input, documentUploadError(c, http.StatusUnsupportedMediaType, "invalid_pdf", "Berkas harus berekstensi .pdf.", gin.H{"attachment": "Gunakan berkas PDF."})
	}
	f, err := file.Open()
	if err != nil {
		return input, documentUploadError(c, http.StatusInternalServerError, "upload_read_failed", "Berkas unggahan tidak dapat dibaca.", nil)
	}
	defer f.Close()
	// ponytail: cek signature PDF, bukan integritas seluruh dokumen; gunakan parser jika perlu validasi struktur.
	var header [5]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || string(header[:]) != "%PDF-" {
		return input, documentUploadError(c, http.StatusUnsupportedMediaType, "invalid_pdf", "Isi berkas tidak memiliki signature PDF.", gin.H{"attachment": "Gunakan berkas PDF yang valid."})
	}
	return input, true
}
