package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// UserFilter narrows the user list.
type UserFilter struct {
	Query  string
	RoleID int64
	Status string
}

// ListUsers returns a page of users with their roles and memberships.
func (s *Service) ListUsers(ctx context.Context, f UserFilter, page httpx.Page) ([]User, int64, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if f.Query != "" {
		where = append(where, "(u.email LIKE ? OR CONCAT(u.first_name, ' ', u.last_name) LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like)
	}
	if f.Status != "" {
		where = append(where, "u.status = ?")
		args = append(args, f.Status)
	}
	if f.RoleID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_id = ?)")
		args = append(args, f.RoleID)
	}
	clause := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users u WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.email, u.first_name, u.last_name, u.phone, u.status, u.last_login_at, u.created_at
		FROM users u WHERE `+clause+` ORDER BY u.created_at DESC, u.id DESC LIMIT ? OFFSET ?`, append(args, page.Size, page.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var users []User
	byID := map[int64]int{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Phone, &u.Status, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		u.Roles, u.Memberships = []RoleRef{}, []Membership{}
		byID[u.ID] = len(users)
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.attachRoles(ctx, users, byID); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// attachRoles loads roles and memberships for a set of users in two queries.
func (s *Service) attachRoles(ctx context.Context, users []User, byID map[int64]int) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]any, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	in := database.Placeholders(len(ids))

	rows, err := s.db.QueryContext(ctx, `SELECT ur.user_id, r.id, r.slug, r.name FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id IN (`+in+`) ORDER BY r.name`, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var userID int64
		var ref RoleRef
		if err := rows.Scan(&userID, &ref.ID, &ref.Slug, &ref.Name); err != nil {
			rows.Close()
			return err
		}
		users[byID[userID]].Roles = append(users[byID[userID]].Roles, ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT om.user_id, o.id, o.name, o.status, r.id, r.name
		FROM organiser_members om JOIN organisers o ON o.id = om.organiser_id JOIN roles r ON r.id = om.role_id
		WHERE om.user_id IN (`+in+`) ORDER BY o.name`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID int64
		var m Membership
		if err := rows.Scan(&userID, &m.OrganiserID, &m.OrganiserName, &m.OrganiserStatus, &m.RoleID, &m.RoleName); err != nil {
			return err
		}
		users[byID[userID]].Memberships = append(users[byID[userID]].Memberships, m)
	}
	return rows.Err()
}

// GetUser returns one user with roles and memberships.
func (s *Service) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT id, email, first_name, last_name, phone, status, last_login_at, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Phone, &u.Status, &u.LastLoginAt, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, apperr.NotFound("User")
	}
	if err != nil {
		return u, err
	}
	u.Roles, u.Memberships = []RoleRef{}, []Membership{}
	users := []User{u}
	if err := s.attachRoles(ctx, users, map[int64]int{u.ID: 0}); err != nil {
		return u, err
	}
	return users[0], nil
}

// UserInput creates or edits a user. Password is optional on create: when empty the user
// receives an email inviting them to choose one.
type UserInput struct {
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
	Email     string  `json:"email"`
	Phone     string  `json:"phone"`
	Password  string  `json:"password"`
	RoleIDs   []int64 `json:"role_ids"`
}

func (in *UserInput) normalize() {
	in.Email = validate.NormalizeEmail(in.Email)
	in.FirstName, in.LastName, in.Phone = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName), strings.TrimSpace(in.Phone)
}

func (in UserInput) validate(requirePassword bool) error {
	v := validate.New()
	v.Required("first_name", in.FirstName)
	v.MaxLen("first_name", in.FirstName, 100)
	v.Required("last_name", in.LastName)
	v.MaxLen("last_name", in.LastName, 100)
	v.Required("email", in.Email)
	v.Email("email", in.Email)
	v.Phone("phone", in.Phone)
	if in.Password != "" || requirePassword {
		v.Password("password", in.Password)
	}
	return v.Err()
}

// CreateUser creates a staff or customer account with the given platform roles.
func (s *Service) CreateUser(ctx context.Context, actor *auth.Principal, in UserInput) (User, error) {
	in.normalize()
	if err := in.validate(false); err != nil {
		return User{}, err
	}
	sendInvite := in.Password == ""
	password := in.Password
	if sendInvite {
		password = random.Token(24)
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return User{}, err
	}

	var userID int64
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		roles, err := s.assignableRoles(ctx, tx, actor, in.RoleIDs)
		if err != nil {
			return err
		}
		now := s.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO users (email, password_hash, first_name, last_name, phone, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'active', ?, ?)`, in.Email, hash, in.FirstName, in.LastName, in.Phone, now, now)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("email", "is already registered")
		}
		if err != nil {
			return err
		}
		userID, _ = res.LastInsertId()
		for _, r := range roles {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)`, userID, r.ID, now); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, now, audit.Entry{
			Action: "user.created", EntityType: "user", EntityID: userID,
			Summary: fmt.Sprintf("User %s created with roles: %s", in.Email, roleNames(roles)),
		})
	})
	if err != nil {
		return User{}, err
	}
	if sendInvite {
		if err := s.SendInvite(ctx, userID, s.cfg.AppName); err != nil {
			s.logger.Error("send invite", "user_id", userID, "error", err)
		}
	}
	return s.GetUser(ctx, userID)
}

// UpdateUser edits a user's details.
func (s *Service) UpdateUser(ctx context.Context, actor *auth.Principal, id int64, in UserInput) (User, error) {
	in.normalize()
	in.Password = ""
	if err := in.validate(false); err != nil {
		return User{}, err
	}
	existing, err := s.GetUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	if err := s.guardSuperAdminTarget(actor, existing); err != nil {
		return User{}, err
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		_, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, first_name = ?, last_name = ?, phone = ?, updated_at = ? WHERE id = ?`,
			in.Email, in.FirstName, in.LastName, in.Phone, now, id)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("email", "is already registered")
		}
		if err != nil {
			return err
		}
		diff := audit.Diff{}
		diff.Add("email", existing.Email, in.Email)
		diff.Add("first_name", existing.FirstName, in.FirstName)
		diff.Add("last_name", existing.LastName, in.LastName)
		diff.Add("phone", existing.Phone, in.Phone)
		if len(diff) == 0 {
			return nil
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user.updated", EntityType: "user", EntityID: id,
			Summary: "Updated " + diff.Fields(), Changes: diff})
	})
	if err != nil {
		return User{}, err
	}
	s.loader.Invalidate(ctx, id)
	return s.GetUser(ctx, id)
}

// SetUserStatus activates or deactivates an account. Deactivation signs the user out.
func (s *Service) SetUserStatus(ctx context.Context, actor *auth.Principal, id int64, active bool) (User, error) {
	if id == actor.UserID && !active {
		return User{}, apperr.Conflict("cannot_deactivate_self", "You cannot deactivate your own account.")
	}
	existing, err := s.GetUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	if err := s.guardSuperAdminTarget(actor, existing); err != nil {
		return User{}, err
	}
	status := "inactive"
	if active {
		status = "active"
	}
	if existing.Status == status {
		return existing, nil
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, now, id); err != nil {
			return err
		}
		if !active {
			if err := s.revokeAllSessions(ctx, tx, id); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user." + map[bool]string{true: "activated", false: "deactivated"}[active],
			EntityType: "user", EntityID: id, Summary: fmt.Sprintf("Account %s set to %s", existing.Email, status)})
	})
	if err != nil {
		return User{}, err
	}
	s.loader.Invalidate(ctx, id)
	return s.GetUser(ctx, id)
}

// SetUserRoles replaces a user's platform roles.
func (s *Service) SetUserRoles(ctx context.Context, actor *auth.Principal, id int64, roleIDs []int64) (User, error) {
	existing, err := s.GetUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	if err := s.guardSuperAdminTarget(actor, existing); err != nil {
		return User{}, err
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		roles, err := s.assignableRoles(ctx, tx, actor, roleIDs)
		if err != nil {
			return err
		}
		// Removing roles is guarded too: you may only take away what you could grant.
		currentIDs := make([]int64, 0, len(existing.Roles))
		for _, r := range existing.Roles {
			currentIDs = append(currentIDs, r.ID)
		}
		if _, err := s.assignableRoles(ctx, tx, actor, currentIDs); err != nil {
			return err
		}
		if id == actor.UserID && actor.IsSuperAdmin && !containsSlug(roles, rbac.RoleSuperAdmin) {
			if err := s.ensureAnotherSuperAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, id); err != nil {
			return err
		}
		for _, r := range roles {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)`, id, r.ID, now); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "user.roles_changed", EntityType: "user", EntityID: id,
			Summary: fmt.Sprintf("Roles for %s set to: %s", existing.Email, roleNames(roles)),
			Changes: audit.Diff{"roles": {From: refNames(existing.Roles), To: roleNames(roles)}}})
	})
	if err != nil {
		return User{}, err
	}
	s.loader.Invalidate(ctx, id)
	return s.GetUser(ctx, id)
}

// SendPasswordReset emails a reset link on behalf of an administrator.
func (s *Service) SendPasswordReset(ctx context.Context, actor *auth.Principal, id int64) error {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if err := s.guardSuperAdminTarget(actor, u); err != nil {
		return err
	}
	link, err := s.createResetLink(ctx, id, 24*time.Hour)
	if err != nil {
		return err
	}
	if err := audit.Record(ctx, s.db, s.now(), audit.Entry{Action: "user.password_reset_sent", EntityType: "user", EntityID: id,
		Summary: "Password reset link sent to " + u.Email}); err != nil {
		return err
	}
	return s.notifier.PasswordReset(ctx, u.Email, u.FirstName, link)
}

// SendInvite emails a new user a link to choose their password.
func (s *Service) SendInvite(ctx context.Context, userID int64, invitedTo string) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	link, err := s.createResetLink(ctx, userID, 72*time.Hour)
	if err != nil {
		return err
	}
	return s.notifier.AccountInvite(ctx, u.Email, u.FirstName, link, invitedTo)
}

// EnsureUser returns the ID of the account with email, creating it (without a usable
// password) when it does not exist. It is used when inviting organiser team members.
func (s *Service) EnsureUser(ctx context.Context, tx *sql.Tx, email, firstName, lastName string) (int64, bool, error) {
	email = validate.NormalizeEmail(email)
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	v := validate.New()
	v.Required("first_name", strings.TrimSpace(firstName))
	v.Required("last_name", strings.TrimSpace(lastName))
	v.Email("email", email)
	if err := v.Err(); err != nil {
		return 0, false, err
	}
	hash, err := s.hasher.Hash(random.Token(24))
	if err != nil {
		return 0, false, err
	}
	now := s.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO users (email, password_hash, first_name, last_name, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?)`, email, hash, strings.TrimSpace(firstName), strings.TrimSpace(lastName), now, now)
	if err != nil {
		return 0, false, err
	}
	id, _ = res.LastInsertId()
	customerRole, err := s.roleIDBySlug(ctx, tx, rbac.RoleCustomer)
	if err != nil {
		return 0, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)`, id, customerRole, now)
	return id, true, err
}

// assignableRoles loads the requested platform roles and checks the actor may grant them.
func (s *Service) assignableRoles(ctx context.Context, q database.Queryer, actor *auth.Principal, roleIDs []int64) ([]Role, error) {
	roleIDs = uniqueIDs(roleIDs)
	if len(roleIDs) == 0 {
		return nil, nil
	}
	roles, err := s.rolesByID(ctx, q, roleIDs)
	if err != nil {
		return nil, err
	}
	if len(roles) != len(roleIDs) {
		return nil, apperr.FieldError("role_ids", "contains an unknown role")
	}
	for _, r := range roles {
		if r.Scope != rbac.ScopePlatform {
			return nil, apperr.FieldError("role_ids", fmt.Sprintf("%q is an organiser role; assign it through the organiser's team", r.Name))
		}
		if r.Slug == rbac.RoleSuperAdmin && !actor.IsSuperAdmin {
			return nil, apperr.Forbidden("Only a super administrator can grant or remove the Super Administrator role.")
		}
		if !canGrant(actor, r.Permissions) {
			return nil, apperr.Forbidden(fmt.Sprintf("You cannot assign %q because it includes permissions you do not have.", r.Name))
		}
	}
	return roles, nil
}

// guardSuperAdminTarget stops non-super-admins from editing super administrators.
func (s *Service) guardSuperAdminTarget(actor *auth.Principal, target User) error {
	if actor.IsSuperAdmin {
		return nil
	}
	for _, r := range target.Roles {
		if r.Slug == rbac.RoleSuperAdmin {
			return apperr.Forbidden("Only a super administrator can modify a super administrator's account.")
		}
	}
	return nil
}

func (s *Service) ensureAnotherSuperAdmin(ctx context.Context, q database.Queryer, excludingUserID int64) error {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id JOIN users u ON u.id = ur.user_id
		WHERE r.slug = ? AND u.status = 'active' AND ur.user_id <> ?`, rbac.RoleSuperAdmin, excludingUserID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.Conflict("last_super_admin", "You are the only active super administrator; assign the role to someone else first.")
	}
	return nil
}

// canGrant reports whether actor holds every permission in perms as a platform permission.
func canGrant(actor *auth.Principal, perms []string) bool {
	if actor.IsSuperAdmin {
		return true
	}
	for _, perm := range perms {
		if !actor.HasPlatform(perm) {
			return false
		}
	}
	return true
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func containsSlug(roles []Role, slug string) bool {
	for _, r := range roles {
		if r.Slug == slug {
			return true
		}
	}
	return false
}

func roleNames(roles []Role) string {
	if len(roles) == 0 {
		return "none"
	}
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

func refNames(roles []RoleRef) string {
	if len(roles) == 0 {
		return "none"
	}
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}
