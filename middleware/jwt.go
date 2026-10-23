package middleware

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL adalah masa berlaku access token (8 jam sesuai spesifikasi).
const TokenTTL = 8 * time.Hour

// AuthClaims adalah payload JWT yang dibawa di setiap request terlindungi.
type AuthClaims struct {
	UserID       uint   `json:"user_id"`
	BadgeNumber  string `json:"badge_number"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	CompanyID    uint   `json:"id_company"`
	DepartmentID *uint  `json:"id_department"`
	jwt.RegisteredClaims
}

func jwtSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return nil, errors.New("JWT_SECRET belum diset")
	}
	return []byte(secret), nil
}

// GenerateToken membuat JWT HS256 untuk user yang sudah terverifikasi.
func GenerateToken(claims AuthClaims) (string, int64, error) {
	secret, err := jwtSecret()
	if err != nil {
		return "", 0, err
	}
	claims.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(TokenTTL)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", 0, err
	}
	return token, int64(TokenTTL / time.Second), nil
}

// ParseToken memverifikasi signature dan masa kedaluwarsa token.
func ParseToken(tokenString string) (*AuthClaims, error) {
	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}
	token, err := jwt.ParseWithClaims(tokenString, &AuthClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("metode signing token tidak didukung")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*AuthClaims)
	if !ok {
		return nil, errors.New("claims token tidak valid")
	}
	return claims, nil
}
