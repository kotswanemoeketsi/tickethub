package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Filter selects the data a report covers. Dates are interpreted in the platform time zone.
type Filter struct {
	From        time.Time // inclusive, UTC
	To          time.Time // exclusive, UTC
	EventID     int64
	OrganiserID int64
}

// Definition describes a report and how to build it.
type Definition struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
	NeedsEvent  bool   `json:"needs_event"`
	// EventFilter is "required", "optional" or "none": whether the report can be narrowed
	// to one event.
	EventFilter string `json:"event_filter"`
	build       func(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error)
}

// Definitions lists every report in display order.
var Definitions = []Definition{
	{Name: "ticket-sales", Title: "Ticket sales", Description: "Tickets sold, complimentary and refunded per ticket type", Permission: rbac.ReportsView, EventFilter: eventOptional, build: ticketSales},
	{Name: "sales-by-category", Title: "Sales by ticket category", Description: "Tickets and revenue by ticket category (VIP, General, Early Bird...)", Permission: rbac.ReportsView, EventFilter: eventOptional, build: salesByCategory},
	{Name: "event-performance", Title: "Event performance", Description: "Sell-through, revenue and attendance per event", Permission: rbac.ReportsView, EventFilter: eventOptional, build: eventPerformance},
	{Name: "attendees", Title: "Attendee list", Description: "Every ticket holder of an event and their check-in status", Permission: rbac.ReportsView, NeedsEvent: true, EventFilter: eventRequired, build: attendees},
	{Name: "revenue", Title: "Revenue", Description: "Daily gross sales, refunds, platform fees and organiser earnings", Permission: rbac.ReportsFinancial, EventFilter: eventOptional, build: revenue},
	{Name: "organiser-performance", Title: "Organiser performance", Description: "Sales, refunds and earnings per organiser", Permission: rbac.ReportsFinancial, EventFilter: eventNone, build: organiserPerformance},
	{Name: "payments", Title: "Payments", Description: "Online and EFT payments with their status", Permission: rbac.ReportsFinancial, EventFilter: eventOptional, build: paymentsReport},
	{Name: "refunds", Title: "Refunds", Description: "Refunds requested, approved and processed", Permission: rbac.ReportsFinancial, EventFilter: eventOptional, build: refundsReport},
}

const (
	eventRequired = "required"
	eventOptional = "optional"
	eventNone     = "none"
)

func definition(name string) (Definition, bool) {
	for _, d := range Definitions {
		if d.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}

// Service builds reports.
type Service struct {
	db       *database.DB
	loc      *time.Location
	currency string
	appName  string
	now      clock.Func
}

// NewService returns the report service.
func NewService(db *database.DB, timezone, currency, appName string, now clock.Func) *Service {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return &Service{db: db, loc: loc, currency: currency, appName: appName, now: now}
}

// Build runs a report for p.
func (s *Service) Build(ctx context.Context, p *auth.Principal, name string, f Filter) (*Table, error) {
	def, ok := definition(name)
	if !ok {
		return nil, apperr.NotFound("Report")
	}
	if !p.CanAny(def.Permission) {
		return nil, auth.ErrForbidden()
	}
	switch {
	case def.EventFilter == eventNone:
		f.EventID = 0
	case def.EventFilter == eventRequired && f.EventID == 0:
		return nil, apperr.FieldError("event_id", "choose an event for this report")
	}
	scope := p.Scope(def.Permission).Narrow(f.OrganiserID)
	var eventTitle string
	if f.EventID > 0 {
		// The event must be one this user may report on; others are reported as missing.
		scopeSQL, args := scope.SQL("e.organiser_id")
		err := s.db.QueryRowContext(ctx, `SELECT e.title FROM events e WHERE `+scopeSQL+` AND e.id = ?`, append(args, f.EventID)...).Scan(&eventTitle)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperr.NotFound("Event")
		}
		if err != nil {
			return nil, err
		}
	}
	t, err := def.build(ctx, s, scope, f)
	if err != nil {
		return nil, err
	}
	t.Name, t.Title, t.Currency, t.loc = def.Name, def.Title, s.currency, s.loc
	t.GeneratedAt = s.now().In(s.loc).Format("2006-01-02 15:04 MST")
	if t.Description == "" {
		t.Description = s.describe(f)
		if eventTitle != "" {
			t.Description = eventTitle + ", " + t.Description
		}
	}
	if t.Rows == nil {
		t.Rows = [][]any{}
	}
	return t, nil
}

func (s *Service) describe(f Filter) string {
	return fmt.Sprintf("%s to %s", f.From.In(s.loc).Format("2 Jan 2006"), f.To.Add(-time.Second).In(s.loc).Format("2 Jan 2006"))
}

// offsetMinutes is used to group UTC timestamps by local calendar day in SQL without
// relying on the server's time zone tables.
func (s *Service) offsetMinutes(at time.Time) int {
	_, offset := at.In(s.loc).Zone()
	return offset / 60
}

func query(ctx context.Context, s *Service, q string, args []any, scan func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func eventClause(f Filter, column string) (string, []any) {
	if f.EventID > 0 {
		return " AND " + column + " = ?", []any{f.EventID}
	}
	return "", nil
}

// ---- Report builders ------------------------------------------------------------------

func ticketSales(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, args := scope.SQL("x.organiser_id")
	evSQL, evArgs := eventClause(f, "t.event_id")
	args = append(append(args, f.From, f.To), evArgs...)
	t := &Table{Columns: []Column{
		{"event", "Event", KindText}, {"ticket_type", "Ticket type", KindText}, {"category", "Category", KindText},
		{"price", "Price", KindMoney}, {"sold", "Sold", KindInt}, {"complimentary", "Complimentary", KindInt},
		{"refunded", "Refunded", KindInt}, {"gross", "Gross sales", KindMoney}, {"refunds", "Refunds", KindMoney}, {"net", "Net sales", KindMoney},
	}}
	var sold, comps, refunded, gross, refunds int64
	err := query(ctx, s, `SELECT e.title, tt.name, tt.category, tt.price_cents,
		SUM(t.is_complimentary = 0), SUM(t.is_complimentary = 1), SUM(t.status = 'refunded'),
		SUM(t.price_cents), SUM(CASE WHEN t.status = 'refunded' THEN t.price_cents ELSE 0 END)
		FROM tickets t JOIN orders x ON x.id = t.order_id JOIN ticket_types tt ON tt.id = t.ticket_type_id JOIN events e ON e.id = t.event_id
		WHERE `+scopeSQL+` AND x.paid_at >= ? AND x.paid_at < ?`+evSQL+`
		GROUP BY e.id, tt.id, e.title, tt.name, tt.category, tt.price_cents, e.starts_at, tt.sort_order
		ORDER BY e.starts_at, e.id, tt.sort_order, tt.id`, args, func(rows *sql.Rows) error {
		var event, name, category string
		var price, nSold, nComps, nRefunded, g, r int64
		if err := rows.Scan(&event, &name, &category, &price, &nSold, &nComps, &nRefunded, &g, &r); err != nil {
			return err
		}
		t.Rows = append(t.Rows, []any{event, name, humanize(category), price, nSold, nComps, nRefunded, g, r, g - r})
		sold, comps, refunded, gross, refunds = sold+nSold, comps+nComps, refunded+nRefunded, gross+g, refunds+r
		return nil
	})
	t.Totals = []any{"Total", nil, nil, nil, sold, comps, refunded, gross, refunds, gross - refunds}
	return t, err
}

func salesByCategory(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, args := scope.SQL("x.organiser_id")
	evSQL, evArgs := eventClause(f, "t.event_id")
	args = append(append(args, f.From, f.To), evArgs...)
	type row struct {
		category      string
		sold, revenue int64
	}
	var list []row
	var totalSold, totalRevenue int64
	err := query(ctx, s, `SELECT tt.category, COUNT(*), SUM(t.price_cents)
		FROM tickets t JOIN orders x ON x.id = t.order_id JOIN ticket_types tt ON tt.id = t.ticket_type_id
		WHERE `+scopeSQL+` AND x.paid_at >= ? AND x.paid_at < ? AND t.status <> 'refunded'`+evSQL+`
		GROUP BY tt.category`, args, func(rows *sql.Rows) error {
		var r row
		if err := rows.Scan(&r.category, &r.sold, &r.revenue); err != nil {
			return err
		}
		list = append(list, r)
		totalSold, totalRevenue = totalSold+r.sold, totalRevenue+r.revenue
		return nil
	})
	sort.Slice(list, func(i, j int) bool { return list[i].revenue > list[j].revenue })
	t := &Table{Columns: []Column{
		{"category", "Ticket category", KindText}, {"tickets", "Tickets", KindInt}, {"ticket_share", "Share of tickets", KindPercent},
		{"revenue", "Revenue", KindMoney}, {"revenue_share", "Share of revenue", KindPercent},
	}}
	for _, r := range list {
		t.Rows = append(t.Rows, []any{humanize(r.category), r.sold, ratio(r.sold, totalSold), r.revenue, ratio(r.revenue, totalRevenue)})
	}
	t.Totals = []any{"Total", totalSold, nil, totalRevenue, nil}
	return t, err
}

func eventPerformance(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, args := scope.SQL("e.organiser_id")
	evSQL, evArgs := eventClause(f, "e.id")
	// A chosen event is shown whatever its date; otherwise events starting in the period.
	args = append(append(args, f.EventID > 0, f.From, f.To), evArgs...)
	t := &Table{Columns: []Column{
		{"event", "Event", KindText}, {"organiser", "Organiser", KindText}, {"starts_at", "Starts", KindDateTime}, {"status", "Status", KindText},
		{"capacity", "Capacity", KindInt}, {"sold", "Tickets sold", KindInt}, {"sell_through", "Sell-through", KindPercent},
		{"revenue", "Net revenue", KindMoney}, {"checked_in", "Checked in", KindInt}, {"attendance", "Attendance", KindPercent},
	}}
	var capacity, sold, revenue, issued, checkedIn int64
	now := s.now()
	err := query(ctx, s, `SELECT e.title, o.name, e.starts_at, e.ends_at, e.status, e.capacity,
		(SELECT COALESCE(SUM(tt.quantity_sold), 0) FROM ticket_types tt WHERE tt.event_id = e.id),
		(SELECT COALESCE(SUM(x.total_cents - x.refunded_cents), 0) FROM orders x WHERE x.event_id = e.id AND x.status IN ('paid', 'partially_refunded', 'refunded')),
		(SELECT COUNT(*) FROM tickets t WHERE t.event_id = e.id AND t.status = 'valid'),
		(SELECT COUNT(*) FROM tickets t WHERE t.event_id = e.id AND t.status = 'valid' AND t.first_checked_in_at IS NOT NULL)
		FROM events e JOIN organisers o ON o.id = e.organiser_id
		WHERE `+scopeSQL+` AND (? OR (e.starts_at >= ? AND e.starts_at < ?)) AND e.status IN ('published', 'cancelled')`+evSQL+`
		ORDER BY e.starts_at`, args, func(rows *sql.Rows) error {
		var title, organiser, status string
		var startsAt, endsAt time.Time
		var cap, nSold, rev, nIssued, nIn int64
		if err := rows.Scan(&title, &organiser, &startsAt, &endsAt, &status, &cap, &nSold, &rev, &nIssued, &nIn); err != nil {
			return err
		}
		switch {
		case status == "cancelled":
			status = "Cancelled"
		case now.Before(startsAt):
			status = "Upcoming"
		case now.Before(endsAt):
			status = "Live"
		default:
			status = "Completed"
		}
		t.Rows = append(t.Rows, []any{title, organiser, startsAt, status, cap, nSold, ratio(nSold, cap), rev, nIn, ratio(nIn, nIssued)})
		capacity, sold, revenue, issued, checkedIn = capacity+cap, sold+nSold, revenue+rev, issued+nIssued, checkedIn+nIn
		return nil
	})
	t.Totals = []any{"Total", nil, nil, nil, capacity, sold, ratio(sold, capacity), revenue, checkedIn, ratio(checkedIn, issued)}
	return t, err
}

func attendees(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, args := scope.SQL("e.organiser_id")
	args = append(args, f.EventID)
	var title string
	if err := s.db.QueryRowContext(ctx, `SELECT e.title FROM events e WHERE `+scopeSQL+` AND e.id = ?`, args...).Scan(&title); err != nil {
		return nil, apperr.NotFound("Event")
	}
	t := &Table{Description: "Attendees of " + title, Columns: []Column{
		{"name", "Name", KindText}, {"email", "Email", KindText}, {"phone", "Phone", KindText}, {"ticket_type", "Ticket type", KindText},
		{"code", "Ticket code", KindText}, {"order", "Order", KindText}, {"status", "Ticket status", KindText},
		{"checkin", "Check-in", KindText}, {"checked_in_at", "First check-in", KindDateTime},
	}}
	var total, in int64
	err := query(ctx, s, `SELECT CONCAT(a.first_name, ' ', a.last_name), a.email, a.phone, tt.name, t.code, x.reference, t.status,
		t.checkin_state, t.first_checked_in_at
		FROM tickets t JOIN attendees a ON a.id = t.attendee_id JOIN ticket_types tt ON tt.id = t.ticket_type_id JOIN orders x ON x.id = t.order_id
		WHERE t.event_id = ? ORDER BY a.last_name, a.first_name, t.id`, []any{f.EventID}, func(rows *sql.Rows) error {
		var name, email, phone, ticketType, code, order, status, state string
		var checkedIn *time.Time
		if err := rows.Scan(&name, &email, &phone, &ticketType, &code, &order, &status, &state, &checkedIn); err != nil {
			return err
		}
		var at any
		if checkedIn != nil {
			at = *checkedIn
			in++
		}
		total++
		t.Rows = append(t.Rows, []any{name, email, phone, ticketType, code[:5] + "-" + code[5:], order, humanize(status), humanize(state), at})
		return nil
	})
	t.Totals = []any{fmt.Sprintf("%d tickets, %d checked in", total, in)}
	return t, err
}

func revenue(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	offset := s.offsetMinutes(f.From)
	scopeSQL, scopeArgs := scope.SQL("x.organiser_id")
	evSQL, evArgs := eventClause(f, "x.event_id")

	type day struct {
		orders, tickets, gross, discounts, refunds, fees int64
	}
	days := map[string]*day{}
	get := func(k string) *day {
		if days[k] == nil {
			days[k] = &day{}
		}
		return days[k]
	}

	args := append(append([]any{offset}, scopeArgs...), f.From, f.To)
	args = append(args, evArgs...)
	err := query(ctx, s, `SELECT DATE_FORMAT(DATE_ADD(x.paid_at, INTERVAL ? MINUTE), '%Y-%m-%d'), COUNT(*),
		SUM((SELECT COALESCE(SUM(oi.quantity), 0) FROM order_items oi WHERE oi.order_id = x.id)),
		SUM(x.total_cents), SUM(x.discount_cents), SUM(ROUND(x.total_cents * x.commission_bps / 10000))
		FROM orders x WHERE `+scopeSQL+` AND x.source = 'online' AND x.paid_at >= ? AND x.paid_at < ?`+evSQL+`
		GROUP BY 1`, args, func(rows *sql.Rows) error {
		var k string
		var d day
		if err := rows.Scan(&k, &d.orders, &d.tickets, &d.gross, &d.discounts, &d.fees); err != nil {
			return err
		}
		*get(k) = d
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Refunds of tickets (not of duplicate payments, which were never revenue) reduce
	// revenue on the day the money was returned, together with the commission on them.
	err = query(ctx, s, `SELECT DATE_FORMAT(DATE_ADD(r.processed_at, INTERVAL ? MINUTE), '%Y-%m-%d'),
		SUM(r.amount_cents), SUM(ROUND(r.amount_cents * x.commission_bps / 10000))
		FROM refunds r JOIN orders x ON x.id = r.order_id
		WHERE `+scopeSQL+` AND r.status = 'completed' AND r.processed_at >= ? AND r.processed_at < ?`+evSQL+`
		AND EXISTS (SELECT 1 FROM refund_items ri WHERE ri.refund_id = r.id)
		GROUP BY 1`, args, func(rows *sql.Rows) error {
		var k string
		var amount, fees int64
		if err := rows.Scan(&k, &amount, &fees); err != nil {
			return err
		}
		d := get(k)
		d.refunds += amount
		d.fees -= fees
		return nil
	})

	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := &Table{Columns: []Column{
		{"date", "Date", KindDate}, {"orders", "Orders", KindInt}, {"tickets", "Tickets", KindInt}, {"gross", "Gross sales", KindMoney},
		{"discounts", "Discounts given", KindMoney}, {"refunds", "Refunds", KindMoney}, {"net", "Net revenue", KindMoney},
		{"fees", "Platform fees", KindMoney}, {"organiser", "Organiser earnings", KindMoney},
	}}
	var tot day
	for _, k := range keys {
		d := days[k]
		date, _ := time.Parse("2006-01-02", k)
		net := d.gross - d.refunds
		t.Rows = append(t.Rows, []any{date, d.orders, d.tickets, d.gross, d.discounts, d.refunds, net, d.fees, net - d.fees})
		tot.orders, tot.tickets, tot.gross, tot.discounts, tot.refunds, tot.fees = tot.orders+d.orders, tot.tickets+d.tickets,
			tot.gross+d.gross, tot.discounts+d.discounts, tot.refunds+d.refunds, tot.fees+d.fees
	}
	net := tot.gross - tot.refunds
	t.Totals = []any{"Total", tot.orders, tot.tickets, tot.gross, tot.discounts, tot.refunds, net, tot.fees, net - tot.fees}
	return t, err
}

func organiserPerformance(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, scopeArgs := scope.SQL("o.id")
	// The six correlated subqueries each take the date range; the scope filter comes last.
	var args []any
	for i := 0; i < 6; i++ {
		args = append(args, f.From, f.To)
	}
	args = append(args, scopeArgs...)
	t := &Table{Columns: []Column{
		{"organiser", "Organiser", KindText}, {"status", "Status", KindText}, {"events", "Events held", KindInt}, {"orders", "Orders", KindInt},
		{"gross", "Gross sales", KindMoney}, {"refunds", "Refunds", KindMoney}, {"net", "Net revenue", KindMoney},
		{"commission", "Commission", KindPercent}, {"fees", "Platform fees", KindMoney}, {"earnings", "Organiser earnings", KindMoney},
	}}
	var events, orders, gross, refunds, fees int64
	err := query(ctx, s, `SELECT o.name, o.status, o.commission_bps,
		(SELECT COUNT(*) FROM events e WHERE e.organiser_id = o.id AND e.status = 'published' AND e.starts_at >= ? AND e.starts_at < ?),
		(SELECT COUNT(*) FROM orders x WHERE x.organiser_id = o.id AND x.source = 'online' AND x.paid_at >= ? AND x.paid_at < ?),
		(SELECT COALESCE(SUM(x.total_cents), 0) FROM orders x WHERE x.organiser_id = o.id AND x.source = 'online' AND x.paid_at >= ? AND x.paid_at < ?),
		(SELECT COALESCE(SUM(ROUND(x.total_cents * x.commission_bps / 10000)), 0) FROM orders x WHERE x.organiser_id = o.id AND x.source = 'online' AND x.paid_at >= ? AND x.paid_at < ?),
		(SELECT COALESCE(SUM(r.amount_cents), 0) FROM refunds r JOIN orders x ON x.id = r.order_id WHERE x.organiser_id = o.id AND r.status = 'completed'
			AND r.processed_at >= ? AND r.processed_at < ? AND EXISTS (SELECT 1 FROM refund_items ri WHERE ri.refund_id = r.id)),
		(SELECT COALESCE(SUM(ROUND(r.amount_cents * x.commission_bps / 10000)), 0) FROM refunds r JOIN orders x ON x.id = r.order_id WHERE x.organiser_id = o.id
			AND r.status = 'completed' AND r.processed_at >= ? AND r.processed_at < ? AND EXISTS (SELECT 1 FROM refund_items ri WHERE ri.refund_id = r.id))
		FROM organisers o WHERE `+scopeSQL+` ORDER BY o.name`, args, func(rows *sql.Rows) error {
		var name, status string
		var bps, nEvents, nOrders, g, saleFees, r, refundFees int64
		if err := rows.Scan(&name, &status, &bps, &nEvents, &nOrders, &g, &saleFees, &r, &refundFees); err != nil {
			return err
		}
		net, fee := g-r, saleFees-refundFees
		t.Rows = append(t.Rows, []any{name, humanize(status), nEvents, nOrders, g, r, net, float64(bps) / 10000, fee, net - fee})
		events, orders, gross, refunds, fees = events+nEvents, orders+nOrders, gross+g, refunds+r, fees+fee
		return nil
	})
	t.Totals = []any{"Total", nil, events, orders, gross, refunds, gross - refunds, nil, fees, gross - refunds - fees}
	return t, err
}

func paymentsReport(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, args := scope.SQL("x.organiser_id")
	evSQL, evArgs := eventClause(f, "x.event_id")
	args = append(append(args, f.From, f.To), evArgs...)
	t := &Table{Columns: []Column{
		{"date", "Date", KindDateTime}, {"order", "Order", KindText}, {"customer", "Customer", KindText}, {"event", "Event", KindText},
		{"method", "Method", KindText}, {"provider", "Provider", KindText}, {"status", "Status", KindText},
		{"amount", "Amount", KindMoney}, {"refunded", "Refunded", KindMoney}, {"reference", "Payment reference", KindText},
	}}
	var received, refunded int64
	err := query(ctx, s, `SELECT pa.created_at, x.reference, x.customer_email, e.title, pa.method, pa.provider, pa.status, pa.amount_cents,
		pa.refunded_cents, pa.provider_reference
		FROM payments pa JOIN orders x ON x.id = pa.order_id JOIN events e ON e.id = x.event_id
		WHERE `+scopeSQL+` AND pa.method IN ('online', 'eft') AND pa.created_at >= ? AND pa.created_at < ?`+evSQL+`
		ORDER BY pa.created_at`, args, func(rows *sql.Rows) error {
		var at time.Time
		var order, customer, event, method, provider, status, ref string
		var amount, ref1 int64
		if err := rows.Scan(&at, &order, &customer, &event, &method, &provider, &status, &amount, &ref1, &ref); err != nil {
			return err
		}
		if status == "succeeded" {
			received += amount
			refunded += ref1
		}
		t.Rows = append(t.Rows, []any{at, order, customer, event, strings.ToUpper(method), provider, humanize(status), amount, ref1, ref})
		return nil
	})
	t.Totals = []any{"Succeeded", nil, nil, nil, nil, nil, nil, received, refunded, nil}
	return t, err
}

func refundsReport(ctx context.Context, s *Service, scope auth.Scope, f Filter) (*Table, error) {
	scopeSQL, args := scope.SQL("x.organiser_id")
	evSQL, evArgs := eventClause(f, "x.event_id")
	args = append(append(args, f.From, f.To), evArgs...)
	t := &Table{Columns: []Column{
		{"date", "Requested", KindDateTime}, {"reference", "Refund", KindText}, {"order", "Order", KindText}, {"customer", "Customer", KindText},
		{"event", "Event", KindText}, {"initiated_by", "Initiated by", KindText}, {"status", "Status", KindText},
		{"amount", "Amount", KindMoney}, {"processed_at", "Processed", KindDateTime}, {"reason", "Reason", KindText},
	}}
	var completed int64
	err := query(ctx, s, `SELECT r.created_at, r.reference, x.reference, x.customer_email, e.title, r.initiated_by, r.status, r.amount_cents,
		r.processed_at, r.reason
		FROM refunds r JOIN orders x ON x.id = r.order_id JOIN events e ON e.id = x.event_id
		WHERE `+scopeSQL+` AND r.created_at >= ? AND r.created_at < ?`+evSQL+` ORDER BY r.created_at`, args, func(rows *sql.Rows) error {
		var at time.Time
		var processed *time.Time
		var ref, order, customer, event, by, status, reason string
		var amount int64
		if err := rows.Scan(&at, &ref, &order, &customer, &event, &by, &status, &amount, &processed, &reason); err != nil {
			return err
		}
		var p any
		if processed != nil {
			p = *processed
		}
		if status == "completed" {
			completed += amount
		}
		t.Rows = append(t.Rows, []any{at, ref, order, customer, event, humanize(by), humanize(status), amount, p, reason})
		return nil
	})
	t.Totals = []any{"Completed", nil, nil, nil, nil, nil, nil, completed, nil, nil}
	return t, err
}

func humanize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---- HTTP -----------------------------------------------------------------------------

// Handler serves reports.
type Handler struct{ svc *Service }

// NewHandler returns the reports handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List returns the reports the caller may run.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var out []Definition
	for _, d := range Definitions {
		if p.CanAny(d.Permission) {
			out = append(out, d)
		}
	}
	return httpx.List(w, out)
}

// Run returns a report as JSON, or as a file when ?format=csv|xlsx|pdf.
func (h *Handler) Run(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	now := h.svc.now().In(h.svc.loc)
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, h.svc.loc).AddDate(0, -2, 0)
	from, err := httpx.QueryDate(r, "from", h.svc.loc, false)
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to", h.svc.loc, true)
	if err != nil {
		return err
	}
	startOfTomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.svc.loc).AddDate(0, 0, 1)
	f := Filter{From: defaultFrom.UTC(), To: startOfTomorrow.UTC(), EventID: httpx.QueryInt64(r, "event_id"), OrganiserID: httpx.QueryInt64(r, "organiser_id")}
	if from != nil {
		f.From = *from
	}
	if to != nil {
		f.To = *to
	}
	if !f.To.After(f.From) {
		return apperr.FieldError("to", "must be on or after the start date")
	}
	if f.To.Sub(f.From) > 3*366*24*time.Hour {
		return apperr.FieldError("from", "reports can cover at most three years")
	}

	t, err := h.svc.Build(r.Context(), p, r.PathValue("name"), f)
	if err != nil {
		return err
	}
	format := httpx.QueryString(r, "format")
	if format == "" || format == "json" {
		return httpx.OK(w, t)
	}
	if !p.CanAny(rbac.ReportsExport) {
		return apperr.Forbidden("You do not have permission to export reports.")
	}
	var data []byte
	var contentType, ext string
	switch format {
	case "csv":
		data, err = t.ToCSV()
		contentType, ext = "text/csv; charset=utf-8", "csv"
	case "xlsx":
		data, err = t.ToXLSX()
		contentType, ext = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"
	case "pdf":
		data, err = t.ToPDF(h.svc.appName)
		contentType, ext = "application/pdf", "pdf"
	default:
		return apperr.FieldError("format", "must be json, csv, xlsx or pdf")
	}
	if err != nil {
		return err
	}
	filename := fmt.Sprintf("%s-%s.%s", t.Name, now.Format("20060102-1504"), ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write(data)
	return err
}
