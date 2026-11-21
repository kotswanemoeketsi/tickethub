// Package organisers manages the marketplace's tenants: the businesses that sell tickets,
// their approval lifecycle and their team members.
package organisers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/identity"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/slug"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Organiser statuses.
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// Organiser is a tenant of the marketplace.
type Organiser struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Slug          string     `json:"slug"`
	Email         string     `json:"email"`
	Phone         string     `json:"phone"`
	Description   string     `json:"description"`
	Status        string     `json:"status"`
	CommissionBPS int        `json:"commission_bps"`
	ApprovedAt    *time.Time `json:"approved_at"`
	CreatedAt     time.Time  `json:"created_at"`
	MemberCount   int        `json:"member_count"`
	EventCount    int        `json:"event_count"`
}

// Member is a user's membership of an organiser.
type Member struct {
	UserID     int64     `json:"user_id"`
	Email      string    `json:"email"`
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	UserStatus string    `json:"user_status"`
	RoleID     int64     `json:"role_id"`
	RoleName   string    `json:"role_name"`
	JoinedAt   time.Time `json:"joined_at"`
}

// Service implements organiser use cases.
type Service struct {
	db                *database.DB
	identity          *identity.Service
	loader            *auth.PrincipalLoader
	defaultCommission int
	now               clock.Func
	logger            *slog.Logger
}

// NewService wires the organiser service.
func NewService(db *database.DB, identitySvc *identity.Service, loader *auth.PrincipalLoader, defaultCommissionBPS int, now clock.Func, logger *slog.Logger) *Service {
	return &Service{db: db, identity: identitySvc, loader: loader, defaultCommission: defaultCommissionBPS, now: now, logger: logger}
}

const selectOrganiser = `SELECT o.id, o.name, o.slug, o.email, o.phone, o.description, o.status, o.commission_bps, o.approved_at, o.created_at,
	(SELECT COUNT(*) FROM organiser_members m WHERE m.organiser_id = o.id),
	(SELECT COUNT(*) FROM events e WHERE e.organiser_id = o.id)
	FROM organisers o`

func scanOrganiser(row interface{ Scan(...any) error }) (Organiser, error) {
	var o Organiser
	err := row.Scan(&o.ID, &o.Name, &o.Slug, &o.Email, &o.Phone, &o.Description, &o.Status, &o.CommissionBPS,
		&o.ApprovedAt, &o.CreatedAt, &o.MemberCount, &o.EventCount)
	return o, err
}

func (s *Service) get(ctx context.Context, q database.Queryer, id int64) (Organiser, error) {
	o, err := scanOrganiser(q.QueryRowContext(ctx, selectOrganiser+` WHERE o.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, apperr.NotFound("Organiser")
	}
	return o, err
}

// canManageTeam reports whether p may manage organiserID's profile and team.
func canManageTeam(p *auth.Principal, organiserID int64) bool {
	return p.HasPlatform(rbac.OrganisersManage) || p.CanFor(organiserID, rbac.TeamManage)
}

// Get returns an organiser visible to p.
func (s *Service) Get(ctx context.Context, p *auth.Principal, id int64) (Organiser, error) {
	o, err := s.get(ctx, s.db, id)
	if err != nil {
		return o, err
	}
	if !p.HasPlatform(rbac.OrganisersView) && !p.IsMember(id) && !s.isMemberAnyStatus(ctx, p.UserID, id) {
		return Organiser{}, apperr.NotFound("Organiser")
	}
	return o, nil
}

func (s *Service) isMemberAnyStatus(ctx context.Context, userID, organiserID int64) bool {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM organiser_members WHERE organiser_id = ? AND user_id = ?`, organiserID, userID).Scan(&one)
	return err == nil
}

// Filter narrows the organiser list.
type Filter struct {
	Status string
	Query  string
}

// List returns organisers for platform staff.
func (s *Service) List(ctx context.Context, f Filter, page httpx.Page) ([]Organiser, int64, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if f.Status != "" {
		where = append(where, "o.status = ?")
		args = append(args, f.Status)
	}
	if f.Query != "" {
		where = append(where, "(o.name LIKE ? OR o.email LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like)
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM organisers o"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, selectOrganiser+clause+` ORDER BY FIELD(o.status, 'pending', 'active', 'suspended'), o.name LIMIT ? OFFSET ?`,
		append(args, page.Size, page.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Organiser
	for rows.Next() {
		o, err := scanOrganiser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// Input creates or edits an organiser.
type Input struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	Phone         string `json:"phone"`
	Description   string `json:"description"`
	CommissionBPS *int   `json:"commission_bps"`
	// OwnerEmail (create only) invites or links a user as the organiser's Event Organiser.
	OwnerEmail     string `json:"owner_email"`
	OwnerFirstName string `json:"owner_first_name"`
	OwnerLastName  string `json:"owner_last_name"`
}

func (in *Input) normalize() {
	in.Name, in.Phone, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Description)
	in.Email = validate.NormalizeEmail(in.Email)
	in.OwnerEmail = validate.NormalizeEmail(in.OwnerEmail)
}

func (in Input) validate() error {
	v := validate.New()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 150)
	v.Required("email", in.Email)
	v.Email("email", in.Email)
	v.Phone("phone", in.Phone)
	v.MaxLen("description", in.Description, 5000)
	if in.CommissionBPS != nil {
		v.Range("commission_bps", int64(*in.CommissionBPS), 0, 10_000)
	}
	v.Email("owner_email", in.OwnerEmail)
	return v.Err()
}

// Apply lets a signed-in user register their business as an organiser. The organiser stays
// pending until a platform administrator approves it; the applicant becomes its Event Organiser.
func (s *Service) Apply(ctx context.Context, p *auth.Principal, in Input) (Organiser, error) {
	in.normalize()
	in.CommissionBPS = nil
	if err := in.validate(); err != nil {
		return Organiser{}, err
	}
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM organiser_members m JOIN organisers o ON o.id = m.organiser_id
		WHERE m.user_id = ? AND o.status = 'pending'`, p.UserID).Scan(&pending); err != nil {
		return Organiser{}, err
	}
	if pending > 0 {
		return Organiser{}, apperr.Conflict("application_pending", "You already have an organiser application awaiting approval.")
	}
	id, err := s.create(ctx, p, in, StatusPending, p.UserID)
	if err != nil {
		return Organiser{}, err
	}
	s.loader.Invalidate(ctx, p.UserID)
	return s.get(ctx, s.db, id)
}

// Create adds an active organiser on behalf of the platform, optionally inviting its owner.
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (Organiser, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return Organiser{}, err
	}
	id, err := s.create(ctx, p, in, StatusActive, 0)
	if err != nil {
		return Organiser{}, err
	}
	return s.get(ctx, s.db, id)
}

func (s *Service) create(ctx context.Context, p *auth.Principal, in Input, status string, ownerUserID int64) (int64, error) {
	commission := s.defaultCommission
	if in.CommissionBPS != nil {
		commission = *in.CommissionBPS
	}
	var id, invitedUser int64
	var invite bool
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		organiserSlug, err := slug.Unique(ctx, tx, "organisers", "slug", slug.Make(in.Name))
		if err != nil {
			return err
		}
		var approvedAt, approvedBy any
		if status == StatusActive {
			approvedAt, approvedBy = now, p.UserID
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO organisers (name, slug, email, phone, description, status, commission_bps, approved_at, approved_by, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Name, organiserSlug, in.Email, in.Phone, in.Description, status, commission, approvedAt, approvedBy, p.UserID, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()

		owner := ownerUserID
		if owner == 0 && in.OwnerEmail != "" {
			owner, invite, err = s.identity.EnsureUser(ctx, tx, in.OwnerEmail, in.OwnerFirstName, in.OwnerLastName)
			if err != nil {
				return err
			}
			invitedUser = owner
		}
		if owner != 0 {
			roleID, err := s.identity.RoleIDBySlug(ctx, tx, rbac.RoleEventOrganiser)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO organiser_members (organiser_id, user_id, role_id, created_at) VALUES (?, ?, ?, ?)`,
				id, owner, roleID, now); err != nil {
				return err
			}
		}
		action := "organiser.created"
		if status == StatusPending {
			action = "organiser.applied"
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: action, EntityType: "organiser", EntityID: id, OrganiserID: id,
			Summary: fmt.Sprintf("Organiser %q created with status %s", in.Name, status)})
	})
	if err != nil {
		return 0, err
	}
	if invitedUser != 0 {
		s.loader.Invalidate(ctx, invitedUser)
		if invite {
			if err := s.identity.SendInvite(ctx, invitedUser, in.Name); err != nil {
				s.logger.Error("send organiser owner invite", "error", err)
			}
		}
	}
	return id, nil
}

// Update edits an organiser's profile. Only platform staff may change the commission.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id int64, in Input) (Organiser, error) {
	existing, err := s.get(ctx, s.db, id)
	if err != nil {
		return Organiser{}, err
	}
	if !canManageTeam(p, id) {
		return Organiser{}, apperr.NotFound("Organiser")
	}
	in.normalize()
	if !p.HasPlatform(rbac.OrganisersManage) {
		in.CommissionBPS = nil
	}
	if err := in.validate(); err != nil {
		return Organiser{}, err
	}
	commission := existing.CommissionBPS
	if in.CommissionBPS != nil {
		commission = *in.CommissionBPS
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE organisers SET name = ?, email = ?, phone = ?, description = ?, commission_bps = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.Email, in.Phone, in.Description, commission, now, id); err != nil {
			return err
		}
		diff := audit.Diff{}
		diff.Add("name", existing.Name, in.Name)
		diff.Add("email", existing.Email, in.Email)
		diff.Add("phone", existing.Phone, in.Phone)
		diff.Add("commission_bps", existing.CommissionBPS, commission)
		if existing.Description != in.Description {
			diff["description"] = audit.Change{From: "(previous text)", To: "(updated text)"}
		}
		if len(diff) == 0 {
			return nil
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "organiser.updated", EntityType: "organiser", EntityID: id, OrganiserID: id,
			Summary: "Updated " + diff.Fields(), Changes: diff})
	})
	if err != nil {
		return Organiser{}, err
	}
	return s.get(ctx, s.db, id)
}

// SetStatus approves, suspends or reactivates an organiser. Suspension immediately removes
// every member's access to the organiser's data; published events stay visible to buyers
// unless they are cancelled separately.
func (s *Service) SetStatus(ctx context.Context, p *auth.Principal, id int64, status string) (Organiser, error) {
	existing, err := s.get(ctx, s.db, id)
	if err != nil {
		return Organiser{}, err
	}
	allowed := map[string][]string{
		StatusActive:    {StatusPending, StatusSuspended},
		StatusSuspended: {StatusActive, StatusPending},
	}
	if !contains(allowed[status], existing.Status) {
		return Organiser{}, apperr.Conflict("invalid_status", fmt.Sprintf("An organiser that is %s cannot be set to %s.", existing.Status, status))
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		action := "organiser.suspended"
		if status == StatusActive {
			action = "organiser.activated"
			if existing.Status == StatusPending {
				action = "organiser.approved"
			}
			if _, err := tx.ExecContext(ctx, `UPDATE organisers SET status = ?, approved_at = COALESCE(approved_at, ?), approved_by = COALESCE(approved_by, ?), updated_at = ? WHERE id = ?`,
				status, now, p.UserID, now, id); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE organisers SET status = ?, updated_at = ? WHERE id = ?`, status, now, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: action, EntityType: "organiser", EntityID: id, OrganiserID: id,
			Summary: fmt.Sprintf("Organiser %q changed from %s to %s", existing.Name, existing.Status, status)})
	})
	if err != nil {
		return Organiser{}, err
	}
	s.loader.InvalidateAll(ctx)
	return s.get(ctx, s.db, id)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
