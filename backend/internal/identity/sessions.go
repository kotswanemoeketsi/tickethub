package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Session is returned after login, registration and refresh.
type Session struct {
	AccessToken           string    `json:"access_token"`
	AccessTokenExpiresAt  time.Time `json:"access_token_expires_at"`
	RefreshToken          string    `json:"refresh_token,omitempty"`
	RefreshTokenExpiresAt time.Time `json:"-"`
	User                  Profile   `json:"user"`
}

var errInvalidCredentials = apperr.New(http.StatusUnauthorized, "invalid_credentials", "The email or password is incorrect.")

// RegisterInput is a new customer account.
type RegisterInput struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Password  string `json:"password"`
}

// Register creates a customer account and signs it in.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*Session, error) {
	in.Email = validate.NormalizeEmail(in.Email)
	in.FirstName, in.LastName, in.Phone = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName), strings.TrimSpace(in.Phone)
	v := validate.New()
	v.Required("first_name", in.FirstName)
	v.MaxLen("first_name", in.FirstName, 100)
	v.Required("last_name", in.LastName)
	v.MaxLen("last_name", in.LastName, 100)
	v.Required("email", in.Email)
	v.Email("email", in.Email)
	v.Phone("phone", in.Phone)
	v.Password("password", in.Password)
	if err := v.Err(); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, err
	}

	var userID int64
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO users (email, password_hash, first_name, last_name, phone, status, password_changed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?)`, in.Email, hash, in.FirstName, in.LastName, in.Phone, now, now, now)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("email", "is already registered")
		}
		if err != nil {
			return err
		}
		userID, _ = res.LastInsertId()
		customerRole, err := s.roleIDBySlug(ctx, tx, rbac.RoleCustomer)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)`, userID, customerRole, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user.registered", EntityType: "user", EntityID: userID, Summary: "Customer account registered: " + in.Email})
	})
	if err != nil {
		return nil, err
	}
	return s.issueSession(ctx, userID, "")
}

const (
	maxFailedLogins   = 10
	failedLoginWindow = 15 * time.Minute
)

// Login verifies credentials and starts a session.
func (s *Service) Login(ctx context.Context, email, password string) (*Session, error) {
	email = validate.NormalizeEmail(email)
	if email == "" || password == "" {
		return nil, errInvalidCredentials
	}
	// Only failed attempts count towards the per-account limit, so people who sign in on
	// several devices are never locked out, while password guessing still is.
	limitKey := "login-failed:" + email
	if s.limiter.Exceeded(ctx, limitKey, maxFailedLogins, failedLoginWindow) {
		return nil, apperr.TooManyRequests("Too many failed sign-in attempts for this account. Please wait 15 minutes or reset your password.")
	}
	fail := func() (*Session, error) {
		s.limiter.Allow(ctx, limitKey, maxFailedLogins, failedLoginWindow)
		return nil, errInvalidCredentials
	}

	var userID int64
	var hash, status string
	err := s.db.QueryRowContext(ctx, `SELECT id, password_hash, status FROM users WHERE email = ?`, email).Scan(&userID, &hash, &status)
	if errors.Is(err, sql.ErrNoRows) {
		_, _, _ = s.hasher.Verify(password, s.dummyHash)
		return fail()
	}
	if err != nil {
		return nil, err
	}
	match, needsRehash, err := s.hasher.Verify(password, hash)
	if err != nil || !match {
		return fail()
	}
	if status != "active" {
		return nil, apperr.New(http.StatusForbidden, "account_inactive", "This account has been deactivated. Please contact support.")
	}

	now := s.now()
	if needsRehash {
		if newHash, err := s.hasher.Hash(password); err == nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, newHash, userID)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, now, userID); err != nil {
		return nil, err
	}
	return s.issueSession(ctx, userID, "")
}

// Refresh exchanges a refresh token for a new access token and a rotated refresh token.
// Presenting an already-rotated token signals theft, so the whole session family is revoked.
func (s *Service) Refresh(ctx context.Context, rawToken string) (*Session, error) {
	if rawToken == "" {
		return nil, apperr.Unauthorized("Your session has expired. Please sign in again.")
	}
	hash := random.HashToken(rawToken)
	var userID int64
	var familyID string
	reused := false
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var tokenID int64
		var expiresAt time.Time
		var revokedAt *time.Time
		err := tx.QueryRowContext(ctx, `SELECT id, user_id, family_id, expires_at, revoked_at FROM refresh_tokens WHERE token_hash = ? FOR UPDATE`, hash).
			Scan(&tokenID, &userID, &familyID, &expiresAt, &revokedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.Unauthorized("Your session has expired. Please sign in again.")
		}
		if err != nil {
			return err
		}
		now := s.now()
		if revokedAt != nil {
			reused = true
			_, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`, now, familyID)
			return err
		}
		if now.After(expiresAt) {
			return apperr.Unauthorized("Your session has expired. Please sign in again.")
		}
		_, err = tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ? WHERE id = ?`, now, tokenID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if reused {
		s.logger.Warn("refresh token reuse detected; session family revoked", "user_id", userID)
		return nil, apperr.Unauthorized("Your session has expired. Please sign in again.")
	}
	if _, err := s.loader.Load(ctx, userID); err != nil {
		if errors.Is(err, auth.ErrInactiveUser) {
			return nil, apperr.Unauthorized("This account is inactive.")
		}
		return nil, err
	}
	return s.issueSession(ctx, userID, familyID)
}

// Logout revokes the session the refresh token belongs to.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?
		WHERE family_id = (SELECT family_id FROM (SELECT family_id FROM refresh_tokens WHERE token_hash = ?) t) AND revoked_at IS NULL`,
		s.now(), random.HashToken(rawToken))
	return err
}

func (s *Service) issueSession(ctx context.Context, userID int64, familyID string) (*Session, error) {
	access, accessExp, err := s.tokens.Issue(userID)
	if err != nil {
		return nil, err
	}
	if familyID == "" {
		familyID = random.Hex(16)
	}
	refresh := random.Token(32)
	now := s.now()
	refreshExp := now.Add(s.cfg.RefreshTokenTTL)
	meta := httpx.ClientMetaFrom(ctx)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at, user_agent, ip_address, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, random.HashToken(refresh), familyID, refreshExp, meta.UserAgent, meta.IP, now); err != nil {
		return nil, err
	}
	profile, err := s.profile(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	return &Session{AccessToken: access, AccessTokenExpiresAt: accessExp, RefreshToken: refresh, RefreshTokenExpiresAt: refreshExp, User: profile}, nil
}

// ForgotPassword emails a reset link if the address belongs to an active account. It always
// succeeds so that the endpoint cannot be used to discover registered addresses.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	email = validate.NormalizeEmail(email)
	if email == "" || !s.limiter.Allow(ctx, "forgot:"+email, 3, time.Hour) {
		return nil
	}
	var userID int64
	var firstName string
	err := s.db.QueryRowContext(ctx, `SELECT id, first_name FROM users WHERE email = ? AND status = 'active'`, email).Scan(&userID, &firstName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	link, err := s.createResetLink(ctx, userID, time.Hour)
	if err != nil {
		return err
	}
	s.sendAsync(ctx, "password_reset", func(ctx context.Context) error {
		return s.notifier.PasswordReset(ctx, email, firstName, link)
	})
	return nil
}

func (s *Service) createResetLink(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	token := random.Token(32)
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO password_reset_tokens (user_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		userID, random.HashToken(token), now.Add(ttl), now); err != nil {
		return "", err
	}
	return s.cfg.PublicURL + "/reset-password?token=" + token, nil
}

// ResetPassword sets a new password using a reset token and signs the user out everywhere.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	v := validate.New()
	v.Required("token", token)
	v.Password("password", newPassword)
	if err := v.Err(); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	invalid := apperr.Conflict("reset_token_invalid", "This password reset link is invalid or has expired. Please request a new one.")
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var tokenID, userID int64
		var expiresAt time.Time
		var usedAt *time.Time
		err := tx.QueryRowContext(ctx, `SELECT id, user_id, expires_at, used_at FROM password_reset_tokens WHERE token_hash = ? FOR UPDATE`,
			random.HashToken(token)).Scan(&tokenID, &userID, &expiresAt, &usedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return invalid
		}
		if err != nil {
			return err
		}
		now := s.now()
		if usedAt != nil || now.After(expiresAt) {
			return invalid
		}
		// Using a link sent to the address proves the owner controls it.
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, email_verified_at = COALESCE(email_verified_at, ?), updated_at = ?
			WHERE id = ?`, hash, now, now, now, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE password_reset_tokens SET used_at = ? WHERE user_id = ? AND used_at IS NULL`, now, userID); err != nil {
			return err
		}
		if err := s.revokeAllSessions(ctx, tx, userID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user.password_reset", EntityType: "user", EntityID: userID, Summary: "Password reset via emailed link"})
	})
}

// ChangePasswordInput is a password change by the signed-in user.
type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword updates the password, revokes all other sessions and returns a fresh one.
func (s *Service) ChangePassword(ctx context.Context, p *auth.Principal, in ChangePasswordInput) (*Session, error) {
	v := validate.New()
	v.Required("current_password", in.CurrentPassword)
	v.Password("new_password", in.NewPassword)
	if err := v.Err(); err != nil {
		return nil, err
	}
	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, p.UserID).Scan(&current); err != nil {
		return nil, err
	}
	if match, _, err := s.hasher.Verify(in.CurrentPassword, current); err != nil || !match {
		return nil, apperr.FieldError("current_password", "is incorrect")
	}
	hash, err := s.hasher.Hash(in.NewPassword)
	if err != nil {
		return nil, err
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, hash, now, now, p.UserID); err != nil {
			return err
		}
		if err := s.revokeAllSessions(ctx, tx, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user.password_changed", EntityType: "user", EntityID: p.UserID, Summary: "Password changed"})
	})
	if err != nil {
		return nil, err
	}
	return s.issueSession(ctx, p.UserID, "")
}

// UpdateProfileInput edits the signed-in user's own details.
type UpdateProfileInput struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

// UpdateProfile edits the user's own name and phone number.
func (s *Service) UpdateProfile(ctx context.Context, p *auth.Principal, in UpdateProfileInput) (Profile, error) {
	in.FirstName, in.LastName, in.Phone = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName), strings.TrimSpace(in.Phone)
	v := validate.New()
	v.Required("first_name", in.FirstName)
	v.MaxLen("first_name", in.FirstName, 100)
	v.Required("last_name", in.LastName)
	v.MaxLen("last_name", in.LastName, 100)
	v.Phone("phone", in.Phone)
	if err := v.Err(); err != nil {
		return Profile{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET first_name = ?, last_name = ?, phone = ?, updated_at = ? WHERE id = ?`,
		in.FirstName, in.LastName, in.Phone, s.now(), p.UserID); err != nil {
		return Profile{}, err
	}
	s.loader.Invalidate(ctx, p.UserID)
	return s.profile(ctx, s.db, p.UserID)
}

// OrganiserAccess describes one organiser the user belongs to.
type OrganiserAccess struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Status      string   `json:"status"`
	RoleName    string   `json:"role_name"`
	Permissions []string `json:"permissions"`
}

// Me describes the signed-in user and everything they may do.
type Me struct {
	User          Profile           `json:"user"`
	IsSuperAdmin  bool              `json:"is_super_admin"`
	AllOrganisers bool              `json:"all_organisers"`
	StaffAccess   bool              `json:"staff_access"`
	Permissions   []string          `json:"permissions"`
	Organisers    []OrganiserAccess `json:"organisers"`
}

// Me returns the user's profile with platform and per-organiser permissions.
func (s *Service) Me(ctx context.Context, p *auth.Principal) (Me, error) {
	profile, err := s.profile(ctx, s.db, p.UserID)
	if err != nil {
		return Me{}, err
	}
	me := Me{
		User: profile, IsSuperAdmin: p.IsSuperAdmin, AllOrganisers: p.AllOrganisers(),
		StaffAccess: p.HasStaffAccess(), Permissions: p.PlatformPermissions(), Organisers: []OrganiserAccess{},
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.name, o.slug, o.status, r.name
		FROM organiser_members om JOIN organisers o ON o.id = om.organiser_id JOIN roles r ON r.id = om.role_id
		WHERE om.user_id = ? ORDER BY o.name`, p.UserID)
	if err != nil {
		return Me{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var o OrganiserAccess
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Status, &o.RoleName); err != nil {
			return Me{}, err
		}
		o.Permissions = p.OrganiserPermissions(o.ID)
		me.Organisers = append(me.Organisers, o)
	}
	return me, rows.Err()
}
