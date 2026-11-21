package sales

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
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// ---- Customer -------------------------------------------------------------------------

// CustomerOrder returns one of the customer's own orders, with QR codes for valid tickets.
func (s *Service) CustomerOrder(ctx context.Context, p *auth.Principal, id int64) (OrderView, error) {
	o, err := getOrder(ctx, s.db, id)
	if err != nil {
		return OrderView{}, err
	}
	if !ownsOrder(o.CustomerID, p.UserID) {
		return OrderView{}, apperr.NotFound("Order")
	}
	return s.orderView(ctx, s.db, id, true)
}

const orderSummarySelect = `SELECT x.id, x.reference, x.status, x.source, x.payment_method, x.event_id, e.title, e.starts_at, e.timezone,
	x.customer_name, x.customer_email, x.currency, x.total_cents, x.refunded_cents,
	(SELECT COALESCE(SUM(oi.quantity), 0) FROM order_items oi WHERE oi.order_id = x.id),
	x.expires_at, x.paid_at, x.created_at
	FROM orders x JOIN events e ON e.id = x.event_id`

func scanOrderSummaries(rows *sql.Rows) ([]OrderSummary, error) {
	defer rows.Close()
	out := []OrderSummary{}
	for rows.Next() {
		var o OrderSummary
		if err := rows.Scan(&o.ID, &o.Reference, &o.Status, &o.Source, &o.PaymentMethod, &o.EventID, &o.EventTitle, &o.EventStartsAt,
			&o.EventTimezone, &o.CustomerName, &o.CustomerEmail, &o.Currency, &o.TotalCents, &o.RefundedCents, &o.TicketCount,
			&o.ExpiresAt, &o.PaidAt, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// MyOrders lists the customer's orders, newest first.
func (s *Service) MyOrders(ctx context.Context, p *auth.Principal, page httpx.Page) ([]OrderSummary, int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE customer_id = ?`, p.UserID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, orderSummarySelect+` WHERE x.customer_id = ? ORDER BY x.created_at DESC, x.id DESC LIMIT ? OFFSET ?`,
		p.UserID, page.Size, page.Offset())
	if err != nil {
		return nil, 0, err
	}
	list, err := scanOrderSummaries(rows)
	return list, total, err
}

// MyTickets lists the customer's tickets for upcoming (default) or past events.
func (s *Service) MyTickets(ctx context.Context, p *auth.Principal, past bool) ([]TicketView, error) {
	when := "e.ends_at > ?"
	order := "e.starts_at, t.id"
	if past {
		when, order = "e.ends_at <= ?", "e.starts_at DESC, t.id"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, `+eventRefColumns+`
		FROM tickets t JOIN orders x ON x.id = t.order_id`+eventRefJoins+`
		WHERE x.customer_id = ? AND x.status IN ('paid', 'partially_refunded', 'refunded') AND `+when+` ORDER BY `+order, p.UserID, s.now())
	if err != nil {
		return nil, err
	}
	events := map[int64]EventRef{}
	var ids []any
	for rows.Next() {
		var id int64
		var ev EventRef
		if err := rows.Scan(append([]any{&id}, eventRefDest(&ev)...)...); err != nil {
			rows.Close()
			return nil, err
		}
		events[id] = ev
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return []TicketView{}, err
	}
	list, err := s.ticketViews(ctx, s.db, "t.id IN ("+database.Placeholders(len(ids))+")", ids, true)
	if err != nil {
		return nil, err
	}
	byID := map[int64]TicketView{}
	for _, t := range list {
		ev := events[t.ID]
		t.Event = &ev
		byID[t.ID] = t
	}
	out := make([]TicketView, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id.(int64)])
	}
	return out, nil
}

// canViewOrder reports whether p may read an order: its customer, or staff with orders.view.
func canViewOrder(p *auth.Principal, o orderRow) bool {
	return ownsOrder(o.CustomerID, p.UserID) || p.CanFor(o.OrganiserID, rbac.OrdersView)
}

// OrderPDF renders the valid tickets of an order (optionally a single ticket) as a PDF.
func (s *Service) OrderPDF(ctx context.Context, p *auth.Principal, orderID, ticketID int64) ([]byte, string, error) {
	o, err := getOrder(ctx, s.db, orderID)
	if err != nil {
		return nil, "", err
	}
	if !canViewOrder(p, o) {
		return nil, "", apperr.NotFound("Order")
	}
	pdf, err := s.RenderTicketsPDF(ctx, orderID, ticketID)
	if err != nil {
		return nil, "", err
	}
	name := fmt.Sprintf("tickets-%s.pdf", o.Reference)
	if ticketID > 0 {
		name = fmt.Sprintf("ticket-%s-%d.pdf", o.Reference, ticketID)
	}
	return pdf, name, nil
}

// RenderTicketsPDF renders valid tickets of an order. ticketID 0 means every valid ticket.
// It performs no authorisation and is used by the email worker.
func (s *Service) RenderTicketsPDF(ctx context.Context, orderID, ticketID int64) ([]byte, error) {
	view, err := s.orderView(ctx, s.db, orderID, true)
	if err != nil {
		return nil, err
	}
	var printable []tickets.PrintableTicket
	for _, t := range view.Tickets {
		if t.Status != "valid" || (ticketID > 0 && t.ID != ticketID) {
			continue
		}
		price := money.Format(t.PriceCents, t.Currency)
		if t.IsComplimentary {
			price = "Complimentary"
		}
		printable = append(printable, tickets.PrintableTicket{
			EventTitle: view.Event.Title, DateText: EventDateText(view.Event.StartsAt, view.Event.EndsAt, view.Event.Timezone),
			VenueName: view.Event.VenueName, VenueAddress: view.Event.VenueAddress, TicketTypeName: t.TicketTypeName,
			HolderName: t.HolderFirstName + " " + t.HolderLastName, Code: t.Code, OrderReference: view.Reference, PriceText: price,
			IsComplimentary: t.IsComplimentary, QRPayload: t.QRPayload,
		})
	}
	if len(printable) == 0 {
		return nil, apperr.Conflict("no_valid_tickets", "There are no valid tickets on this order to download.")
	}
	return tickets.RenderPDF(s.cfg.AppName, printable)
}

// EventDateText formats an event's dates in its own time zone, e.g.
// "Sat 5 Dec 2026, 18:00 – 23:30 (SAST)".
func EventDateText(start, end time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	s, e := start.In(loc), end.In(loc)
	zone, _ := s.Zone()
	if s.Year() == e.Year() && s.YearDay() == e.YearDay() {
		return fmt.Sprintf("%s – %s (%s)", s.Format("Mon 2 Jan 2006, 15:04"), e.Format("15:04"), zone)
	}
	return fmt.Sprintf("%s – %s (%s)", s.Format("Mon 2 Jan 2006, 15:04"), e.Format("Mon 2 Jan 2006, 15:04"), zone)
}

// UpdateAttendee changes who a ticket is for (a transfer). The ticket's QR version is bumped
// so the previously issued QR code stops working, and the new holder is emailed the ticket.
func (s *Service) UpdateAttendee(ctx context.Context, p *auth.Principal, ticketID int64, in AttendeeInput) (TicketView, error) {
	in.normalize()
	v := validate.New()
	in.validate(v, "attendee")
	if err := v.Err(); err != nil {
		return TicketView{}, err
	}
	var orderID int64
	if err := s.db.QueryRowContext(ctx, `SELECT order_id FROM tickets WHERE id = ?`, ticketID).Scan(&orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TicketView{}, apperr.NotFound("Ticket")
		}
		return TicketView{}, err
	}
	isOwner := false
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		isOwner = ownsOrder(o.CustomerID, p.UserID)
		if !isOwner && !p.CanFor(o.OrganiserID, rbac.OrdersManage) {
			return apperr.NotFound("Ticket")
		}
		var attendeeID int64
		var status, state string
		var startsAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT t.attendee_id, t.status, t.checkin_state, e.starts_at FROM tickets t JOIN events e ON e.id = t.event_id
			WHERE t.id = ? FOR UPDATE`, ticketID).Scan(&attendeeID, &status, &state, &startsAt); err != nil {
			return err
		}
		now := s.now()
		switch {
		case status != "valid":
			return apperr.Conflict("ticket_not_valid", "Only valid tickets can be transferred.")
		case state != "not_checked_in":
			return apperr.Conflict("ticket_used", "This ticket has already been used.")
		case isOwner && !now.Before(startsAt):
			return apperr.Conflict("event_started", "Tickets cannot be transferred after the event has started.")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attendees SET first_name = ?, last_name = ?, email = ?, phone = ?, updated_at = ? WHERE id = ?`,
			in.FirstName, in.LastName, in.Email, in.Phone, now, attendeeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET qr_version = qr_version + 1, updated_at = ? WHERE id = ?`, now, ticketID); err != nil {
			return err
		}
		if err := jobs.Enqueue(ctx, tx, now, JobSendTicket, map[string]int64{"ticket_id": ticketID}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "ticket.attendee_changed", EntityType: "ticket", EntityID: ticketID, OrganiserID: o.OrganiserID,
			Summary: fmt.Sprintf("Ticket on order %s reassigned to %s %s <%s>; previous QR code revoked", o.Reference, in.FirstName, in.LastName, in.Email)})
	})
	if err != nil {
		return TicketView{}, err
	}
	list, err := s.ticketViews(ctx, s.db, "t.id = ?", []any{ticketID}, isOwner)
	if err != nil || len(list) == 0 {
		return TicketView{}, err
	}
	return list[0], nil
}

// ---- Administration -------------------------------------------------------------------

// OrderFilter narrows the staff order list.
type OrderFilter struct {
	EventID     int64
	OrganiserID int64
	Status      string
	Source      string
	Query       string
}

// ListOrders lists orders in the caller's scope.
func (s *Service) ListOrders(ctx context.Context, p *auth.Principal, f OrderFilter, page httpx.Page) ([]OrderSummary, int64, error) {
	scopeSQL, args := p.Scope(rbac.OrdersView).Narrow(f.OrganiserID).SQL("x.organiser_id")
	where := []string{scopeSQL}
	if f.EventID > 0 {
		where = append(where, "x.event_id = ?")
		args = append(args, f.EventID)
	}
	if f.Status != "" {
		where = append(where, "x.status = ?")
		args = append(args, f.Status)
	}
	if f.Source != "" {
		where = append(where, "x.source = ?")
		args = append(args, f.Source)
	}
	if f.Query != "" {
		where = append(where, "(x.reference LIKE ? OR x.customer_email LIKE ? OR x.customer_name LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like, like)
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders x`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, orderSummarySelect+clause+` ORDER BY x.created_at DESC, x.id DESC LIMIT ? OFFSET ?`, append(args, page.Size, page.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	list, err := scanOrderSummaries(rows)
	return list, total, err
}

// AdminOrder returns an order for staff. QR payloads are never included for staff.
func (s *Service) AdminOrder(ctx context.Context, p *auth.Principal, id int64) (OrderView, error) {
	o, err := getOrder(ctx, s.db, id)
	if err != nil {
		return OrderView{}, err
	}
	if !p.CanFor(o.OrganiserID, rbac.OrdersView) {
		return OrderView{}, apperr.NotFound("Order")
	}
	return s.orderView(ctx, s.db, id, false)
}

// ResendTickets queues the ticket email again.
func (s *Service) ResendTickets(ctx context.Context, p *auth.Principal, id int64) error {
	o, err := getOrder(ctx, s.db, id)
	if err != nil {
		return err
	}
	if !p.CanFor(o.OrganiserID, rbac.OrdersManage) {
		return apperr.NotFound("Order")
	}
	if o.Status != OrderPaid && o.Status != OrderPartiallyRefunded {
		return apperr.Conflict("order_not_paid", "Tickets can only be resent for paid orders.")
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		if err := jobs.Enqueue(ctx, tx, now, JobSendOrderTickets, map[string]int64{"order_id": id}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "order.tickets_resent", EntityType: "order", EntityID: id, OrganiserID: o.OrganiserID,
			Summary: "Tickets resent to " + o.CustomerEmail})
	})
}

// AdminCancelOrder cancels an unpaid order on the customer's behalf.
func (s *Service) AdminCancelOrder(ctx context.Context, p *auth.Principal, id int64) (OrderView, error) {
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		if !p.CanFor(o.OrganiserID, rbac.OrdersManage) {
			return apperr.NotFound("Order")
		}
		if o.Status != OrderPending {
			return apperr.Conflict("order_not_pending", "Only unpaid orders can be cancelled; refund paid orders instead.")
		}
		if err := s.release(ctx, tx, o, OrderCancelled); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "order.cancelled", EntityType: "order", EntityID: id, OrganiserID: o.OrganiserID,
			Summary: "Unpaid order " + o.Reference + " cancelled by staff"})
	})
	if err != nil {
		return OrderView{}, err
	}
	return s.orderView(ctx, s.db, id, false)
}

// PaymentFilter narrows the payment list.
type PaymentFilter struct {
	Status      string
	Method      string
	Query       string
	OrganiserID int64
}

// ListPayments lists payments in the caller's scope.
func (s *Service) ListPayments(ctx context.Context, p *auth.Principal, f PaymentFilter, page httpx.Page) ([]PaymentView, int64, error) {
	scopeSQL, args := p.Scope(rbac.PaymentsView).Narrow(f.OrganiserID).SQL("x.organiser_id")
	where := []string{scopeSQL, "pa.method IN ('online', 'eft')"}
	if f.Status != "" {
		where = append(where, "pa.status = ?")
		args = append(args, f.Status)
	}
	if f.Method != "" {
		where = append(where, "pa.method = ?")
		args = append(args, f.Method)
	}
	if f.Query != "" {
		where = append(where, "(x.reference LIKE ? OR pa.provider_reference LIKE ? OR x.customer_email LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like, like)
	}
	clause := strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payments pa JOIN orders x ON x.id = pa.order_id WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	list, err := s.paymentViews(ctx, s.db, clause, args, "pa.created_at DESC, pa.id DESC", page.Size, page.Offset())
	return list, total, err
}

// RefundFilter narrows the refund list.
type RefundFilter struct {
	Status      string
	Query       string
	EventID     int64
	OrganiserID int64
}

// ListRefunds lists refunds in the caller's scope.
func (s *Service) ListRefunds(ctx context.Context, p *auth.Principal, f RefundFilter, page httpx.Page) ([]RefundView, int64, error) {
	scopeSQL, args := p.Scope(rbac.RefundsView).Narrow(f.OrganiserID).SQL("x.organiser_id")
	where := []string{scopeSQL}
	if f.Status != "" {
		where = append(where, "r.status = ?")
		args = append(args, f.Status)
	}
	if f.EventID > 0 {
		where = append(where, "x.event_id = ?")
		args = append(args, f.EventID)
	}
	if f.Query != "" {
		where = append(where, "(r.reference LIKE ? OR x.reference LIKE ? OR x.customer_email LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like, like)
	}
	clause := strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM refunds r JOIN orders x ON x.id = r.order_id WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	list, err := s.refundViews(ctx, s.db, clause, args, "FIELD(r.status, 'requested', 'approved', 'failed', 'processing') DESC, r.created_at DESC", page.Size, page.Offset())
	return list, total, err
}

// AttendeeRow is one ticket holder on an event's attendee list.
type AttendeeRow struct {
	TicketID         int64      `json:"ticket_id"`
	Code             string     `json:"code"`
	DisplayCode      string     `json:"display_code"`
	TicketStatus     string     `json:"ticket_status"`
	CheckinState     string     `json:"checkin_state"`
	FirstCheckedInAt *time.Time `json:"first_checked_in_at"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	Email            string     `json:"email"`
	Phone            string     `json:"phone"`
	TicketTypeName   string     `json:"ticket_type_name"`
	OrderID          int64      `json:"order_id"`
	OrderReference   string     `json:"order_reference"`
	IsComplimentary  bool       `json:"is_complimentary"`
}

// AttendeeFilter narrows the attendee list.
type AttendeeFilter struct {
	Query        string
	CheckinState string
	TicketTypeID int64
}

// EventAttendees lists the holders of issued tickets for an event.
func (s *Service) EventAttendees(ctx context.Context, p *auth.Principal, eventID int64, f AttendeeFilter, page httpx.Page) ([]AttendeeRow, int64, error) {
	var organiserID int64
	if err := s.db.QueryRowContext(ctx, `SELECT organiser_id FROM events WHERE id = ?`, eventID).Scan(&organiserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, apperr.NotFound("Event")
		}
		return nil, 0, err
	}
	if !p.CanFor(organiserID, rbac.AttendeesView) {
		return nil, 0, apperr.NotFound("Event")
	}
	where := []string{"t.event_id = ?"}
	args := []any{eventID}
	if f.Query != "" {
		where = append(where, "(a.first_name LIKE ? OR a.last_name LIKE ? OR a.email LIKE ? OR t.code LIKE ? OR x.reference LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like, like, database.EscapeLike(tickets.NormalizeCode(f.Query)), like)
	}
	if f.CheckinState != "" {
		where = append(where, "t.checkin_state = ?")
		args = append(args, f.CheckinState)
	}
	if f.TicketTypeID > 0 {
		where = append(where, "t.ticket_type_id = ?")
		args = append(args, f.TicketTypeID)
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	from := ` FROM tickets t JOIN attendees a ON a.id = t.attendee_id JOIN ticket_types tt ON tt.id = t.ticket_type_id JOIN orders x ON x.id = t.order_id`
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.code, t.status, t.checkin_state, t.first_checked_in_at, a.first_name, a.last_name,
		a.email, a.phone, tt.name, x.id, x.reference, t.is_complimentary`+from+clause+` ORDER BY a.last_name, a.first_name, t.id LIMIT ? OFFSET ?`,
		append(args, page.Size, page.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AttendeeRow{}
	for rows.Next() {
		var a AttendeeRow
		if err := rows.Scan(&a.TicketID, &a.Code, &a.TicketStatus, &a.CheckinState, &a.FirstCheckedInAt, &a.FirstName, &a.LastName,
			&a.Email, &a.Phone, &a.TicketTypeName, &a.OrderID, &a.OrderReference, &a.IsComplimentary); err != nil {
			return nil, 0, err
		}
		a.DisplayCode = tickets.FormatCode(a.Code)
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// PaymentOptions describes how customers can pay, for the checkout page.
type PaymentOptions struct {
	OnlineProvider string `json:"online_provider"`
	EFTEnabled     bool   `json:"eft_enabled"`
	ReservationTTL int    `json:"reservation_minutes"`
}

// Options returns the configured payment options.
func (s *Service) Options() PaymentOptions {
	return PaymentOptions{OnlineProvider: s.gateway.Name(), EFTEnabled: s.cfg.EFTEnabled, ReservationTTL: int(s.cfg.ReservationTTL.Minutes())}
}
