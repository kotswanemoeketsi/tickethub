package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
)

const (
	tokenIssuer   = "gotaticket"
	tokenAudience = "gotaticket-api"
)

// ErrInvalidToken is returned for any access token that fails validation.
var ErrInvalidToken = errors.New("invalid access token")

// TokenIssuer issues and validates short-lived HS256 access tokens. Permissions are not
// embedded in the token: they are loaded per request so role changes and deactivations
// take effect immediately.
type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
	now    clock.Func
}

// NewTokenIssuer returns an issuer signing with secret.
func NewTokenIssuer(secret string, ttl time.Duration, now clock.Func) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), ttl: ttl, now: now}
}

// Issue creates an access token for userID.
func (t *TokenIssuer) Issue(userID int64) (string, time.Time, error) {
	now := t.now()
	expires := now.Add(t.ttl)
	claims := jwt.RegisteredClaims{
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{tokenAudience},
		Subject:   strconv.FormatInt(userID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expires, nil
}

// Parse validates token and returns the user ID it was issued to.
func (t *TokenIssuer) Parse(token string) (int64, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return 0, ErrInvalidToken
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		return 0, ErrInvalidToken
	}
	return userID, nil
}
