package controller

import (
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// extractJWTSubject returns the "sub" claim from a JWT without verifying the
// signature. Used for advisory logout where the token is already trusted by
// the client.
func extractJWTSubject(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	claims := &jwt.RegisteredClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(raw, claims)
	if err != nil {
		return 0
	}
	sub, err := strconv.Atoi(claims.Subject)
	if err != nil {
		return 0
	}
	return sub
}

// extractJWTClientID returns the "aud" (audience) claim from a JWT without
// verifying the signature. Used for logout hint.
func extractJWTClientID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	claims := &jwt.RegisteredClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(raw, claims)
	if err != nil || len(claims.Audience) == 0 {
		return ""
	}
	return claims.Audience[0]
}

// extractJWTExp returns the "exp" claim as a Unix timestamp without verifying
// the signature.
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
