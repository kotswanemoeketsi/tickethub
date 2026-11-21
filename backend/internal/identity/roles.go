package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Role is a named set of permissions.
type Role struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Scope       string    `json:"scope"`
	IsSystem    bool      `json:"is_system"`
	Permissions []string  `json:"permissions"`
	UserCount   int64     `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListRoles returns every role with its permissions and how many users hold it.
func (s *Service) ListRoles(ctx context.Context, scope string) ([]Role, error) {
	where, args := "", []any{}
	if scope != "" {
		where, args = "WHERE r.scope = ?", []any{scope}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.slug, r.name, r.description, r.scope, r.is_system, r.created_at,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id) + (SELECT COUNT(*) FROM organiser_members om WHERE om.role_id = r.id)
		FROM roles r `+where+` ORDER BY r.is_system DESC, r.name`, args...)
	if err != nil {
		return nil, err
	}
	var roles []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Slug, &r.Name, &r.Description, &r.Scope, &r.IsSystem, &r.CreatedAt, &r.UserCount); err != nil {
			rows.Close()
			return nil, err
		}
		r.Permissions = []string{}
		roles = append(roles, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roles, s.attachPermissions(ctx, s.db, roles)
}

func (s *Service) rolesByID(ctx context.Context, q database.Queryer, ids []int64) ([]Role, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, slug, name, description, scope, is_system, created_at FROM roles
		WHERE id IN (`+database.Placeholders(len(ids))+`)`, database.Args(ids)...)
	if err != nil {
		return nil, err
	}
	var roles []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Slug, &r.Name, &r.Description, &r.Scope, &r.IsSystem, &r.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		r.Permissions = []string{}
		roles = append(roles, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roles, s.attachPermissions(ctx, q, roles)
}

// RoleByID loads one role with its permissions.
func (s *Service) RoleByID(ctx context.Context, q database.Queryer, id int64) (Role, error) {
	roles, err := s.rolesByID(ctx, q, []int64{id})
	if err != nil {
		return Role{}, err
	}
	if len(roles) == 0 {
		return Role{}, apperr.NotFound("Role")
	}
	return roles[0], nil
}

func (s *Service) attachPermissions(ctx context.Context, q database.Queryer, roles []Role) error {
	if len(roles) == 0 {
		return nil
	}
	index := map[int64]int{}
	ids := make([]any, len(roles))
	for i, r := range roles {
		index[r.ID] = i
		ids[i] = r.ID
	}
	rows, err := q.QueryContext(ctx, `SELECT rp.role_id, p.code FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id IN (`+database.Placeholders(len(ids))+`) ORDER BY p.code`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var roleID int64
		var code string
		if err := rows.Scan(&roleID, &code); err != nil {
			return err
		}
		roles[index[roleID]].Permissions = append(roles[index[roleID]].Permissions, code)
	}
	return rows.Err()
}

// RoleInput creates or edits a role.
type RoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	Permissions []string `json:"permissions"`
}

func (in *RoleInput) normalize() {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Scope == "" {
		in.Scope = rbac.ScopePlatform
	}
	seen := map[string]bool{}
	perms := make([]string, 0, len(in.Permissions))
	for _, p := range in.Permissions {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			perms = append(perms, p)
		}
	}
	sort.Strings(perms)
	in.Permissions = perms
}

func (in RoleInput) validate() error {
	v := validate.New()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 100)
	v.MaxLen("description", in.Description, 255)
	v.OneOf("scope", in.Scope, rbac.ScopePlatform, rbac.ScopeOrganiser)
	for _, code := range in.Permissions {
		perm, ok := rbac.Lookup(code)
		if !ok {
			v.Add("permissions", fmt.Sprintf("unknown permission %q", code))
			break
		}
		if in.Scope == rbac.ScopeOrganiser && !perm.Organiser {
			v.Add("permissions", fmt.Sprintf("%q can only be granted by a platform role", code))
			break
		}
	}
	return v.Err()
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// CreateRole creates a custom role, e.g. "Marketing Manager".
func (s *Service) CreateRole(ctx context.Context, actor *auth.Principal, in RoleInput) (Role, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return Role{}, err
	}
	if !canGrant(actor, in.Permissions) {
		return Role{}, apperr.Forbidden("A role cannot include permissions that you do not have yourself.")
	}
	slug := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(in.Name), "_"), "_")
	if slug == "" {
		return Role{}, apperr.FieldError("name", "must contain letters or numbers")
	}
	var id int64
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO roles (slug, name, description, scope, is_system, created_at, updated_at) VALUES (?, ?, ?, ?, FALSE, ?, ?)`,
			slug, in.Name, in.Description, in.Scope, now, now)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("name", "a role with this name already exists")
		}
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		if err := setRolePermissions(ctx, tx, id, in.Permissions); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "role.created", EntityType: "role", EntityID: id,
			Summary: fmt.Sprintf("Role %q created with %d permissions", in.Name, len(in.Permissions)),
			Changes: map[string]any{"permissions": in.Permissions, "scope": in.Scope}})
	})
	if err != nil {
		return Role{}, err
	}
	s.loader.InvalidateAll(ctx)
	return s.RoleByID(ctx, s.db, id)
}

// UpdateRole edits a role. System roles keep their name and scope, and the super
// administrator role cannot be edited at all.
func (s *Service) UpdateRole(ctx context.Context, actor *auth.Principal, id int64, in RoleInput) (Role, error) {
	existing, err := s.RoleByID(ctx, s.db, id)
	if err != nil {
		return Role{}, err
	}
	if existing.Slug == rbac.RoleSuperAdmin {
		return Role{}, apperr.Conflict("role_locked", "The Super Administrator role always has every permission and cannot be edited.")
	}
	if existing.IsSystem {
		in.Name, in.Scope = existing.Name, existing.Scope
	}
	in.normalize()
	if in.Scope != existing.Scope {
		return Role{}, apperr.FieldError("scope", "cannot be changed after the role is created")
	}
	if err := in.validate(); err != nil {
		return Role{}, err
	}
	if !canGrant(actor, append(append([]string{}, existing.Permissions...), in.Permissions...)) {
		return Role{}, apperr.Forbidden("You can only change roles whose permissions you hold yourself.")
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		_, err := tx.ExecContext(ctx, `UPDATE roles SET name = ?, description = ?, updated_at = ? WHERE id = ?`, in.Name, in.Description, now, id)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("name", "a role with this name already exists")
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, id); err != nil {
			return err
		}
		if err := setRolePermissions(ctx, tx, id, in.Permissions); err != nil {
			return err
		}
		added, removed := diffSets(existing.Permissions, in.Permissions)
		return audit.Record(ctx, tx, now, audit.Entry{Action: "role.updated", EntityType: "role", EntityID: id,
			Summary: fmt.Sprintf("Role %q updated: %d permissions added, %d removed", in.Name, len(added), len(removed)),
			Changes: map[string]any{"added": added, "removed": removed}})
	})
	if err != nil {
		return Role{}, err
	}
	s.loader.InvalidateAll(ctx)
	return s.RoleByID(ctx, s.db, id)
}

// DeleteRole removes a custom role that nobody holds.
func (s *Service) DeleteRole(ctx context.Context, actor *auth.Principal, id int64) error {
	existing, err := s.RoleByID(ctx, s.db, id)
	if err != nil {
		return err
	}
	if existing.IsSystem {
		return apperr.Conflict("role_locked", "System roles cannot be deleted.")
	}
	if !canGrant(actor, existing.Permissions) {
		return apperr.Forbidden("You can only delete roles whose permissions you hold yourself.")
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var inUse int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM user_roles WHERE role_id = ?) + (SELECT COUNT(*) FROM organiser_members WHERE role_id = ?)`,
			id, id).Scan(&inUse); err != nil {
			return err
		}
		if inUse > 0 {
			return apperr.Conflict("role_in_use", fmt.Sprintf("This role is assigned to %d user(s); reassign them before deleting it.", inUse))
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "role.deleted", EntityType: "role", EntityID: id,
			Summary: fmt.Sprintf("Role %q deleted", existing.Name)})
	})
}

func setRolePermissions(ctx context.Context, tx *sql.Tx, roleID int64, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission_id)
		SELECT ?, id FROM permissions WHERE code IN (`+database.Placeholders(len(codes))+`)`,
		append([]any{roleID}, database.Args(codes)...)...)
	return err
}

func diffSets(before, after []string) (added, removed []string) {
	b, a := map[string]bool{}, map[string]bool{}
	for _, x := range before {
		b[x] = true
	}
	for _, x := range after {
		a[x] = true
		if !b[x] {
			added = append(added, x)
		}
	}
	for _, x := range before {
		if !a[x] {
			removed = append(removed, x)
		}
	}
	return added, removed
}

// OrganiserRole loads an organiser-scoped role for team assignment.
func (s *Service) OrganiserRole(ctx context.Context, q database.Queryer, id int64) (Role, error) {
	r, err := s.RoleByID(ctx, q, id)
	if err != nil {
		return Role{}, err
	}
	if r.Scope != rbac.ScopeOrganiser {
		return Role{}, apperr.FieldError("role_id", "must be an organiser role")
	}
	return r, nil
}

// RoleIDBySlug exposes system role lookup to other modules.
func (s *Service) RoleIDBySlug(ctx context.Context, q database.Queryer, slug string) (int64, error) {
	id, err := s.roleIDBySlug(ctx, q, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, apperr.NotFound("Role")
	}
	return id, err
}
