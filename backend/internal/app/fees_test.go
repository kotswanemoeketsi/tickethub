package app_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/reports"
)

// Exercise actual checkout, immutable snapshots, partial/full refunds, historical
// sales and both financial report builders against MySQL.
func TestTicketFeeReportsAndRefunds(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 20, ticketType("Paid", 20000, 15), ticketType("Free", 0, 5))
	_, customer := e.user("fee-buyer")
	o := e.buy(customer, fx.EventID, fx.Types[0], 2)
	check := func(comm, fixed, net int64) {
		t.Helper()
		for _, name := range []string{"revenue", "organiser-performance"} {
			url := fmt.Sprintf("/api/v1/admin/reports/%s?from=%s&to=%s&organiser_id=%d", name, e.clock.Now().Add(-24*time.Hour).Format("2006-01-02"), e.clock.Now().Add(24*time.Hour).Format("2006-01-02"), fx.OrganiserID)
			var table reports.Table
			e.must(200, e.request("GET", url, admin, nil)).decode(t, &table)
			want := map[string]int64{"commission_fees": comm, "ticket_fees": fixed, "fees": comm + fixed, "organiser": net - comm - fixed, "earnings": net - comm - fixed, "net": net}
			for i, c := range table.Columns {
				if v, ok := want[c.Key]; ok && c.Kind == reports.KindMoney && table.Totals[i] != float64(v) {
					t.Fatalf("%s %s = %v, want %d", name, c.Key, table.Totals[i], v)
				}
			}
			csv, err := table.ToCSV()
			if err != nil || !strings.Contains(string(csv), "Ticket fees") {
				t.Fatalf("CSV missing fees: %v", err)
			}
			if _, err := table.ToXLSX(); err != nil {
				t.Fatal(err)
			}
			// Use the endpoint for PDF so dates retain their typed values.
			e.must(200, e.request("GET", url+"&format=pdf", admin, nil))
		}
	}
	check(1400, 1000, 40000)
	// Changing an organiser's current rate must not rewrite settled charges.
	if _, err := e.app.DB.ExecContext(e.ctx, "UPDATE organisers SET commission_bps = 900 WHERE id = ?", fx.OrganiserID); err != nil {
		t.Fatal(err)
	}
	check(1400, 1000, 40000)
	for i, tk := range o.Tickets {
		var refund struct {
			ID int64 `json:"id"`
		}
		e.must(201, e.request("POST", fmt.Sprintf("/api/v1/orders/%d/refunds", o.ID), customer, map[string]any{"ticket_ids": []int64{tk.ID}, "reason": "Cannot attend"})).decode(t, &refund)
		e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/refunds/%d/approve", refund.ID), admin, nil))
		e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/refunds/%d/process", refund.ID), admin, nil))
		remaining := int64(1 - i)
		check(700*remaining, 500*remaining, 20000*remaining)
	}
	// Free orders never accrue a fixed fee.
	free, r := e.reserve(customer, fx.EventID, fx.Types[1], 2, "")
	e.must(201, r)
	_, r = e.checkout(customer, free, "online")
	e.must(200, r)
	check(0, 0, 0)
	// Legacy sales have NULL commission snapshots and no fixed fee. Their original
	// order-level percentage still applies, independently of the new default.
	legacy := e.buy(customer, fx.EventID, fx.Types[0], 1)
	if _, err := e.app.DB.ExecContext(e.ctx, "UPDATE orders SET commission_cents = NULL, ticket_fees_cents = 0, commission_bps = 500 WHERE id = ?", legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.DB.ExecContext(e.ctx, "UPDATE tickets SET commission_cents = 0, ticket_fee_cents = 0 WHERE order_id = ?", legacy.ID); err != nil {
		t.Fatal(err)
	}
	check(1000, 0, 20000)
}
