package app_test

import (
	"fmt"
	"testing"
	"time"
)

// The dashboard and the reports can be narrowed to one event, and only to events the user
// may see.
func TestEventFilters(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	a := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	b := e.publishedEvent(admin, 96*time.Hour, 50, ticketType("General", 20000, 50))
	_, customer := e.user("cust")
	e.buy(customer, a.EventID, a.Types[0], 2)
	e.buy(customer, b.EventID, b.Types[0], 1)

	var dash struct {
		TicketsSold int64 `json:"tickets_sold"`
		NetRevenue  int64 `json:"net_revenue_cents"`
		Inventory   struct {
			Capacity int64 `json:"capacity"`
			Sold     int64 `json:"sold"`
		} `json:"inventory"`
		Upcoming []struct{ ID int64 } `json:"upcoming_list"`
		Event    *struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"event"`
	}
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/dashboard?event_id=%d", a.EventID), admin, nil)).decode(t, &dash)
	if dash.Event == nil || dash.Event.ID != a.EventID || dash.TicketsSold != 2 || dash.NetRevenue != 20000 ||
		dash.Inventory.Capacity != 50 || dash.Inventory.Sold != 2 || len(dash.Upcoming) != 0 {
		t.Fatalf("dashboard for event A: %+v", dash)
	}
	dash.Event = nil
	e.must(200, e.request("GET", "/api/v1/admin/dashboard", admin, nil)).decode(t, &dash)
	if dash.Event != nil || dash.TicketsSold < 3 {
		t.Fatalf("unfiltered dashboard: %+v", dash)
	}

	// Organiser A sees their own event, and another organiser's event does not exist for them.
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/dashboard?event_id=%d", a.EventID), a.OwnerToken, nil))
	e.must(404, e.request("GET", fmt.Sprintf("/api/v1/admin/dashboard?event_id=%d", b.EventID), a.OwnerToken, nil))
	e.must(404, e.request("GET", fmt.Sprintf("/api/v1/admin/reports/ticket-sales?event_id=%d", b.EventID), a.OwnerToken, nil))

	var report struct {
		Description string  `json:"description"`
		Rows        [][]any `json:"rows"`
	}
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/reports/payments?event_id=%d", a.EventID), admin, nil)).decode(t, &report)
	if len(report.Rows) != 1 {
		t.Fatalf("payments for event A: %d rows, want 1", len(report.Rows))
	}
	e.must(200, e.request("GET", "/api/v1/admin/reports/payments", admin, nil)).decode(t, &report)
	if len(report.Rows) < 2 {
		t.Fatalf("all payments: %d rows, want at least 2", len(report.Rows))
	}
	// Event performance shows a chosen event even when it falls outside the date range.
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/reports/event-performance?event_id=%d&from=2020-01-01&to=2020-01-31", b.EventID), admin, nil)).decode(t, &report)
	if len(report.Rows) != 1 {
		t.Fatalf("event performance for event B: %d rows, want 1", len(report.Rows))
	}
	// Organiser performance is per organiser, so an event filter is ignored rather than refused.
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/reports/organiser-performance?event_id=%d", a.EventID), admin, nil))
}

// Administrators manage users, but only a super administrator can make someone a super
// administrator, by creating a user or by changing an existing user's roles.
func TestAdministratorCannotCreateSuperAdministrator(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("ops", "admin")
	var roles []struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	e.must(200, e.request("GET", "/api/v1/admin/roles?scope=platform", admin, nil)).decode(t, &roles)
	var superRole int64
	for _, r := range roles {
		if r.Slug == "super_admin" {
			superRole = r.ID
		}
	}
	if superRole == 0 {
		t.Fatal("super administrator role not listed")
	}

	e.must(403, e.request("POST", "/api/v1/admin/users", admin, map[string]any{
		"first_name": "New", "last_name": "Boss", "email": "boss@example.test", "password": testPassword, "role_ids": []int64{superRole},
	}))
	var created struct {
		ID int64 `json:"id"`
	}
	e.must(201, e.request("POST", "/api/v1/admin/users", admin, map[string]any{
		"first_name": "New", "last_name": "Staff", "email": "staff@example.test", "password": testPassword,
	})).decode(t, &created)
	e.must(403, e.request("PUT", fmt.Sprintf("/api/v1/admin/users/%d/roles", created.ID), admin, map[string]any{"role_ids": []int64{superRole}}))

	// A super administrator can.
	e.must(200, e.request("PUT", fmt.Sprintf("/api/v1/admin/users/%d/roles", created.ID), e.login(superAdminMail), map[string]any{"role_ids": []int64{superRole}}))
}
