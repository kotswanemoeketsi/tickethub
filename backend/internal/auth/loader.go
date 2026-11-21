package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// ErrInactiveUser is returned when the user no longer exists or has been deactivated.
var ErrInactiveUser = errors.New("user is inactive")

// cachedPrincipal is the serialisable form of a Principal.
type cachedPrincipal struct {
	UserID     int64              `json:"user_id"`
	Email      string             `json:"email"`
	FirstName  string             `json:"first_name"`
	LastName   string             `json:"last_name"`
	SuperAdmin bool               `json:"super_admin"`
	Platform   []string           `json:"platform"`
	Organisers map[int64][]string `json:"organisers"`
}

// PrincipalLoader loads a user's permissions from the database with a short-lived cache.
//
// Cache keys include a global version number. Role or permission edits bump the version,
// which invalidates every cached principal at once; user-specific changes (deactivation,
// role assignment, membership) delete that user's entry directly.
type PrincipalLoader struct {
	db    *database.DB
	cache cache.Cache
	ttl   time.Duration
}

// NewPrincipalLoader returns a loader caching principals for ttl.
func NewPrincipalLoader(db *database.DB, c cache.Cache, ttl time.Duration) *PrincipalLoader {
	return &PrincipalLoader{db: db, cache: c, ttl: ttl}
}

const authzVersionKey = "authz:version"

func (l *PrincipalLoader) cacheKey(ctx context.Context, userID int64) string {
	version := "0"
	if raw, ok, err := l.cache.Get(ctx, authzVersionKey); err == nil && ok {
		version = string(raw)
	}
	return "authz:v" + version + ":user:" + strconv.FormatInt(userID, 10)
}

// Load returns the principal for userID.
func (l *PrincipalLoader) Load(ctx context.Context, userID int64) (*Principal, error) {
	key := l.cacheKey(ctx, userID)
	var cp cachedPrincipal
	if hit, err := cache.GetJSON(ctx, l.cache, key, &cp); err == nil && hit {
		return cp.principal(), nil
	}
	cp, err := l.query(ctx, userID)
	if err != nil {
		return nil, err
	}
	_ = cache.SetJSON(ctx, l.cache, key, cp, l.ttl)
	return cp.principal(), nil
}

// Invalidate drops the cached principal for one user.
func (l *PrincipalLoader) Invalidate(ctx context.Context, userID int64) {
	_ = l.cache.Delete(ctx, l.cacheKey(ctx, userID))
}

// InvalidateAll drops every cached principal (after role or permission changes).
func (l *PrincipalLoader) InvalidateAll(ctx context.Context) {
	_, _ = l.cache.Incr(ctx, authzVersionKey, 0)
}

func (cp cachedPrincipal) principal() *Principal {
	return NewPrincipal(cp.UserID, cp.Email, cp.FirstName, cp.LastName, cp.SuperAdmin, cp.Platform, cp.Organisers)
}

func (l *PrincipalLoader) query(ctx context.Context, userID int64) (cachedPrincipal, error) {
	cp := cachedPrincipal{UserID: userID, Organisers: map[int64][]string{}}
	var status string
	err := l.db.QueryRowContext(ctx, `SELECT email, first_name, last_name, status FROM users WHERE id = ?`, userID).
		Scan(&cp.Email, &cp.FirstName, &cp.LastName, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "active") {
		return cp, ErrInactiveUser
	}
	if err != nil {
		return cp, fmt.Errorf("load user: %w", err)
	}

	rows, err := l.db.QueryContext(ctx, `
		SELECT r.slug, p.code
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		LEFT JOIN permissions p ON p.id = rp.permission_id
		WHERE ur.user_id = ?`, userID)
	if err != nil {
		return cp, fmt.Errorf("load platform permissions: %w", err)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var slug string
		var code sql.NullString
		if err := rows.Scan(&slug, &code); err != nil {
			rows.Close()
			return cp, err
		}
		if slug == rbac.RoleSuperAdmin {
			cp.SuperAdmin = true
		}
		if code.Valid && !seen[code.String] {
			seen[code.String] = true
			cp.Platform = append(cp.Platform, code.String)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return cp, err
	}

	// Memberships of pending or suspended organisers grant nothing.
	rows, err = l.db.QueryContext(ctx, `
		SELECT om.organiser_id, p.code
		FROM organiser_members om
		JOIN organisers o ON o.id = om.organiser_id AND o.status = 'active'
		LEFT JOIN role_permissions rp ON rp.role_id = om.role_id
		LEFT JOIN permissions p ON p.id = rp.permission_id
		WHERE om.user_id = ?`, userID)
	if err != nil {
		return cp, fmt.Errorf("load organiser permissions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var orgID int64
		var code sql.NullString
		if err := rows.Scan(&orgID, &code); err != nil {
			return cp, err
		}
		if _, ok := cp.Organisers[orgID]; !ok {
			cp.Organisers[orgID] = []string{}
		}
		if code.Valid {
			cp.Organisers[orgID] = append(cp.Organisers[orgID], code.String)
		}
	}
	return cp, rows.Err()
}
