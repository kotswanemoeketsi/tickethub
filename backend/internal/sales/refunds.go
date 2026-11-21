package sales

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/payments"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// Refund lifecycle: requested → approved → processing → completed, with rejected and failed
// as side exits (failed refunds can be processed again). Tickets are voided when a refund is
// approved, so they can no longer be used at the door, and returned to inventory when the
// money has actually been refunded.

type refundRow struct {
	ID          int64
	Reference   string
	OrderID     int64
	PaymentID   *int64
	AmountCents int64
	Currency    string
	Status      string
	InitiatedBy string
}

func lockRefund(ctx context.Context, tx *sql.Tx, id int64) (refundRow, error) {
	var r refundRow
	err := tx.QueryRowContext(ctx, `SELECT id, reference, order_id, payment_id, amount_cents, currency, status, initiated_by
		FROM refunds WHERE id = ? FOR UPDATE`, id).Scan(&r.ID, &r.Reference, &r.OrderID, &r.PaymentID, &r.AmountCents, &r.Currency, &r.Status, &r.InitiatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return r, apperr.NotFound("Refund")
	}
	return r, err
}

func refundOrderID(ctx context.Context, q database.Queryer, refundID int64) (int64, error) {
	var orderID int64
	err := q.QueryRowContext(ctx, `SELECT order_id FROM refunds WHERE id = ?`, refundID).Scan(&orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, apperr.NotFound("Refund")
	}
	return orderID, err
}

type refundTicket struct {
	ID           int64
	TicketTypeID int64
	PriceCents   int64
}

// lockRefundableTickets locks the requested tickets (all valid ones when ids is empty) and
// checks each is valid, unused when required, and not already part of an open refund.
func lockRefundableTickets(ctx context.Context, tx *sql.Tx, orderID int64, ids []int64, allowCheckedIn bool) ([]refundTicket, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.ticket_type_id, t.price_cents, t.status, t.checkin_state,
		EXISTS (SELECT 1 FROM refund_items ri JOIN refunds r ON r.id = ri.refund_id
			WHERE ri.ticket_id = t.id AND r.status IN ('requested', 'approved', 'processing'))
		FROM tickets t WHERE t.order_id = ? ORDER BY t.id FOR UPDATE`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requested := map[int64]bool{}
	for _, id := range ids {
		requested[id] = true
	}
	all := len(requested) == 0
	found := 0
	var out []refundTicket
	for rows.Next() {
		var t refundTicket
		var status, state string
		var inOpenRefund bool
		if err := rows.Scan(&t.ID, &t.TicketTypeID, &t.PriceCents, &status, &state, &inOpenRefund); err != nil {
			return nil, err
		}
		usable := status == "valid" && !inOpenRefund && (allowCheckedIn || state == "not_checked_in")
		if all {
			if usable {
				out = append(out, t)
			}
			continue
		}
		if !requested[t.ID] {
			continue
		}
		found++
		switch {
		case status != "valid":
			return nil, apperr.FieldError("ticket_ids", "includes a ticket that has already been refunded or cancelled")
		case inOpenRefund:
			return nil, apperr.FieldError("ticket_ids", "includes a ticket that already has a refund in progress")
		case !allowCheckedIn && state != "not_checked_in":
			return nil, apperr.FieldError("ticket_ids", "includes a ticket that has already been used")
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found != len(requested) {
		return nil, apperr.FieldError("ticket_ids", "includes tickets that are not part of this order")
	}
	if len(out) == 0 {
		return nil, apperr.Conflict("nothing_to_refund", "There are no tickets on this order that can be refunded.")
	}
	return out, nil
}

func succeededPaymentID(ctx context.Context, tx *sql.Tx, orderID int64) (*int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM payments WHERE order_id = ? AND status = 'succeeded' ORDER BY id LIMIT 1`, orderID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &id, err
}

// insertTicketRefund creates a refund covering the given tickets.
func (s *Service) insertTicketRefund(ctx context.Context, tx *sql.Tx, o orderRow, items []refundTicket, status, initiatedBy, reason string, actor *auth.Principal) (int64, error) {
	paymentID, err := succeededPaymentID(ctx, tx, o.ID)
	if err != nil {
		return 0, err
	}
	var amount int64
	for _, t := range items {
		amount += t.PriceCents
	}
	now := s.now()
	var requestedBy, reviewedBy, reviewedAt any
	if actor != nil {
		requestedBy = actor.UserID
		if status == "approved" {
			reviewedBy, reviewedAt = actor.UserID, now
		}
	}
	var refundID int64
	_, err = insertWithUniqueCode(tickets.NewRefundReference, func(ref string) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO refunds (reference, order_id, payment_id, amount_cents, currency, reason, status, initiated_by,
			requested_by, reviewed_by, reviewed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ref, o.ID, paymentID, amount, o.Currency, truncate(reason, 500), status, initiatedBy, requestedBy, reviewedBy, reviewedAt, now, now)
		if err == nil {
			refundID, _ = res.LastInsertId()
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, t := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO refund_items (refund_id, ticket_id, amount_cents) VALUES (?, ?, ?)`, refundID, t.ID, t.PriceCents); err != nil {
			return 0, err
		}
	}
	if status == "approved" {
		if err := voidTickets(ctx, tx, refundID, now); err != nil {
			return 0, err
		}
	}
	return refundID, audit.Record(ctx, tx, now, audit.Entry{Action: "refund.created", EntityType: "refund", EntityID: refundID, OrganiserID: o.OrganiserID,
		Summary: fmt.Sprintf("Refund of %s for %d ticket(s) on order %s (%s): %s", money.Format(amount, o.Currency), len(items), o.Reference, status, truncate(reason, 200))})
}

func voidTickets(ctx context.Context, tx *sql.Tx, refundID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE tickets SET status = 'void', updated_at = ? WHERE status = 'valid'
		AND id IN (SELECT ticket_id FROM refund_items WHERE refund_id = ?)`, now, refundID)
	return err
}

// RefundRequestInput asks for tickets to be refunded.
type RefundRequestInput struct {
	TicketIDs []int64 `json:"ticket_ids"`
	Reason    string  `json:"reason"`
}

// RequestRefund lets a customer ask for a refund within the event's refund policy.
func (s *Service) RequestRefund(ctx context.Context, p *auth.Principal, orderID int64, in RefundRequestInput) (RefundView, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return RefundView{}, apperr.FieldError("reason", "is required")
	}
	var refundID int64
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !ownsOrder(o.CustomerID, p.UserID) {
			return apperr.NotFound("Order")
		}
		if (o.Status != OrderPaid && o.Status != OrderPartiallyRefunded) || o.Source == "complimentary" {
			return apperr.Conflict("not_refundable", "This order cannot be refunded.")
		}
		ev, err := s.loadSaleEvent(ctx, tx, o.EventID, false)
		if err != nil {
			return err
		}
		switch {
		case ev.Status == "cancelled":
			return apperr.Conflict("event_cancelled", "This event was cancelled; your refund is processed automatically.")
		case !ev.RefundsAllowed:
			return apperr.Conflict("refunds_not_allowed", "The organiser does not offer refunds for this event.")
		case !s.now().Before(ev.StartsAt.Add(-time.Duration(ev.RefundCutoffHours) * time.Hour)):
			return apperr.Conflict("refund_window_closed", fmt.Sprintf("Refunds close %d hours before the event starts.", ev.RefundCutoffHours))
		}
		items, err := lockRefundableTickets(ctx, tx, o.ID, in.TicketIDs, false)
		if err != nil {
			return err
		}
		refundID, err = s.insertTicketRefund(ctx, tx, o, items, "requested", "customer", in.Reason, p)
		return err
	})
	if err != nil {
		return RefundView{}, err
	}
	return s.refundView(ctx, refundID)
}

// CreateRefund lets staff refund tickets on an order directly (the refund is pre-approved).
func (s *Service) CreateRefund(ctx context.Context, p *auth.Principal, orderID int64, in RefundRequestInput) (RefundView, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return RefundView{}, apperr.FieldError("reason", "is required")
	}
	var refundID int64
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !p.CanFor(o.OrganiserID, rbac.RefundsManage) {
			return apperr.NotFound("Order")
		}
		if o.Status != OrderPaid && o.Status != OrderPartiallyRefunded {
			return apperr.Conflict("not_refundable", "Only paid orders can be refunded.")
		}
		items, err := lockRefundableTickets(ctx, tx, o.ID, in.TicketIDs, true)
		if err != nil {
			return err
		}
		refundID, err = s.insertTicketRefund(ctx, tx, o, items, "approved", "staff", in.Reason, p)
		return err
	})
	if err != nil {
		return RefundView{}, err
	}
	return s.refundView(ctx, refundID)
}

// ReviewRefund approves or rejects a customer's refund request.
func (s *Service) ReviewRefund(ctx context.Context, p *auth.Principal, refundID int64, approve bool, note string) (RefundView, error) {
	note = strings.TrimSpace(note)
	if !approve && note == "" {
		return RefundView{}, apperr.FieldError("note", "explain to the customer why the refund was declined")
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		orderID, err := refundOrderID(ctx, tx, refundID)
		if err != nil {
			return err
		}
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !p.CanFor(o.OrganiserID, rbac.RefundsManage) {
			return apperr.NotFound("Refund")
		}
		r, err := lockRefund(ctx, tx, refundID)
		if err != nil {
			return err
		}
		if r.Status != "requested" {
			return apperr.Conflict("invalid_status", "Only requested refunds can be reviewed.")
		}
		now := s.now()
		status := "rejected"
		if approve {
			status = "approved"
			var used int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets t JOIN refund_items ri ON ri.ticket_id = t.id
				WHERE ri.refund_id = ? AND (t.status <> 'valid' OR t.checkin_state <> 'not_checked_in') FOR UPDATE`, refundID).Scan(&used); err != nil {
				return err
			}
			if used > 0 {
				return apperr.Conflict("ticket_used", "One or more of these tickets have been used or cancelled since the request was made.")
			}
			if err := voidTickets(ctx, tx, refundID, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE refunds SET status = ?, review_note = ?, reviewed_by = ?, reviewed_at = ?, updated_at = ? WHERE id = ?`,
			status, truncate(note, 500), p.UserID, now, now, refundID); err != nil {
			return err
		}
		if !approve {
			if err := jobs.Enqueue(ctx, tx, now, JobSendRefundUpdate, map[string]int64{"refund_id": refundID}); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "refund." + status, EntityType: "refund", EntityID: refundID, OrganiserID: o.OrganiserID,
			Summary: fmt.Sprintf("Refund %s for order %s %s", r.Reference, o.Reference, status)})
	})
	if err != nil {
		return RefundView{}, err
	}
	return s.refundView(ctx, refundID)
}

// ProcessRefundInput carries the bank reference for refunds of EFT payments, which finance
// pays out manually.
type ProcessRefundInput struct {
	BankReference string `json:"bank_reference"`
}

// ProcessRefund moves the money for an approved refund: card/online payments are refunded
// through the gateway, EFT payments are recorded with the bank transfer reference.
func (s *Service) ProcessRefund(ctx context.Context, p *auth.Principal, refundID int64, in ProcessRefundInput) (RefundView, error) {
	var gatewayCall *payments.RefundRequest
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		orderID, err := refundOrderID(ctx, tx, refundID)
		if err != nil {
			return err
		}
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !p.CanFor(o.OrganiserID, rbac.RefundsProcess) {
			return apperr.NotFound("Refund")
		}
		r, err := lockRefund(ctx, tx, refundID)
		if err != nil {
			return err
		}
		if r.Status != "approved" && r.Status != "failed" {
			return apperr.Conflict("invalid_status", "Only approved or failed refunds can be processed.")
		}
		var pay paymentRow
		if r.PaymentID != nil {
			if pay, err = lockPayment(ctx, tx, *r.PaymentID); err != nil {
				return err
			}
		}
		now := s.now()
		switch {
		case r.AmountCents == 0 || r.PaymentID == nil:
			return s.completeRefund(ctx, tx, o, r, "", "")
		case pay.Method == MethodEFT:
			ref := strings.TrimSpace(in.BankReference)
			if ref == "" {
				return apperr.FieldError("bank_reference", "enter the reference of the bank transfer you made to the customer")
			}
			return s.completeRefund(ctx, tx, o, r, "", truncate(ref, 100))
		case pay.Method == MethodOnline:
			if _, err := tx.ExecContext(ctx, `UPDATE refunds SET status = 'processing', processed_by = ?, failure_reason = '', updated_at = ? WHERE id = ?`,
				p.UserID, now, r.ID); err != nil {
				return err
			}
			gatewayCall = &payments.RefundRequest{
				PaymentReference: pay.ProviderReference, TransactionID: pay.TransactionID, AmountCents: r.AmountCents,
				Currency: r.Currency, RefundReference: r.Reference, Reason: "Refund " + r.Reference + " for order " + o.Reference,
			}
			return audit.Record(ctx, tx, now, audit.Entry{Action: "refund.processing", EntityType: "refund", EntityID: r.ID, OrganiserID: o.OrganiserID,
				Summary: fmt.Sprintf("Refund %s of %s sent to %s", r.Reference, money.Format(r.AmountCents, r.Currency), pay.Provider)})
		default:
			return s.completeRefund(ctx, tx, o, r, "", "")
		}
	})
	if err != nil {
		return RefundView{}, err
	}
	if gatewayCall != nil {
		result, err := s.gateway.Refund(ctx, *gatewayCall)
		if err != nil {
			s.markRefundFailed(ctx, refundID, "Gateway refund failed: "+err.Error())
			return RefundView{}, apperr.New(http.StatusBadGateway, "refund_failed", "The payment provider rejected the refund. It has been marked as failed and can be retried.")
		}
		if err := s.applyGatewayRefund(ctx, refundID, result); err != nil {
			return RefundView{}, err
		}
	}
	return s.refundView(ctx, refundID)
}

// applyGatewayRefund records the gateway's view of a refund.
func (s *Service) applyGatewayRefund(ctx context.Context, refundID int64, result payments.RefundResult) error {
	switch result.Status {
	case payments.RefundCompleted:
		return s.db.WithTx(ctx, func(tx *sql.Tx) error {
			orderID, err := refundOrderID(ctx, tx, refundID)
			if err != nil {
				return err
			}
			o, err := lockOrder(ctx, tx, orderID)
			if err != nil {
				return err
			}
			r, err := lockRefund(ctx, tx, refundID)
			if err != nil || r.Status == "completed" {
				return err
			}
			return s.completeRefund(ctx, tx, o, r, result.ProviderRefundID, "")
		})
	case payments.RefundFailed:
		s.markRefundFailed(ctx, refundID, result.FailureReason)
		return nil
	default:
		_, err := s.db.ExecContext(ctx, `UPDATE refunds SET provider_refund_id = ?, updated_at = ? WHERE id = ?`, result.ProviderRefundID, s.now(), refundID)
		return err
	}
}

func (s *Service) markRefundFailed(ctx context.Context, refundID int64, reason string) {
	if _, err := s.db.ExecContext(ctx, `UPDATE refunds SET status = 'failed', failure_reason = ?, updated_at = ? WHERE id = ? AND status = 'processing'`,
		truncate(reason, 500), s.now(), refundID); err != nil {
		s.logger.Error("mark refund failed", "refund_id", refundID, "error", err)
	}
}

// completeRefund records that the money has been returned: tickets become refunded, their
// seats return to inventory, and the order and payment totals are updated. The order and
// refund must be locked by the caller.
func (s *Service) completeRefund(ctx context.Context, tx *sql.Tx, o orderRow, r refundRow, providerRefundID, bankReference string) error {
	now := s.now()
	if r.PaymentID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE payments SET refunded_cents = refunded_cents + ?, updated_at = ? WHERE id = ?`, r.AmountCents, now, *r.PaymentID); err != nil {
			return err
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT t.ticket_type_id, COUNT(*) FROM refund_items ri JOIN tickets t ON t.id = ri.ticket_id
		WHERE ri.refund_id = ? AND t.status <> 'refunded' GROUP BY t.ticket_type_id`, r.ID)
	if err != nil {
		return err
	}
	perType := map[int64]int{}
	var typeIDs []int64
	for rows.Next() {
		var typeID int64
		var n int
		if err := rows.Scan(&typeID, &n); err != nil {
			rows.Close()
			return err
		}
		perType[typeID] = n
		typeIDs = append(typeIDs, typeID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if len(typeIDs) > 0 {
		if _, err := s.loadSaleEvent(ctx, tx, o.EventID, true); err != nil {
			return err
		}
		if _, err := lockTicketTypes(ctx, tx, o.EventID, typeIDs); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET status = 'refunded', updated_at = ?
			WHERE id IN (SELECT ticket_id FROM refund_items WHERE refund_id = ?)`, now, r.ID); err != nil {
			return err
		}
		for typeID, n := range perType {
			if _, err := tx.ExecContext(ctx, `UPDATE ticket_types SET quantity_sold = quantity_sold - ?, updated_at = ? WHERE id = ?`, n, now, typeID); err != nil {
				return err
			}
		}
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE order_id = ? AND status <> 'refunded'`, o.ID).Scan(&remaining); err != nil {
			return err
		}
		status := OrderPartiallyRefunded
		if remaining == 0 {
			status = OrderRefunded
		}
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET refunded_cents = refunded_cents + ?, status = ?, updated_at = ? WHERE id = ?`,
			r.AmountCents, status, now, o.ID); err != nil {
			return err
		}
	}

	var processedBy any
	if p := auth.FromContext(ctx); p != nil {
		processedBy = p.UserID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE refunds SET status = 'completed', processed_at = ?, processed_by = COALESCE(processed_by, ?),
		provider_refund_id = CASE WHEN ? <> '' THEN ? ELSE provider_refund_id END, bank_reference = ?, failure_reason = '', updated_at = ? WHERE id = ?`,
		now, processedBy, providerRefundID, providerRefundID, bankReference, now, r.ID); err != nil {
		return err
	}
	if err := jobs.Enqueue(ctx, tx, now, JobSendRefundUpdate, map[string]int64{"refund_id": r.ID}); err != nil {
		return err
	}
	return audit.Record(ctx, tx, now, audit.Entry{Action: "refund.completed", EntityType: "refund", EntityID: r.ID, OrganiserID: o.OrganiserID,
		Summary: fmt.Sprintf("Refund %s of %s completed for order %s", r.Reference, money.Format(r.AmountCents, r.Currency), o.Reference)})
}

// syncRefundsForPayment re-checks in-flight refunds of a payment with the gateway.
func (s *Service) syncRefundsForPayment(ctx context.Context, paymentReference string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.provider_refund_id FROM refunds r JOIN payments pa ON pa.id = r.payment_id
		WHERE r.status = 'processing' AND r.provider_refund_id <> '' AND (pa.provider_reference = ? OR pa.provider_transaction_id = ?)`,
		paymentReference, paymentReference)
	if err != nil {
		return err
	}
	type pending struct {
		id         int64
		providerID string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.providerID); err != nil {
			rows.Close()
			return err
		}
		list = append(list, p)
	}
	rows.Close()
	for _, p := range list {
		result, err := s.gateway.RefundStatus(ctx, p.providerID)
		if err != nil {
			return err
		}
		if err := s.applyGatewayRefund(ctx, p.id, result); err != nil {
			return err
		}
	}
	return rows.Err()
}

// EventCancelled implements events.CancellationHook: pending reservations are released and
// every paid order receives an approved refund for its valid tickets, ready for finance.
func (s *Service) EventCancelled(ctx context.Context, eventID int64, reason string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, status FROM orders WHERE event_id = ? AND status IN ('pending', 'paid', 'partially_refunded')`, eventID)
	if err != nil {
		return err
	}
	type entry struct {
		id     int64
		status string
	}
	var list []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.status); err != nil {
			rows.Close()
			return err
		}
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var errs []error
	for _, e := range list {
		err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
			o, err := lockOrder(ctx, tx, e.id)
			if err != nil {
				return err
			}
			if o.Status == OrderPending {
				return s.release(ctx, tx, o, OrderCancelled)
			}
			if o.Status != OrderPaid && o.Status != OrderPartiallyRefunded {
				return nil
			}
			items, err := lockRefundableTickets(ctx, tx, o.ID, nil, true)
			if apperr.IsCode(err, "nothing_to_refund") {
				return nil
			}
			if err != nil {
				return err
			}
			if _, err := s.insertTicketRefund(ctx, tx, o, items, "approved", "system", "Event cancelled: "+reason, nil); err != nil {
				return err
			}
			return jobs.Enqueue(ctx, tx, s.now(), JobSendEventCancelled, map[string]int64{"order_id": o.ID})
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("order %d: %w", e.id, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) refundView(ctx context.Context, id int64) (RefundView, error) {
	list, err := s.refundViews(ctx, s.db, "r.id = ?", []any{id}, "r.id", 1, 0)
	if err != nil {
		return RefundView{}, err
	}
	if len(list) == 0 {
		return RefundView{}, apperr.NotFound("Refund")
	}
	return list[0], nil
}
