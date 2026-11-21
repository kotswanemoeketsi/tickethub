package app_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/kotswanemoeketsi/gotaticket/internal/app"
	"github.com/kotswanemoeketsi/gotaticket/internal/config"
)

const googleClientID = "client-123.apps.googleusercontent.com"

// fakeGoogle plays Google's token endpoint. Each authorization code maps to the claims the
// ID token will carry and the PKCE challenge the code was issued for.
type fakeGoogle struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	codes map[string]fakeGrant
}

type fakeGrant struct {
	challenge string
	claims    map[string]any
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{t: t, codes: map[string]fakeGrant{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" || r.ParseForm() != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		grant, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || r.Form.Get("client_id") != googleClientID || r.Form.Get("client_secret") != "secret" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		payload, _ := json.Marshal(grant.claims)
		token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": token, "access_token": "x"})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newGoogleEnv(t *testing.T) (*env, *fakeGoogle) {
	g := newFakeGoogle(t)
	e := newEnvWith(t, func(c *config.Config) []app.Option {
		c.GoogleClientID, c.GoogleClientSecret = googleClientID, "secret"
		return []app.Option{app.WithGoogleEndpoints("https://accounts.example/auth", g.srv.URL+"/token")}
	})
	return e, g
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type googleFlow struct {
	state, nonce, challenge string
	cookie                  *http.Cookie
}

// startGoogle begins a sign-in the way a browser would and returns what Google receives.
func (e *env) startGoogle(next string) googleFlow {
	e.t.Helper()
	res, err := noRedirects.Get(e.srv.URL + "/api/v1/auth/google/start?next=" + url.QueryEscape(next))
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		e.t.Fatalf("start: status %d", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	q := loc.Query()
	if q.Get("client_id") != googleClientID || q.Get("redirect_uri") != "http://web.test/api/v1/auth/google/callback" ||
		q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), "email") {
		e.t.Fatalf("unexpected authorization request: %s", loc)
	}
	f := googleFlow{state: q.Get("state"), nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	for _, c := range res.Cookies() {
		if c.Name == "gt_oauth_state" {
			f.cookie = c
		}
	}
	if f.cookie == nil || f.cookie.Value != f.state || !f.cookie.HttpOnly {
		e.t.Fatal("state cookie missing or wrong")
	}
	return f
}

// claims returns ID token claims for flow f; overrides replace individual claims.
func (g *fakeGoogle) grant(f googleFlow, sub, email string, overrides map[string]any) string {
	claims := map[string]any{
		"iss": "https://accounts.google.com", "aud": googleClientID, "exp": 4102444800, "nonce": f.nonce,
		"sub": sub, "email": email, "email_verified": true, "given_name": "Ama", "family_name": "Mensah",
	}
	for k, v := range overrides {
		claims[k] = v
	}
	code := fmt.Sprintf("code-%s", f.state[:8])
	g.mu.Lock()
	g.codes[code] = fakeGrant{challenge: f.challenge, claims: claims}
	g.mu.Unlock()
	return code
}

// callback returns the redirect location and the refresh cookie, if one was set.
func (e *env) callback(f googleFlow, code string, cookie *http.Cookie) (*url.URL, *http.Cookie) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/auth/google/callback?"+url.Values{"code": {code}, "state": {f.state}}.Encode(), nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := noRedirects.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		e.t.Fatalf("callback: status %d", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	for _, c := range res.Cookies() {
		if c.Name == "gt_refresh" && c.Value != "" {
			return loc, c
		}
	}
	return loc, nil
}

// signInWithGoogle runs the whole flow and returns the signed-in user's email.
func (e *env) signInWithGoogle(g *fakeGoogle, sub, email string) string {
	e.t.Helper()
	f := e.startGoogle("/events/jazz")
	loc, refresh := e.callback(f, g.grant(f, sub, email, nil), f.cookie)
	if loc.Path != "/auth/complete" || loc.Query().Get("next") != "/events/jazz" || refresh == nil {
		e.t.Fatalf("sign-in did not complete: %s", loc)
	}
	r := e.must(200, e.request("POST", "/api/v1/auth/refresh", "", nil, "Cookie", "gt_refresh="+refresh.Value))
	var s struct {
		User struct{ Email string } `json:"user"`
	}
	r.decode(e.t, &s)
	return s.User.Email
}

func TestGoogleSignIn(t *testing.T) {
	e, g := newGoogleEnv(t)
	e.must(200, e.request("GET", "/api/v1/auth/providers", "", nil))

	// A new Google user gets a customer account; signing in again reuses it.
	if got := e.signInWithGoogle(g, "g-new", "ama@gmail.com"); got != "ama@gmail.com" {
		t.Fatalf("signed in as %q", got)
	}
	e.signInWithGoogle(g, "g-new", "ama@gmail.com")
	var accounts, identities int
	_ = e.app.DB.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM users WHERE email = 'ama@gmail.com'`).Scan(&accounts)
	_ = e.app.DB.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM user_identities WHERE subject = 'g-new'`).Scan(&identities)
	if accounts != 1 || identities != 1 {
		t.Fatalf("accounts=%d identities=%d", accounts, identities)
	}

	// An account registered with a password, whose owner never proved the address, is
	// taken over by the verified Google owner: the old password stops working.
	squatted, _ := e.user("squat")
	e.signInWithGoogle(g, "g-owner", squatted)
	e.must(401, e.request("POST", "/api/v1/auth/login", "", map[string]string{"email": squatted, "password": testPassword}))

	// An account whose owner did prove the address keeps its password when linked.
	e.signInWithGoogle(g, "g-admin", superAdminMail)
	e.login(superAdminMail)

	// Deactivated accounts cannot sign in with Google either.
	admin := e.login(superAdminMail)
	var id int64
	_ = e.app.DB.QueryRowContext(e.ctx, `SELECT id FROM users WHERE email = 'ama@gmail.com'`).Scan(&id)
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/users/%d/deactivate", id), admin, nil))
	f := e.startGoogle("/")
	if loc, refresh := e.callback(f, g.grant(f, "g-new", "ama@gmail.com", nil), f.cookie); refresh != nil || loc.Query().Get("sso_error") != "account_inactive" {
		t.Fatalf("inactive account signed in: %s", loc)
	}
}

func TestGoogleSignInRejectsBadResponses(t *testing.T) {
	e, g := newGoogleEnv(t)
	cases := []struct {
		name      string
		overrides map[string]any
		cookie    func(f googleFlow) *http.Cookie
		want      string
	}{
		{"missing state cookie", nil, func(googleFlow) *http.Cookie { return nil }, "invalid_state"},
		{"state from another browser", nil, func(googleFlow) *http.Cookie { return &http.Cookie{Name: "gt_oauth_state", Value: "other"} }, "invalid_state"},
		{"wrong nonce", map[string]any{"nonce": "replayed"}, nil, "verification_failed"},
		{"other audience", map[string]any{"aud": "someone-else"}, nil, "verification_failed"},
		{"wrong issuer", map[string]any{"iss": "https://evil.example"}, nil, "verification_failed"},
		{"expired token", map[string]any{"exp": 1000}, nil, "verification_failed"},
		{"unverified email", map[string]any{"email_verified": false}, nil, "email_unverified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := e.startGoogle("/")
			cookie := f.cookie
			if c.cookie != nil {
				cookie = c.cookie(f)
			}
			loc, refresh := e.callback(f, g.grant(f, "g-"+c.name, "x-"+strings.ReplaceAll(c.name, " ", "")+"@gmail.com", c.overrides), cookie)
			if refresh != nil || loc.Path != "/login" || loc.Query().Get("sso_error") != c.want {
				t.Fatalf("got %s, want sso_error=%s", loc, c.want)
			}
		})
	}

	// A code can only be used once.
	f := e.startGoogle("/")
	code := g.grant(f, "g-once", "once@gmail.com", nil)
	if _, refresh := e.callback(f, code, f.cookie); refresh == nil {
		t.Fatal("first use failed")
	}
	if loc, refresh := e.callback(f, code, f.cookie); refresh != nil || loc.Query().Get("sso_error") == "" {
		t.Fatal("state or code was accepted twice")
	}
}
