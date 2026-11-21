package organisers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/identity"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Members lists an organiser's team.
func (s *Service) Members(ctx context.Context, p *auth.Principal, organiserID int64) ([]Member, error) {
	if _, err := s.get(ctx, s.db, organiserID); err != nil {
		return nil, err
	}
	if !canManageTeam(p, organiserID) {
		return nil, apperr.NotFound("Organiser")
	}
	return s.members(ctx, s.db, organiserID)
}

func (s *Service) members(ctx context.Context, q database.Queryer, organiserID int64) ([]Member, error) {
	rows, err := q.QueryContext(ctx, `SELECT u.id, u.email, u.first_name, u.last_name, u.status, r.id, r.name, m.created_at
		FROM organiser_members m JOIN users u ON u.id = m.user_id JOIN roles r ON r.id = m.role_id
		WHERE m.organiser_id = ? ORDER BY u.first_name, u.last_name`, organiserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.FirstName, &m.LastName, &m.UserStatus, &m.RoleID, &m.RoleName, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemberInput adds a team member by email, inviting them if they have no account yet.
type MemberInput struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	RoleID    int64  `json:"role_id"`
}

// AddMember adds a user to the organiser's team with an organiser-scoped role.
func (s *Service) AddMember(ctx context.Context, p *auth.Principal, organiserID int64, in MemberInput) (Member, error) {
	org, err := s.get(ctx, s.db, organiserID)
	if err != nil {
		return Member{}, err
	}
	if !canManageTeam(p, organiserID) {
		return Member{}, apperr.NotFound("Organiser")
	}
	var userID int64
	var created bool
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		role, err := s.grantableRole(ctx, tx, p, organiserID, in.RoleID)
		if err != nil {
			return err
		}
		userID, created, err = s.identity.EnsureUser(ctx, tx, in.Email, in.FirstName, in.LastName)
		if err != nil {
			return err
		}
		now := s.now()
		_, err = tx.ExecContext(ctx, `INSERT INTO organiser_members (organiser_id, user_id, role_id, created_at) VALUES (?, ?, ?, ?)`,
			organiserID, userID, role.ID, now)
		if database.IsDuplicateKey(err) {
			return apperr.Conflict("already_member", "This person is already a member of the team.")
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "organiser.member_added", EntityType: "organiser", EntityID: organiserID,
			OrganiserID: organiserID, Summary: fmt.Sprintf("%s added to %s as %s", in.Email, org.Name, role.Name)})
	})
	if err != nil {
		return Member{}, err
	}
	s.loader.Invalidate(ctx, userID)
	if created {
		if err := s.identity.SendInvite(ctx, userID, org.Name); err != nil {
			s.logger.Error("send team invite", "error", err)
		}
	}
	return s.member(ctx, organiserID, userID)
}

// UpdateMemberRole changes a member's role.
func (s *Service) UpdateMemberRole(ctx context.Context, p *auth.Principal, organiserID, userID, roleID int64) (Member, error) {
	if _, err := s.get(ctx, s.db, organiserID); err != nil {
		return Member{}, err
	}
	if !canManageTeam(p, organiserID) {
		return Member{}, apperr.NotFound("Organiser")
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		current, err := s.memberRole(ctx, tx, organiserID, userID)
		if err != nil {
			return err
		}
		if _, err := s.grantableRole(ctx, tx, p, organiserID, current.ID); err != nil {
			return err
		}
		role, err := s.grantableRole(ctx, tx, p, organiserID, roleID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE organiser_members SET role_id = ? WHERE organiser_id = ? AND user_id = ?`, role.ID, organiserID, userID); err != nil {
			return err
		}
		if err := s.ensureTeamManagerRemains(ctx, tx, organiserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "organiser.member_role_changed", EntityType: "organiser", EntityID: organiserID,
			OrganiserID: organiserID, Summary: fmt.Sprintf("Member #%d changed from %s to %s", userID, current.Name, role.Name)})
	})
	if err != nil {
		return Member{}, err
	}
	s.loader.Invalidate(ctx, userID)
	return s.member(ctx, organiserID, userID)
}

// RemoveMember removes a user from the team.
func (s *Service) RemoveMember(ctx context.Context, p *auth.Principal, organiserID, userID int64) error {
	if _, err := s.get(ctx, s.db, organiserID); err != nil {
		return err
	}
	if !canManageTeam(p, organiserID) {
		return apperr.NotFound("Organiser")
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		current, err := s.memberRole(ctx, tx, organiserID, userID)
		if err != nil {
			return err
		}
		if _, err := s.grantableRole(ctx, tx, p, organiserID, current.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM organiser_members WHERE organiser_id = ? AND user_id = ?`, organiserID, userID); err != nil {
			return err
		}
		if err := s.ensureTeamManagerRemains(ctx, tx, organiserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "organiser.member_removed", EntityType: "organiser", EntityID: organiserID,
			OrganiserID: organiserID, Summary: fmt.Sprintf("Member #%d (%s) removed from the team", userID, current.Name)})
	})
	if err != nil {
		return err
	}
	s.loader.Invalidate(ctx, userID)
	return nil
}

func (s *Service) member(ctx context.Context, organiserID, userID int64) (Member, error) {
	members, err := s.members(ctx, s.db, organiserID)
	if err != nil {
		return Member{}, err
	}
	for _, m := range members {
		if m.UserID == userID {
			return m, nil
		}
	}
	return Member{}, apperr.NotFound("Member")
}

func (s *Service) memberRole(ctx context.Context, q database.Queryer, organiserID, userID int64) (identity.Role, error) {
	var roleID int64
	err := q.QueryRowContext(ctx, `SELECT role_id FROM organiser_members WHERE organiser_id = ? AND user_id = ? FOR UPDATE`, organiserID, userID).Scan(&roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Role{}, apperr.NotFound("Member")
	}
	if err != nil {
		return identity.Role{}, err
	}
	return s.identity.RoleByID(ctx, q, roleID)
}

// grantableRole loads an organiser role and checks that p could grant every permission in it
// within this organiser, preventing privilege escalation by team managers.
func (s *Service) grantableRole(ctx context.Context, q database.Queryer, p *auth.Principal, organiserID, roleID int64) (identity.Role, error) {
	role, err := s.identity.OrganiserRole(ctx, q, roleID)
	if err != nil {
		return role, err
	}
	if p.HasPlatform(rbac.OrganisersManage) {
		return role, nil
	}
	for _, perm := range role.Permissions {
		if !p.CanFor(organiserID, perm) {
			return role, apperr.Forbidden(fmt.Sprintf("You cannot assign %q because it includes permissions you do not have.", role.Name))
		}
	}
	return role, nil
}

// ensureTeamManagerRemains keeps at least one member able to manage the team.
func (s *Service) ensureTeamManagerRemains(ctx context.Context, q database.Queryer, organiserID int64) error {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM organiser_members m
		JOIN role_permissions rp ON rp.role_id = m.role_id JOIN permissions p ON p.id = rp.permission_id
		WHERE m.organiser_id = ? AND p.code = ?`, organiserID, rbac.TeamManage).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.Conflict("last_team_manager", "The team must keep at least one member who can manage it.")
	}
	return nil
}
