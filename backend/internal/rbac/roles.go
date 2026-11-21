package rbac

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
)

// Role scopes.
const (
	ScopePlatform  = "platform"
	ScopeOrganiser = "organiser"
)

// System role slugs.
const (
	RoleSuperAdmin       = "super_admin"
	RoleAdmin            = "admin"
	RoleFinanceManager   = "finance_manager"
	RoleTicketingOfficer = "ticketing_officer"
	RoleCheckinOfficer   = "checkin_officer"
	RoleCustomer         = "customer"
	RoleEventOrganiser   = "event_organiser"
	RoleOrganiserCheckin = "organiser_checkin"
)

// RoleDefinition is a system role with its default permissions. Defaults are only applied
// when the role is first created; afterwards administrators own the mapping (except for
// the super administrator, which always holds every permission).
type RoleDefinition struct {
	Slug        string
	Name        string
	Description string
	Scope       string
	Permissions []string
}

// SystemRoles are created on first start-up.
var SystemRoles = []RoleDefinition{
	{
		Slug: RoleSuperAdmin, Name: "Super Administrator", Scope: ScopePlatform,
		Description: "Full system access and configuration",
		Permissions: AllCodes(),
	},
	{
		Slug: RoleAdmin, Name: "Administrator", Scope: ScopePlatform,
		Description: "Manage users, events, reports and operations",
		Permissions: AllCodes(),
	},
	{
		Slug: RoleFinanceManager, Name: "Finance Manager", Scope: ScopePlatform,
		Description: "Financial reports, payments and refunds",
		Permissions: []string{
			DashboardView, EventsView, OrdersView, PaymentsView, PaymentsConfirm,
			RefundsView, RefundsManage, RefundsProcess,
			ReportsView, ReportsFinancial, ReportsExport, OrganisersView, AllOrganisers,
		},
	},
	{
		Slug: RoleTicketingOfficer, Name: "Ticketing Officer", Scope: ScopePlatform,
		Description: "Manage ticket sales and orders",
		Permissions: []string{
			DashboardView, EventsView, OrdersView, OrdersManage, TicketsComplimentary,
			AttendeesView, PaymentsView, RefundsView, RefundsManage, ReportsView, AllOrganisers,
		},
	},
	{
		Slug: RoleCheckinOfficer, Name: "Check-in Officer", Scope: ScopePlatform,
		Description: "Scan tickets and manage event attendance at any event",
		Permissions: []string{EventsView, AttendeesView, CheckinScan, AllOrganisers},
	},
	{
		Slug: RoleCustomer, Name: "Customer", Scope: ScopePlatform,
		Description: "Browse events, purchase and manage tickets",
		Permissions: []string{},
	},
	{
		Slug: RoleEventOrganiser, Name: "Event Organiser", Scope: ScopeOrganiser,
		Description: "Create and manage the organiser's events, tickets and team",
		Permissions: []string{
			DashboardView, EventsView, EventsCreate, EventsUpdate, EventsDelete, EventsCancel,
			TicketTypesManage, PromotionsManage, TicketsComplimentary,
			OrdersView, OrdersManage, PaymentsView, RefundsView, RefundsManage,
			AttendeesView, CheckinScan, ReportsView, ReportsFinancial, ReportsExport,
			TeamManage, VenuesManage, AuditView,
		},
	},
	{
		Slug: RoleOrganiserCheckin, Name: "Organiser Check-in Staff", Scope: ScopeOrganiser,
		Description: "Scan tickets at the organiser's events",
		Permissions: []string{EventsView, AttendeesView, CheckinScan},
	},
}

// Sync writes the permission catalogue to the database, removes retired permissions,
// creates missing system roles and keeps the super administrator complete.
func Sync(ctx context.Context, db *database.DB, now time.Time) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, p := range Catalogue {
			if _, err := tx.ExecContext(ctx, `INSERT INTO permissions (code, module, description) VALUES (?, ?, ?)
				ON DUPLICATE KEY UPDATE module = VALUES(module), description = VALUES(description)`,
				p.Code, p.Module, p.Description); err != nil {
				return fmt.Errorf("sync permission %s: %w", p.Code, err)
			}
		}
		codes := AllCodes()
		if _, err := tx.ExecContext(ctx, `DELETE FROM permissions WHERE code NOT IN (`+database.Placeholders(len(codes))+`)`,
			database.Args(codes)...); err != nil {
			return fmt.Errorf("remove retired permissions: %w", err)
		}

		for _, def := range SystemRoles {
			var roleID int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM roles WHERE slug = ?`, def.Slug).Scan(&roleID)
			created := false
			switch {
			case err == sql.ErrNoRows:
				res, err := tx.ExecContext(ctx, `INSERT INTO roles (slug, name, description, scope, is_system, created_at, updated_at)
					VALUES (?, ?, ?, ?, TRUE, ?, ?)`, def.Slug, def.Name, def.Description, def.Scope, now, now)
				if err != nil {
					return fmt.Errorf("create role %s: %w", def.Slug, err)
				}
				roleID, _ = res.LastInsertId()
				created = true
			case err != nil:
				return err
			}
			if created || def.Slug == RoleSuperAdmin {
				if err := grant(ctx, tx, roleID, def.Permissions); err != nil {
					return fmt.Errorf("grant permissions to %s: %w", def.Slug, err)
				}
			}
		}
		return nil
	})
}

func grant(ctx context.Context, tx *sql.Tx, roleID int64, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO role_permissions (role_id, permission_id)
		SELECT ?, id FROM permissions WHERE code IN (`+database.Placeholders(len(codes))+`)`,
		append([]any{roleID}, database.Args(codes)...)...)
	return err
}
