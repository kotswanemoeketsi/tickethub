package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Sign-in with Google uses the OpenID Connect authorization code flow with PKCE:
//
//  1. StartGoogle stores a random state, nonce and PKCE verifier and returns Google's
//     consent URL. The state is also set in a cookie, binding the flow to this browser.
//  2. Google sends the browser back to the callback with a one-time code.
//  3. FinishGoogle checks the state, exchanges the code (with the client secret and the
//     PKCE verifier) directly with Google over TLS, and validates the ID token's issuer,
//     audience, expiry and nonce. Because the token comes straight from Google's token
//     endpoint, OpenID Connect does not require checking its signature (Core §3.1.3.7).
//  4. The Google account is matched to a GotATicket account (see loginWithGoogle) and a
//     normal session is issued.

const (
	ProviderGoogle    = "google"
	googleStateTTL    = 10 * time.Minute
	googleHTTPLimit   = 1 << 20
	defaultAuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURL   = "https://oauth2.googleapis.com/token"
	googleStatePrefix = "oauth:google:"
)

var googleIssuers = map[string]bool{"https://accounts.google.com": true, "accounts.google.com": true}

// GoogleConfig configures sign-in with Google. It is disabled when ClientID is empty.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // https://<site>/api/v1/auth/google/callback
	AuthURL      string // overridable for tests
	TokenURL     string
	HTTPClient   *http.Client
}

// Enabled reports whether Google sign-in is configured.
func (c GoogleConfig) Enabled() bool { return c.ClientID != "" && c.ClientSecret != "" }

// EnableGoogle turns on sign-in with Google. store keeps the short-lived login state; it
// must be shared by every API instance (Redis) when more than one runs.
func (s *Service) EnableGoogle(cfg GoogleConfig, store cache.Cache) {
	if !cfg.Enabled() {
		return
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = defaultAuthURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = defaultTokenURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	s.google = &cfg
	s.oauthStore = store
}

// GoogleEnabled reports whether the Google button should be offered.
func (s *Service) GoogleEnabled() bool { return s.google != nil }

type googleState struct {
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
	Next     string `json:"next"`
}

// ErrSSO is returned for any failed external sign-in. Code is safe to show in a URL.
type ErrSSO struct{ Code string }

func (e *ErrSSO) Error() string { return "sign-in with Google failed: " + e.Code }

// StartGoogle begins a Google sign-in and returns the URL to send the browser to and the
// state value to bind to the browser in a cookie.
func (s *Service) StartGoogle(ctx context.Context, next string) (redirect, state string, err error) {
	if s.google == nil {
		return "", "", apperr.NotFound("Sign-in provider")
	}
	st := googleState{Verifier: random.Token(48), Nonce: random.Token(24), Next: SafeNext(next)}
	state = random.Token(24)
	if err := cache.SetJSON(ctx, s.oauthStore, googleStatePrefix+state, st, googleStateTTL); err != nil {
		return "", "", err
	}
	challenge := sha256.Sum256([]byte(st.Verifier))
	q := url.Values{
		"client_id":             {s.google.ClientID},
		"redirect_uri":          {s.google.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {st.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return s.google.AuthURL + "?" + q.Encode(), state, nil
}

// FinishGoogle completes a Google sign-in. It returns the new session and the in-app path
// to continue to.
func (s *Service) FinishGoogle(ctx context.Context, state, cookieState, code string) (*Session, string, error) {
	if s.google == nil {
		return nil, "/", &ErrSSO{Code: "unavailable"}
	}
	// The state must match the cookie set when this browser started the flow; otherwise
	// someone could make a victim's browser complete a sign-in the attacker started.
	if state == "" || code == "" || cookieState == "" || state != cookieState {
		return nil, "/", &ErrSSO{Code: "invalid_state"}
	}
	var st googleState
	ok, err := cache.GetJSON(ctx, s.oauthStore, googleStatePrefix+state, &st)
	if err != nil {
		return nil, "/", err
	}
	if !ok {
		return nil, "/", &ErrSSO{Code: "expired"}
	}
	_ = s.oauthStore.Delete(ctx, googleStatePrefix+state) // one use only

	claims, err := s.exchangeGoogleCode(ctx, code, st)
	if err != nil {
		s.logger.WarnContext(ctx, "google sign-in rejected", "error", err)
		return nil, st.Next, &ErrSSO{Code: "verification_failed"}
	}
	session, err := s.loginWithGoogle(ctx, claims)
	return session, st.Next, err
}

type googleClaims struct {
	Issuer        string          `json:"iss"`
	Audience      json.RawMessage `json:"aud"`
	Expires       int64           `json:"exp"`
	Nonce         string          `json:"nonce"`
	Subject       string          `json:"sub"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	GivenName     string          `json:"given_name"`
	FamilyName    string          `json:"family_name"`
	Name          string          `json:"name"`
}

func (c googleClaims) audienceIs(clientID string) bool {
	var one string
	if json.Unmarshal(c.Audience, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(c.Audience, &many) == nil {
		for _, a := range many {
			if a == clientID {
				return true
			}
		}
	}
	return false
}

// emailVerified accepts both true and "true", as Google has used both.
func (c googleClaims) emailVerified() bool {
	v := strings.Trim(string(c.EmailVerified), `"`)
	return v == "true"
}

func (s *Service) exchangeGoogleCode(ctx context.Context, code string, st googleState) (googleClaims, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {s.google.ClientID},
		"client_secret": {s.google.ClientSecret},
		"redirect_uri":  {s.google.RedirectURL},
		"code_verifier": {st.Verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.google.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := s.google.HTTPClient.Do(req)
	if err != nil {
		return googleClaims{}, fmt.Errorf("token request: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, googleHTTPLimit))
	if err != nil {
		return googleClaims{}, err
	}
	if res.StatusCode != http.StatusOK {
		return googleClaims{}, fmt.Errorf("token endpoint returned %d", res.StatusCode)
	}
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.IDToken == "" {
		return googleClaims{}, errors.New("token response has no id_token")
	}
	parts := strings.Split(tokens.IDToken, ".")
	if len(parts) != 3 {
		return googleClaims{}, errors.New("malformed id_token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return googleClaims{}, fmt.Errorf("decode id_token: %w", err)
	}
	var c googleClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return googleClaims{}, fmt.Errorf("parse id_token: %w", err)
	}
	switch {
	case !googleIssuers[c.Issuer]:
		return c, fmt.Errorf("unexpected issuer %q", c.Issuer)
	case !c.audienceIs(s.google.ClientID):
		return c, errors.New("id_token was issued to another client")
	case s.now().Unix() >= c.Expires:
		return c, errors.New("id_token has expired")
	case c.Nonce != st.Nonce:
		return c, errors.New("nonce mismatch")
	case c.Subject == "":
		return c, errors.New("id_token has no subject")
	}
	return c, nil
}

// loginWithGoogle finds or creates the account for a Google identity:
//
//   - a Google account already linked signs in to its GotATicket account;
//   - otherwise, if the Google email is verified and belongs to an existing account, the
//     two are linked. If that account's owner had never proved control of the address,
//     its password and sessions are cleared, so that nobody who registered the address
//     beforehand (without owning it) keeps access;
//   - otherwise a new customer account is created.
func (s *Service) loginWithGoogle(ctx context.Context, c googleClaims) (*Session, error) {
	email := strings.ToLower(strings.TrimSpace(c.Email))
	if email == "" || !c.emailVerified() {
		return nil, &ErrSSO{Code: "email_unverified"}
	}
	first, last := splitName(c)

	var userID int64
	var status string
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		err := tx.QueryRowContext(ctx, `SELECT u.id, u.status FROM user_identities i JOIN users u ON u.id = i.user_id
			WHERE i.provider = ? AND i.subject = ? FOR UPDATE`, ProviderGoogle, c.Subject).Scan(&userID, &status)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, `UPDATE user_identities SET email = ?, last_used_at = ? WHERE provider = ? AND subject = ?`,
				email, now, ProviderGoogle, c.Subject)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		var verifiedAt *time.Time
		err = tx.QueryRowContext(ctx, `SELECT id, status, email_verified_at FROM users WHERE email = ? FOR UPDATE`, email).Scan(&userID, &status, &verifiedAt)
		switch {
		case err == nil:
			if verifiedAt == nil {
				unusable, err := s.hasher.Hash(random.Token(32))
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`,
					unusable, now, now, userID); err != nil {
					return err
				}
				if err := s.revokeAllSessions(ctx, tx, userID); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, ?) WHERE id = ?`, now, userID); err != nil {
				return err
			}
			if err := s.linkIdentity(ctx, tx, userID, c.Subject, email); err != nil {
				return err
			}
			return audit.Record(ctx, tx, now, audit.Entry{Action: "user.identity_linked", EntityType: "user", EntityID: userID,
				Summary: "Google account linked for " + email})
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		// New account. It has no usable password until the owner sets one with "Forgot password".
		unusable, err := s.hasher.Hash(random.Token(32))
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO users (email, password_hash, first_name, last_name, phone, status, email_verified_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', 'active', ?, ?, ?)`, email, unusable, first, last, now, now, now)
		if err != nil {
			return err
		}
		userID, _ = res.LastInsertId()
		status = "active"
		customerRole, err := s.roleIDBySlug(ctx, tx, rbac.RoleCustomer)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)`, userID, customerRole, now); err != nil {
			return err
		}
		if err := s.linkIdentity(ctx, tx, userID, c.Subject, email); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user.registered", EntityType: "user", EntityID: userID,
			Summary: "Customer account registered with Google: " + email})
	})
	if err != nil {
		return nil, err
	}
	if status != "active" {
		return nil, &ErrSSO{Code: "account_inactive"}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, s.now(), userID); err != nil {
		return nil, err
	}
	s.loader.Invalidate(ctx, userID)
	return s.issueSession(ctx, userID, "")
}

func (s *Service) linkIdentity(ctx context.Context, tx *sql.Tx, userID int64, subject, email string) error {
	now := s.now()
	_, err := tx.ExecContext(ctx, `INSERT INTO user_identities (user_id, provider, subject, email, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, ProviderGoogle, subject, email, now, now)
	return err
}

func splitName(c googleClaims) (first, last string) {
	first, last = strings.TrimSpace(c.GivenName), strings.TrimSpace(c.FamilyName)
	if first == "" && last == "" {
		if f, l, ok := strings.Cut(strings.TrimSpace(c.Name), " "); ok {
			first, last = f, strings.TrimSpace(l)
		} else {
			first = strings.TrimSpace(c.Name)
		}
	}
	if first == "" {
		first = strings.Split(c.Email, "@")[0]
	}
	return truncateRunes(first, 100), truncateRunes(last, 100)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// SafeNext keeps redirects on this site: only absolute paths are allowed.
func SafeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}
