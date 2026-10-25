package middleware

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const TokenTTL = 8 * time.Hour

type AuthClaims struct {
	UserID       uint   `json:"user_id"`
	Name         string `json:"name"`
	BadgeNumber  string `json:"badge_number"`
	Role         string `json:"role"`
	CompanyID    uint   `json:"id_company"`
	DepartmentID *uint  `json:"id_department"`
	jwt.RegisteredClaims
}

func jwtSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return nil, errors.New("Tidak ada JWT_SECRET")
	}

	return []byte(secret), nil
}

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

func ParseToken(tokenString string) (*AuthClaims, error) {
	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}

	token, err := jwt.ParseWithClaims(tokenString, &AuthClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("Signing method tidak valid")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*AuthClaims)
	if !ok {
		return nil, errors.New("claims tidak valid")
	}
	return claims, nil
}
