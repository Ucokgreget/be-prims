package controllers

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDocumentUploadMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, filename, prefix, contentType, code, field        string
		fileSize, status                                        int
		badDate, missingDepartments, unknownLength, chunkedTail bool
	}{
		{name: "exact file limit with multipart overhead", filename: "dokumen.PDF", prefix: "%PDF-", fileSize: 25 << 20, status: 204},
		{name: "file over by one byte", filename: "dokumen.pdf", prefix: "%PDF-", fileSize: (25 << 20) + 1, status: 413, code: "file_too_large", field: "attachment"},
		{name: "whole request limit with length", filename: "dokumen.pdf", prefix: "%PDF-", fileSize: 26 << 20, status: 413, code: "request_too_large"},
		{name: "whole request limit without length", filename: "dokumen.pdf", prefix: "%PDF-", fileSize: 26 << 20, unknownLength: true, status: 413, code: "request_too_large"},
		{name: "whole request limit includes epilogue", filename: "dokumen.pdf", prefix: "%PDF-", fileSize: 26 << 20, unknownLength: true, chunkedTail: true, status: 413, code: "request_too_large"},
		{name: "unsupported content type", contentType: "application/json", status: 415, code: "unsupported_media_type"},
		{name: "missing boundary", contentType: "multipart/form-data", status: 400, code: "invalid_multipart"},
		{name: "malformed body", contentType: "multipart/form-data; boundary=wrong", status: 400, code: "invalid_multipart"},
		{name: "missing attachment", status: 400, code: "invalid_fields", field: "attachment"},
		{name: "missing departments", filename: "dokumen.pdf", prefix: "%PDF-", fileSize: 5, missingDepartments: true, status: 400, code: "invalid_fields", field: "department_id"},
		{name: "bad date", filename: "dokumen.pdf", prefix: "%PDF-", fileSize: 5, badDate: true, status: 400, code: "invalid_fields", field: "publication_date"},
		{name: "empty PDF", filename: "dokumen.pdf", status: 400, code: "empty_attachment", field: "attachment"},
		{name: "fake PDF", filename: "dokumen.pdf", prefix: "abcde", fileSize: 5, status: 415, code: "invalid_pdf", field: "attachment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var headers, footer bytes.Buffer
			w := multipart.NewWriter(&headers)
			date := "2026-01-01"
			if tc.badDate {
				date = "01/01/2026"
			}
			for _, field := range [][2]string{
				{"title", "Judul"}, {"description", "Isi"}, {"document_number", "1"}, {"publication_date", date},
				{"category_id", "1"}, {"tag_id", "2"}, {"is_private", "true"}, {"shared_to", "4"}, {"shared_to", "5"},
			} {
				if err := w.WriteField(field[0], field[1]); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.missingDepartments {
				for _, id := range []string{"6", "7"} {
					if err := w.WriteField("department_id", id); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.filename != "" {
				if _, err := w.CreateFormFile("attachment", tc.filename); err != nil {
					t.Fatal(err)
				}
			}
			start := bytes.Clone(headers.Bytes())
			headers.Reset()
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			footer.Write(headers.Bytes())
			padding := bytes.NewReader(make([]byte, tc.fileSize-len(tc.prefix)))
			var body io.Reader = io.MultiReader(bytes.NewReader(start), strings.NewReader(tc.prefix), padding, &footer)
			if tc.chunkedTail {
				// A valid small file followed by excess data after the closing boundary.
				body = io.MultiReader(bytes.NewReader(start), strings.NewReader(tc.prefix), &footer, padding)
			}
			r := httptest.NewRequest(http.MethodPost, "/", body)
			if !tc.unknownLength {
				r.ContentLength = int64(len(start) + tc.fileSize + footer.Len())
			}
			contentType := tc.contentType
			if contentType == "" {
				contentType = w.FormDataContentType()
			}
			r.Header.Set("Content-Type", contentType)
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = r
			input, ok := bindDocumentUpload(c)
			if r.MultipartForm != nil {
				t.Cleanup(func() { _ = r.MultipartForm.RemoveAll() })
			}
			if ok {
				c.Status(http.StatusNoContent)
				c.Writer.WriteHeaderNow()
				if input.Attachments.Size != int64(tc.fileSize) || !slices.Equal(input.DepartmentID, []uint{6, 7}) || !slices.Equal(input.SharedTo, []uint{4, 5}) {
					t.Fatalf("multipart file or repeated IDs did not survive binding: %+v", input)
				}
			}
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.status, response.Body)
			}
			if tc.code != "" {
				var body struct {
					Code   string
					Fields map[string]string
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != tc.code || (tc.field != "" && body.Fields[tc.field] == "") {
					t.Fatalf("want error %s field %s; body=%s", tc.code, tc.field, response.Body)
				}
			}
		})
	}
}
