package controller

import (
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// extractJWTExp returns the "exp" claim as a Unix timestamp without verifying
// the signature. Only used for introspect reporting on tokens that were already
// verified by the caller.
func extractJWTExp(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	claims := &jwt.RegisteredClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(raw, claims)
	if err != nil || claims.ExpiresAt == nil {
		return 0
	}
	return claims.ExpiresAt.Unix()
}

// extractJWTIat returns the "iat" claim as a Unix timestamp without verifying
// the signature.
func extractJWTIat(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	claims := &jwt.RegisteredClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(raw, claims)
	if err != nil || claims.IssuedAt == nil {
		return 0
	}
	return claims.IssuedAt.Unix()
}
