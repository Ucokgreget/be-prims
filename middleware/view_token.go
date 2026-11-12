package middleware

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ViewTokenTTL: umur token sesi viewer PDF. Sengaja pendek karena token ini
// ikut di URL stream (bisa tersimpan di log proxy / history browser).
const ViewTokenTTL = 10 * time.Minute

const (
	viewTokenAudience = "document-view"
	viewTokenPurpose  = "document_view"
	viewTokenIssuer   = "be-prims"
)

// ViewTokenClaims adalah payload token sesi viewer dokumen.
//
// Identity disimpan di dalam token (bukan hanya user_id) karena nilainya dipakai
// frontend untuk merender dynamic watermark, dan spec BE-08 meminta watermark
// diambil dari identitas saat sesi dibuat. Konsekuensinya perubahan nama/badge
// baru terlihat setelah sesi berikutnya (maks. ViewTokenTTL).
type ViewTokenClaims struct {
	DocumentUUID string `json:"doc_uuid"`
	UserID       uint   `json:"user_id"`
	CompanyID    uint   `json:"id_company"`
	Role         string `json:"role"`
	BadgeNumber  string `json:"badge_number"`
	UserName     string `json:"user_name"`
	CompanyName  string `json:"company_name"`
	ClientIP     string `json:"client_ip"`
	Purpose      string `json:"purpose"`
	jwt.RegisteredClaims
}

// viewTokenSecret memakai VIEW_TOKEN_SECRET bila diisi; kalau tidak, jatuh ke
// JWT_SECRET. Secret terpisah disarankan supaya token viewer yang bocor lewat
// URL/log tidak bisa dipakai memalsukan token API (lihat guard audience di
// ParseToken yang menutup celah ini meski secret-nya sama).
func viewTokenSecret() ([]byte, error) {
	if secret := os.Getenv("VIEW_TOKEN_SECRET"); secret != "" {
		return []byte(secret), nil
	}
	return jwtSecret()
}

func GenerateViewToken(claims ViewTokenClaims) (string, int64, error) {
	secret, err := viewTokenSecret()
	if err != nil {
		return "", 0, err
	}

	now := time.Now()
	claims.Purpose = viewTokenPurpose
	claims.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    viewTokenIssuer,
		Audience:  jwt.ClaimStrings{viewTokenAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ViewTokenTTL)),
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", 0, err
	}
	return token, int64(ViewTokenTTL / time.Second), nil
}

func ParseViewToken(tokenString string) (*ViewTokenClaims, error) {
	secret, err := viewTokenSecret()
	if err != nil {
		return nil, err
	}

	token, err := jwt.ParseWithClaims(tokenString, &ViewTokenClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("Signing method tidak valid")
		}
		return secret, nil
	},
		// Audience membedakan token viewer dari token API: token API tidak punya
		// audience "document-view" sehingga tidak lolos di sini, dan sebaliknya
		// (lihat ParseToken).
		jwt.WithAudience(viewTokenAudience),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*ViewTokenClaims)
	if !ok || claims.Purpose != viewTokenPurpose {
		return nil, errors.New("claims tidak valid")
	}
	return claims, nil
}
