// Package identity manages accounts, sessions, users and roles.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/ratelimit"
)

// Notifier sends account emails. It is implemented by the notifications package.
type Notifier interface {
	PasswordReset(ctx context.Context, to, name, link string) error
	AccountInvite(ctx context.Context, to, name, link, invitedTo string) error
}

// Config holds identity settings.
type Config struct {
	AppName         string
	PublicURL       string
	RefreshTokenTTL time.Duration
}

// Service implements account and session use cases.
type Service struct {
	db        *database.DB
	hasher    auth.PasswordHasher
	tokens    *auth.TokenIssuer
	loader    *auth.PrincipalLoader
	limiter   *ratelimit.Limiter
	notifier  Notifier
	cfg       Config
	now       clock.Func
	logger    *slog.Logger
	dummyHash string

	google     *GoogleConfig // nil when sign-in with Google is not configured
	oauthStore cache.Cache
}

// NewService wires the identity service.
func NewService(db *database.DB, hasher auth.PasswordHasher, tokens *auth.TokenIssuer, loader *auth.PrincipalLoader,
	limiter *ratelimit.Limiter, notifier Notifier, cfg Config, now clock.Func, logger *slog.Logger) (*Service, error) {
	// Verifying against a dummy hash when an email is unknown keeps login timing uniform,
	// so response times do not reveal which addresses are registered.
	dummy, err := hasher.Hash(random.Token(16))
	if err != nil {
		return nil, err
	}
	return &Service{db: db, hasher: hasher, tokens: tokens, loader: loader, limiter: limiter,
		notifier: notifier, cfg: cfg, now: now, logger: logger, dummyHash: dummy}, nil
}

// User is an account as shown to administrators.
type User struct {
	ID          int64        `json:"id"`
	Email       string       `json:"email"`
	FirstName   string       `json:"first_name"`
	LastName    string       `json:"last_name"`
	Phone       string       `json:"phone"`
	Status      string       `json:"status"`
	LastLoginAt *time.Time   `json:"last_login_at"`
	CreatedAt   time.Time    `json:"created_at"`
	Roles       []RoleRef    `json:"roles"`
	Memberships []Membership `json:"memberships"`
}

// RoleRef is a role summary.
type RoleRef struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Membership is a user's role inside an organiser.
type Membership struct {
	OrganiserID     int64  `json:"organiser_id"`
	OrganiserName   string `json:"organiser_name"`
	OrganiserStatus string `json:"organiser_status"`
	RoleID          int64  `json:"role_id"`
	RoleName        string `json:"role_name"`
}

// Profile is the signed-in user's own account.
type Profile struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

func (s *Service) profile(ctx context.Context, q database.Queryer, userID int64) (Profile, error) {
	var p Profile
	err := q.QueryRowContext(ctx, `SELECT id, email, first_name, last_name, phone FROM users WHERE id = ?`, userID).
		Scan(&p.ID, &p.Email, &p.FirstName, &p.LastName, &p.Phone)
	if errors.Is(err, sql.ErrNoRows) {
		return p, apperr.NotFound("User")
	}
	return p, err
}

func (s *Service) roleIDBySlug(ctx context.Context, q database.Queryer, slug string) (int64, error) {
	var id int64
	if err := q.QueryRowContext(ctx, `SELECT id FROM roles WHERE slug = ?`, slug).Scan(&id); err != nil {
		return 0, fmt.Errorf("find role %s: %w", slug, err)
	}
	return id, nil
}

// revokeAllSessions logs a user out everywhere.
func (s *Service) revokeAllSessions(ctx context.Context, q database.Queryer, userID int64) error {
	_, err := q.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, s.now(), userID)
	return err
}

// sendAsync runs an email send outside the request so that SMTP latency (or failure) does
// not affect the response time, which would otherwise reveal whether an account exists.
func (s *Service) sendAsync(ctx context.Context, what string, send func(context.Context) error) {
	go func() {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := send(sendCtx); err != nil {
			s.logger.Error("send account email", "email", what, "error", err)
		}
	}()
}
