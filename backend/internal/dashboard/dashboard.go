// Package dashboard aggregates the headline numbers shown on the admin dashboard.
package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Summary is everything the dashboard shows. Figures are limited to the organisers the
// user can see, so an organiser only ever sees their own business.
type Summary struct {
	Currency           string        `json:"currency"`
	NetRevenueCents    int64         `json:"net_revenue_cents"`
	RevenueLast30Cents int64         `json:"revenue_last_30_days_cents"`
	TicketsSold        int64         `json:"tickets_sold"`
	OrdersPaid         int64         `json:"orders_paid"`
	UpcomingEvents     int64         `json:"upcoming_events"`
	LiveEvents         int64         `json:"live_events"`
	CompletedEvents    int64         `json:"completed_events"`
	PendingReview      int64         `json:"pending_review"`
	DraftEvents        int64         `json:"draft_events"`
	PendingEFT         int64         `json:"pending_eft_payments"`
	PendingRefunds     int64         `json:"pending_refunds"`
	Inventory          Inventory     `json:"inventory"`
	Attendance         Attendance    `json:"attendance"`
	SalesTrend         []TrendPoint  `json:"sales_trend"`
	RecentTransactions []Transaction `json:"recent_transactions"`
	UpcomingList       []EventLine   `json:"upcoming_list"`
	// Event is set when the dashboard is narrowed to one event.
	Event *EventFocus `json:"event,omitempty"`
}

// EventFocus describes the event a filtered dashboard covers.
type EventFocus struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Timezone string    `json:"timezone"`
	Capacity int64     `json:"capacity"`
}

// Inventory compares sold tickets with capacity for upcoming and live events.
type Inventory struct {
	Capacity  int64 `json:"capacity"`
	Sold      int64 `json:"sold"`
	Reserved  int64 `json:"reserved"`
	Available int64 `json:"available"`
}

// Attendance summarises check-ins for events that started in the last 30 days.
type Attendance struct {
	TicketsIssued int64 `json:"tickets_issued"`
	CheckedIn     int64 `json:"checked_in"`
}

// TrendPoint is one day of sales.
type TrendPoint struct {
	Date         string `json:"date"`
	RevenueCents int64  `json:"revenue_cents"`
	Tickets      int64  `json:"tickets"`
}

// Transaction is a recent payment.
type Transaction struct {
	OrderID        int64     `json:"order_id"`
	OrderReference string    `json:"order_reference"`
	CustomerName   string    `json:"customer_name"`
	EventTitle     string    `json:"event_title"`
	Method         string    `json:"method"`
	AmountCents    int64     `json:"amount_cents"`
	PaidAt         time.Time `json:"paid_at"`
}

// EventLine is an upcoming event with its sales progress.
type EventLine struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
	Timezone string    `json:"timezone"`
	Capacity int64     `json:"capacity"`
	Sold     int64     `json:"sold"`
}

// Service computes dashboard figures.
type Service struct {
	db       *database.DB
	loc      *time.Location
	currency string
	now      clock.Func
}

// NewService returns the dashboard service.
func NewService(db *database.DB, timezone, currency string, now clock.Func) *Service {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return &Service{db: db, loc: loc, currency: currency, now: now}
}

// Summary builds the dashboard for p, optionally narrowed to one organiser or one event.
func (s *Service) Summary(ctx context.Context, p *auth.Principal, organiserID, eventID int64) (Summary, error) {
	out := Summary{Currency: s.currency, SalesTrend: []TrendPoint{}, RecentTransactions: []Transaction{}, UpcomingList: []EventLine{}}
	scope := p.Scope(rbac.DashboardView).Narrow(organiserID)
	orgSQL, orgArgs := scope.SQL("x.organiser_id")
	evSQL, evArgs := scope.SQL("e.organiser_id")
	now := s.now()
	since := now.AddDate(0, 0, -30)

	// Conditions that limit inventory and attendance to current events; a single chosen
	// event is shown in full whatever its dates.
	inventoryCond, inventoryArgs := ` AND e.status = 'published' AND e.ends_at > ?`, []any{now}
	attendanceCond, attendanceArgs := ` AND e.starts_at BETWEEN ? AND ?`, []any{since, now}

	if eventID > 0 {
		var ev EventFocus
		err := s.db.QueryRowContext(ctx, `SELECT e.id, e.title, e.status, e.starts_at, e.ends_at, e.timezone, e.capacity
			FROM events e WHERE `+evSQL+` AND e.id = ?`, append(evArgs, eventID)...).
			Scan(&ev.ID, &ev.Title, &ev.Status, &ev.StartsAt, &ev.EndsAt, &ev.Timezone, &ev.Capacity)
		if errors.Is(err, sql.ErrNoRows) {
			// Events the user may not see are reported as missing, not forbidden.
			return out, apperr.NotFound("Event")
		}
		if err != nil {
			return out, err
		}
		out.Event = &ev
		// The extra condition sits right after the scope condition in every query, so its
		// argument is appended right after the scope arguments.
		orgSQL, orgArgs = orgSQL+" AND x.event_id = ?", append(orgArgs, eventID)
		evSQL, evArgs = evSQL+" AND e.id = ?", append(evArgs, eventID)
		inventoryCond, inventoryArgs = "", nil
		attendanceCond, attendanceArgs = "", nil
	}

	row := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(x.total_cents - x.refunded_cents), 0),
		COALESCE(SUM(CASE WHEN x.paid_at >= ? THEN x.total_cents - x.refunded_cents ELSE 0 END), 0),
		COUNT(*)
		FROM orders x WHERE `+orgSQL+` AND x.source = 'online' AND x.status IN ('paid', 'partially_refunded', 'refunded')`,
		append([]any{since}, orgArgs...)...)
	if err := row.Scan(&out.NetRevenueCents, &out.RevenueLast30Cents, &out.OrdersPaid); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets t JOIN orders x ON x.id = t.order_id
		WHERE `+orgSQL+` AND t.status = 'valid' AND t.is_complimentary = FALSE`, orgArgs...).Scan(&out.TicketsSold); err != nil {
		return out, err
	}

	if err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(e.status = 'published' AND e.starts_at > ?), 0),
		COALESCE(SUM(e.status = 'published' AND e.starts_at <= ? AND e.ends_at > ?), 0),
		COALESCE(SUM(e.status = 'published' AND e.ends_at <= ?), 0),
		COALESCE(SUM(e.status = 'pending_review'), 0),
		COALESCE(SUM(e.status = 'draft'), 0)
		FROM events e WHERE `+evSQL, append([]any{now, now, now, now}, evArgs...)...).
		Scan(&out.UpcomingEvents, &out.LiveEvents, &out.CompletedEvents, &out.PendingReview, &out.DraftEvents); err != nil {
		return out, err
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(e.capacity), 0),
		COALESCE(SUM((SELECT COALESCE(SUM(tt.quantity_sold), 0) FROM ticket_types tt WHERE tt.event_id = e.id)), 0),
		COALESCE(SUM((SELECT COALESCE(SUM(tt.quantity_reserved), 0) FROM ticket_types tt WHERE tt.event_id = e.id)), 0)
		FROM events e WHERE `+evSQL+inventoryCond, append(evArgs, inventoryArgs...)...).
		Scan(&out.Inventory.Capacity, &out.Inventory.Sold, &out.Inventory.Reserved); err != nil {
		return out, err
	}
	out.Inventory.Available = max(0, out.Inventory.Capacity-out.Inventory.Sold-out.Inventory.Reserved)

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(t.first_checked_in_at IS NOT NULL), 0)
		FROM tickets t JOIN events e ON e.id = t.event_id
		WHERE `+evSQL+` AND t.status = 'valid'`+attendanceCond, append(evArgs, attendanceArgs...)...).
		Scan(&out.Attendance.TicketsIssued, &out.Attendance.CheckedIn); err != nil {
		return out, err
	}

	if p.CanAny(rbac.PaymentsConfirm) {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payments pa JOIN orders x ON x.id = pa.order_id
			WHERE `+orgSQL+` AND pa.method = 'eft' AND pa.status = 'pending'`, orgArgs...).Scan(&out.PendingEFT)
	}
	if p.CanAny(rbac.RefundsView) {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM refunds r JOIN orders x ON x.id = r.order_id
			WHERE `+orgSQL+` AND r.status IN ('requested', 'approved', 'failed')`, orgArgs...).Scan(&out.PendingRefunds)
	}

	if err := s.trend(ctx, &out, orgSQL, orgArgs, now); err != nil {
		return out, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT x.id, x.reference, x.customer_name, e.title, pa.method, pa.amount_cents, pa.paid_at
		FROM payments pa JOIN orders x ON x.id = pa.order_id JOIN events e ON e.id = x.event_id
		WHERE `+orgSQL+` AND pa.status = 'succeeded' AND pa.method IN ('online', 'eft')
		ORDER BY pa.paid_at DESC LIMIT 8`, orgArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.OrderID, &t.OrderReference, &t.CustomerName, &t.EventTitle, &t.Method, &t.AmountCents, &t.PaidAt); err != nil {
			rows.Close()
			return out, err
		}
		out.RecentTransactions = append(out.RecentTransactions, t)
	}
	rows.Close()

	if out.Event != nil {
		return out, nil // the upcoming-events list only makes sense across events
	}
	rows, err = s.db.QueryContext(ctx, `SELECT e.id, e.title, e.starts_at, e.timezone, e.capacity,
		(SELECT COALESCE(SUM(tt.quantity_sold), 0) FROM ticket_types tt WHERE tt.event_id = e.id)
		FROM events e WHERE `+evSQL+` AND e.status = 'published' AND e.ends_at > ? ORDER BY e.starts_at LIMIT 6`, append(evArgs, now)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l EventLine
		if err := rows.Scan(&l.ID, &l.Title, &l.StartsAt, &l.Timezone, &l.Capacity, &l.Sold); err != nil {
			return out, err
		}
		out.UpcomingList = append(out.UpcomingList, l)
	}
	return out, rows.Err()
}

// trend fills 30 days of sales, including days without sales so charts have no gaps.
func (s *Service) trend(ctx context.Context, out *Summary, orgSQL string, orgArgs []any, now time.Time) error {
	localNow := now.In(s.loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, s.loc).AddDate(0, 0, -29)
	_, offset := localNow.Zone()
	byDay := map[string]TrendPoint{}
	rows, err := s.db.QueryContext(ctx, `SELECT DATE_FORMAT(DATE_ADD(x.paid_at, INTERVAL ? MINUTE), '%Y-%m-%d'),
		SUM(x.total_cents), SUM((SELECT COALESCE(SUM(oi.quantity), 0) FROM order_items oi WHERE oi.order_id = x.id))
		FROM orders x WHERE `+orgSQL+` AND x.source = 'online' AND x.paid_at >= ? AND x.status IN ('paid', 'partially_refunded', 'refunded')
		GROUP BY 1`, append(append([]any{offset / 60}, orgArgs...), start.UTC())...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.Date, &p.RevenueCents, &p.Tickets); err != nil {
			return err
		}
		byDay[p.Date] = p
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for d := 0; d < 30; d++ {
		key := start.AddDate(0, 0, d).Format("2006-01-02")
		point, ok := byDay[key]
		if !ok {
			point = TrendPoint{Date: key}
		}
		out.SalesTrend = append(out.SalesTrend, point)
	}
	return nil
}

// Handler serves the dashboard.
type Handler struct{ svc *Service }

// NewHandler returns the dashboard handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	sum, err := h.svc.Summary(r.Context(), p, httpx.QueryInt64(r, "organiser_id"), httpx.QueryInt64(r, "event_id"))
	if err != nil {
		return err
	}
	return httpx.OK(w, sum)
}
