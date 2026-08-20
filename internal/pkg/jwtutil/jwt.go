// Package jwtutil JWT 签发/校验（HS256），参考 ~/ww/mianba/backend/auth.py。
package jwtutil

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
)

// Sign 签发 token，sub = userID。
func Sign(secret string, userID uint, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Parse 校验 token 并返回 userID。
func Parse(secret, tokenString string) (uint, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(secret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return 0, ErrExpiredToken
		}
		return 0, ErrInvalidToken
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return 0, ErrInvalidToken
	}
	sub, ok := claims["sub"].(float64)
	if !ok || sub <= 0 {
		return 0, ErrInvalidToken
	}
	return uint(sub), nil
}
