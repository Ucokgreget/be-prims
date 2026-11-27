package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLoginCORS(t *testing.T) {
	const local = "http://localhost:3000"
	const prod = "https://portal.example.com"
	for _, tc := range []struct {
		name, configured, method, origin, allowedOrigin string
		status                                          int
	}{
		{"local preflight", local, http.MethodOptions, local, local, http.StatusNoContent},
		{"prod preflight", prod, http.MethodOptions, prod, prod, http.StatusNoContent},
		{"localhost not allowed in prod", prod, http.MethodOptions, local, "", http.StatusNotFound},
		{"CORS disabled", "", http.MethodOptions, local, "", http.StatusNotFound},
		{"login reaches handler", local, http.MethodPost, local, local, http.StatusBadRequest},
		{"same-origin login still works", "", http.MethodPost, "", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CORS_ALLOWED_ORIGIN", tc.configured)
			router := gin.New()
			if err := SetupRouter(router, nil, nil); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.method, "/api/v1/auth/login", strings.NewReader("{}"))
			req.Header.Set("Origin", tc.origin)
			if tc.method == http.MethodOptions {
				req.Header.Set("Access-Control-Request-Method", "POST")
				req.Header.Set("Access-Control-Request-Headers", "content-type")
			} else {
				req.Header.Set("Content-Type", "application/json")
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if got := res.Header().Get("Access-Control-Allow-Origin"); res.Code != tc.status || got != tc.allowedOrigin {
				t.Fatalf("status = %d, allowed origin = %q; want %d, %q", res.Code, got, tc.status, tc.allowedOrigin)
			}
			if res.Code == http.StatusNoContent &&
				(!strings.Contains(res.Header().Get("Access-Control-Allow-Methods"), "POST") ||
					!strings.Contains(strings.ToLower(res.Header().Get("Access-Control-Allow-Headers")), "content-type")) {
				t.Fatalf("preflight tidak mengizinkan POST JSON: %v", res.Header())
			}
		})
	}
}

func TestInvalidCORSOriginConfig(t *testing.T) {
	for _, origin := range []string{"*", "null", "https://*.example.com", "https://portal.example.com/", "https://user@portal.example.com"} {
		t.Setenv("CORS_ALLOWED_ORIGIN", origin)
		if err := SetupRouter(gin.New(), nil, nil); err == nil {
			t.Errorf("accepted invalid CORS origin %q", origin)
		}
	}
}
