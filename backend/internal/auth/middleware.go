package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Authenticator resolves bearer tokens into principals.
type Authenticator struct {
	tokens *TokenIssuer
	loader *PrincipalLoader
}

// NewAuthenticator returns an authenticator.
func NewAuthenticator(tokens *TokenIssuer, loader *PrincipalLoader) *Authenticator {
	return &Authenticator{tokens: tokens, loader: loader}
}

// Middleware attaches the principal for requests carrying a valid bearer token. Requests
// without a token continue anonymously; requests with an invalid token are rejected so a
// client with an expired token knows to refresh it.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			httpx.WriteError(w, r, apperr.Unauthorized("Authorization header must use the Bearer scheme."))
			return
		}
		userID, err := a.tokens.Parse(strings.TrimSpace(token))
		if err != nil {
			httpx.WriteError(w, r, apperr.New(http.StatusUnauthorized, "token_invalid", "Your session has expired. Please sign in again."))
			return
		}
		principal, err := a.loader.Load(r.Context(), userID)
		if errors.Is(err, ErrInactiveUser) {
			httpx.WriteError(w, r, apperr.New(http.StatusUnauthorized, "account_inactive", "This account is inactive."))
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "load principal", "error", err, "user_id", userID)
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

// RequireUser rejects anonymous requests.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()) == nil {
			httpx.WriteError(w, r, apperr.Unauthorized("Please sign in to continue."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAny rejects users who hold none of the permissions anywhere. Handlers still
// perform organiser-level checks once they know which organiser's data is involved.
func RequireAny(perms ...string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := FromContext(r.Context())
			for _, perm := range perms {
				if p.CanAny(perm) {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.WriteError(w, r, apperr.Forbidden("You do not have permission to perform this action."))
		}))
	}
}

// RequirePlatform rejects users who do not hold perm through a platform role.
func RequirePlatform(perm string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !FromContext(r.Context()).HasPlatform(perm) {
				httpx.WriteError(w, r, apperr.Forbidden("You do not have permission to perform this action."))
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// MustPrincipal returns the authenticated principal or an Unauthorized error.
func MustPrincipal(r *http.Request) (*Principal, error) {
	p := FromContext(r.Context())
	if p == nil {
		return nil, apperr.Unauthorized("Please sign in to continue.")
	}
	return p, nil
}

// ErrForbidden is the standard permission error.
func ErrForbidden() error {
	return apperr.Forbidden("You do not have permission to perform this action.")
}
