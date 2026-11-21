package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

func TestPasswordHashing(t *testing.T) {
	h := PasswordHasher{Iterations: 10_000}
	encoded, err := h.Hash("correct horse battery staple 1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "pbkdf2-sha256$10000$") {
		t.Fatalf("unexpected encoding %q", encoded)
	}
	if ok, rehash, err := h.Verify("correct horse battery staple 1", encoded); !ok || rehash || err != nil {
		t.Fatalf("verify failed: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify("wrong password", encoded); ok {
		t.Fatal("wrong password verified")
	}
	again, _ := h.Hash("correct horse battery staple 1")
	if again == encoded {
		t.Fatal("hashes must be salted")
	}
	stronger := PasswordHasher{Iterations: 20_000}
	if ok, rehash, _ := stronger.Verify("correct horse battery staple 1", encoded); !ok || !rehash {
		t.Fatal("hashes made with fewer iterations should be flagged for rehashing")
	}
	if _, _, err := h.Verify("x", "md5$abc"); err == nil {
		t.Fatal("malformed hashes must be rejected")
	}
}

func TestAccessTokens(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	issuer := NewTokenIssuer(strings.Repeat("s", 32), 15*time.Minute, clk.Now)
	token, _, err := issuer.Issue(42)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := issuer.Parse(token); err != nil || id != 42 {
		t.Fatalf("parse: id=%d err=%v", id, err)
	}
	clk.Advance(16 * time.Minute)
	if _, err := issuer.Parse(token); err == nil {
		t.Fatal("expired token accepted")
	}

	other := NewTokenIssuer(strings.Repeat("x", 32), 15*time.Minute, clk.Now)
	forged, _, _ := other.Issue(42)
	if _, err := issuer.Parse(forged); err == nil {
		t.Fatal("token signed with another key accepted")
	}

	// The "none" algorithm must never be accepted.
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Subject: "42", Issuer: tokenIssuer,
		Audience: jwt.ClaimStrings{tokenAudience}, ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour))})
	raw, _ := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := issuer.Parse(raw); err == nil {
		t.Fatal("unsigned token accepted")
	}
}

func TestPrincipalScopes(t *testing.T) {
	organiserUser := NewPrincipal(1, "o@example.test", "O", "U", false, nil, map[int64][]string{
		10: {rbac.EventsView, rbac.EventsUpdate},
		20: {rbac.EventsView},
	})
	if !organiserUser.CanFor(10, rbac.EventsUpdate) || organiserUser.CanFor(20, rbac.EventsUpdate) || organiserUser.CanFor(30, rbac.EventsView) {
		t.Fatal("organiser permissions must apply per organiser")
	}
	if scope := organiserUser.Scope(rbac.EventsView); scope.All || len(scope.IDs) != 2 {
		t.Fatalf("unexpected scope %+v", scope)
	}
	if clause, args := organiserUser.Scope(rbac.EventsUpdate).SQL("e.organiser_id"); clause != "e.organiser_id IN (?)" || len(args) != 1 {
		t.Fatalf("unexpected SQL %q %v", clause, args)
	}

	// Platform permissions only reach organiser data together with platform.all_organisers.
	platformNoScope := NewPrincipal(2, "p@example.test", "P", "U", false, []string{rbac.EventsView}, nil)
	if platformNoScope.CanFor(10, rbac.EventsView) || !platformNoScope.Scope(rbac.EventsView).Empty() {
		t.Fatal("platform permission without all-organisers access must not reach organiser data")
	}
	staff := NewPrincipal(3, "s@example.test", "S", "U", false, []string{rbac.EventsView, rbac.AllOrganisers}, nil)
	if !staff.CanFor(99, rbac.EventsView) || !staff.Scope(rbac.EventsView).All {
		t.Fatal("platform staff should see every organiser")
	}
	if narrowed := staff.Scope(rbac.EventsView).Narrow(5); narrowed.All || len(narrowed.IDs) != 1 {
		t.Fatalf("narrowing failed: %+v", narrowed)
	}
	if outside := organiserUser.Scope(rbac.EventsView).Narrow(30); !outside.Empty() {
		t.Fatal("narrowing to an organiser outside the scope must yield nothing")
	}
}
