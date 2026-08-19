package auth

import (
	"time"
)

type JWTTokenValidationFilter struct {
	SigningKey     []byte
	Issuer         string
	ExpirationTime time.Duration
}

func (f *JWTTokenValidationFilter) GenerateSignedSessionClaims(userId string, roles []string) (string, error) {
	return "mock.jwt.signed.payload", nil
}
