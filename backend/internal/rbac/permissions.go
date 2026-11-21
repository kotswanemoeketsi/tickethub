// Package rbac defines the permission catalogue and the default roles.
//
// Permissions are owned by the code, because the code is what enforces them; roles are data
// that administrators configure. On start-up Sync writes the catalogue to the database and
// creates any missing system roles, so new permissions appear without manual steps.
package rbac

// Permission codes. Handlers check these, never role names.
const (
	DashboardView = "dashboard.view"

	EventsView   = "events.view"
	EventsCreate = "events.create"
	EventsUpdate = "events.update"
	EventsDelete = "events.delete"
	EventsCancel = "events.cancel"
	EventsReview = "events.review"

	TicketTypesManage    = "ticket_types.manage"
	PromotionsManage     = "promotions.manage"
	TicketsComplimentary = "tickets.complimentary"

	OrdersView   = "orders.view"
	OrdersManage = "orders.manage"

	PaymentsView    = "payments.view"
	PaymentsConfirm = "payments.confirm"

	RefundsView    = "refunds.view"
	RefundsManage  = "refunds.manage"
	RefundsProcess = "refunds.process"

	AttendeesView = "attendees.view"
	CheckinScan   = "checkin.scan"

	ReportsView      = "reports.view"
	ReportsFinancial = "reports.financial"
	ReportsExport    = "reports.export"

	OrganisersView   = "organisers.view"
	OrganisersManage = "organisers.manage"
	TeamManage       = "team.manage"

	CategoriesManage = "categories.manage"
	VenuesManage     = "venues.manage"

	UsersView   = "users.view"
	UsersManage = "users.manage"
	RolesManage = "roles.manage"

	AuditView = "audit.view"

	// AllOrganisers lets a platform role act on every organiser's data. Without it a user
	// only reaches the organisers they are a member of.
	AllOrganisers = "platform.all_organisers"
)

// Permission describes one entry of the catalogue.
type Permission struct {
	Code        string `json:"code"`
	Module      string `json:"module"`
	Description string `json:"description"`
	// Organiser reports whether the permission may be granted by an organiser-scoped role.
	// Platform-only permissions (user administration, event approval, money movement)
	// can only be granted through platform roles.
	Organiser bool `json:"organiser"`
}

// Catalogue lists every permission, grouped by module for display.
var Catalogue = []Permission{
	{DashboardView, "Dashboard", "View the dashboard", true},

	{EventsView, "Events", "View events", true},
	{EventsCreate, "Events", "Create events", true},
	{EventsUpdate, "Events", "Edit events and submit them for review", true},
	{EventsDelete, "Events", "Delete draft events", true},
	{EventsCancel, "Events", "Cancel events", true},
	{EventsReview, "Events", "Approve or reject events for publishing", false},

	{TicketTypesManage, "Tickets", "Manage ticket types, pricing and availability", true},
	{PromotionsManage, "Tickets", "Manage discount and promotional codes", true},
	{TicketsComplimentary, "Tickets", "Issue complimentary tickets", true},

	{OrdersView, "Orders", "View orders and tickets", true},
	{OrdersManage, "Orders", "Cancel orders, resend tickets and edit attendees", true},

	{PaymentsView, "Payments", "View payments", true},
	{PaymentsConfirm, "Payments", "Confirm manual EFT payments", false},

	{RefundsView, "Refunds", "View refunds", true},
	{RefundsManage, "Refunds", "Create, approve and reject refunds", true},
	{RefundsProcess, "Refunds", "Process approved refunds (move money)", false},

	{AttendeesView, "Attendees", "View attendees and attendance", true},
	{CheckinScan, "Attendees", "Scan tickets and check attendees in and out", true},

	{ReportsView, "Reports", "View sales, event and attendee reports", true},
	{ReportsFinancial, "Reports", "View revenue, payment, refund and organiser reports", true},
	{ReportsExport, "Reports", "Export reports to CSV, Excel and PDF", true},

	{OrganisersView, "Organisers", "View all organisers", false},
	{OrganisersManage, "Organisers", "Create, approve and suspend organisers", false},
	{TeamManage, "Organisers", "Manage the organiser's team members and profile", true},

	{CategoriesManage, "Catalogue", "Manage event categories", false},
	{VenuesManage, "Catalogue", "Manage venues", true},

	{UsersView, "Users", "View users", false},
	{UsersManage, "Users", "Create and edit users, assign roles, activate and deactivate accounts", false},
	{RolesManage, "Users", "Create roles and configure permissions", false},

	{AuditView, "Audit", "View audit logs", true},

	{AllOrganisers, "Platform", "Access the data of all organisers", false},
}

var catalogueIndex = func() map[string]Permission {
	m := make(map[string]Permission, len(Catalogue))
	for _, p := range Catalogue {
		m[p.Code] = p
	}
	return m
}()

// Lookup returns the catalogue entry for code.
func Lookup(code string) (Permission, bool) {
	p, ok := catalogueIndex[code]
	return p, ok
}

// AllCodes returns every permission code.
func AllCodes() []string {
	out := make([]string, len(Catalogue))
	for i, p := range Catalogue {
		out[i] = p.Code
	}
	return out
}
