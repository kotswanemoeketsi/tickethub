package sales

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/payments"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// SyncPayment fetches the gateway's authoritative state for a payment reference and applies
// it. This is the only path by which an online payment becomes "succeeded".
func (s *Service) SyncPayment(ctx context.Context, reference string) error {
	v, err := s.gateway.VerifyPayment(ctx, reference)
	if errors.Is(err, payments.ErrNotFound) {
		return nil // the customer has not reached the gateway yet
	}
	if err != nil {
		return fmt.Errorf("verify payment %s: %w", reference, err)
	}
	switch v.Status {
	case payments.StatusSucceeded:
		return s.confirmGatewayPayment(ctx, v)
	case payments.StatusFailed:
		_, err := s.db.ExecContext(ctx, `UPDATE payments SET status = 'failed', failure_reason = ?, raw_response = ?, updated_at = ?
			WHERE provider = ? AND provider_reference = ? AND status = 'pending'`,
			truncate(v.FailureReason, 500), rawJSON(v.Raw), s.now(), s.gateway.Name(), reference)
		return err
	default:
		return nil
	}
}

// confirmGatewayPayment records a verified successful gateway payment and fulfils the order.
func (s *Service) confirmGatewayPayment(ctx context.Context, v payments.Verification) error {
	var orderID, paymentID int64
	err := s.db.QueryRowContext(ctx, `SELECT order_id, id FROM payments WHERE provider = ? AND provider_reference = ?`,
		s.gateway.Name(), v.Reference).Scan(&orderID, &paymentID)
	if errors.Is(err, sql.ErrNoRows) {
		s.logger.Warn("gateway reported a payment we do not know", "reference", v.Reference)
		return nil
	}
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		pay, err := lockPayment(ctx, tx, paymentID)
		if err != nil {
			return err
		}
		if pay.Status == "succeeded" {
			return nil // already applied: idempotent
		}
		now := s.now()
		if v.AmountCents != pay.AmountCents || !strings.EqualFold(v.Currency, pay.Currency) {
			reason := fmt.Sprintf("Amount mismatch: expected %s, gateway reported %s",
				money.Format(pay.AmountCents, pay.Currency), money.Format(v.AmountCents, v.Currency))
			if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'failed', failure_reason = ?, raw_response = ?, updated_at = ? WHERE id = ?`,
				reason, rawJSON(v.Raw), now, pay.ID); err != nil {
				return err
			}
			s.logger.Error("payment amount mismatch; manual review required", "payment_id", pay.ID, "order", o.Reference)
			return audit.Record(ctx, tx, now, audit.Entry{Action: "payment.amount_mismatch", EntityType: "payment", EntityID: pay.ID,
				OrganiserID: o.OrganiserID, Summary: reason + " for order " + o.Reference})
		}
		paidAt := now
		if v.PaidAt != nil {
			paidAt = *v.PaidAt
		}
		if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'succeeded', provider_transaction_id = ?, paid_at = ?, raw_response = ?,
			failure_reason = '', updated_at = ? WHERE id = ?`, v.TransactionID, paidAt, rawJSON(v.Raw), now, pay.ID); err != nil {
			return err
		}
		pay.Status, pay.TransactionID = "succeeded", v.TransactionID
		return s.applySuccessfulPayment(ctx, tx, o, pay)
	})
}

// applySuccessfulPayment turns a succeeded payment into tickets. Edge cases are handled
// rather than ignored, because the customer's money has already been taken:
//
//   - the order is still reserved: tickets are issued from the reservation;
//   - the reservation lapsed before payment arrived: tickets are issued if still available,
//     otherwise the payment is queued for refund;
//   - the event was cancelled meanwhile, or the order was already paid by another payment:
//     the payment is queued for refund.
func (s *Service) applySuccessfulPayment(ctx context.Context, tx *sql.Tx, o orderRow, pay paymentRow) error {
	now := s.now()
	if err := audit.Record(ctx, tx, now, audit.Entry{Action: "payment.succeeded", EntityType: "payment", EntityID: pay.ID,
		OrganiserID: o.OrganiserID, Summary: fmt.Sprintf("%s payment of %s received for order %s", pay.Method, money.Format(pay.AmountCents, pay.Currency), o.Reference)}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'cancelled', updated_at = ? WHERE order_id = ? AND id <> ? AND status = 'pending'`,
		now, o.ID, pay.ID); err != nil {
		return err
	}

	switch o.Status {
	case OrderPending, OrderExpired, OrderCancelled:
		issued, err := s.issueTickets(ctx, tx, o, pay.Method)
		if err != nil || issued {
			return err
		}
		reason := "Payment arrived after the reservation expired and the tickets were no longer available."
		if o.Status == OrderPending {
			// Only possible when the event was cancelled while the customer was paying.
			if err := s.release(ctx, tx, o, OrderCancelled); err != nil {
				return err
			}
			reason = "The event was cancelled while the payment was being made."
		}
		return s.createPaymentRefund(ctx, tx, o, pay, reason)
	default:
		return s.createPaymentRefund(ctx, tx, o, pay, "Duplicate payment for an order that was already paid.")
	}
}

// issueTickets converts the order's tickets to sold and creates them. For a pending order the
// tickets come out of its reservation; for an expired or cancelled one they are re-acquired
// if still available. It reports false (and changes nothing) when tickets cannot be issued.
func (s *Service) issueTickets(ctx context.Context, tx *sql.Tx, o orderRow, method string) (bool, error) {
	ev, err := s.loadSaleEvent(ctx, tx, o.EventID, true)
	if err != nil {
		return false, err
	}
	if ev.Status == "cancelled" {
		return false, nil
	}
	items, err := loadItems(ctx, tx, o.ID)
	if err != nil {
		return false, err
	}
	types, err := lockTicketTypes(ctx, tx, o.EventID, itemTypeIDs(items))
	if err != nil {
		return false, err
	}
	fromReservation := o.Status == OrderPending
	if !fromReservation {
		committed, err := eventCommitted(ctx, tx, o.EventID)
		if err != nil {
			return false, err
		}
		needed := 0
		for _, it := range items {
			needed += it.Quantity
			if types[it.TicketTypeID].available() < it.Quantity {
				return false, nil
			}
		}
		if committed+needed > ev.Capacity {
			return false, nil
		}
	}

	now := s.now()
	for _, it := range items {
		query := `UPDATE ticket_types SET quantity_sold = quantity_sold + ?, updated_at = ? WHERE id = ?`
		if fromReservation {
			query = `UPDATE ticket_types SET quantity_reserved = quantity_reserved - ?, quantity_sold = quantity_sold + ?, updated_at = ? WHERE id = ?`
			if _, err := tx.ExecContext(ctx, query, it.Quantity, it.Quantity, now, it.TicketTypeID); err != nil {
				return false, err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, query, it.Quantity, now, it.TicketTypeID); err != nil {
			return false, err
		}
	}
	if !fromReservation && o.PromotionID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE promotions SET times_used = times_used + 1 WHERE id = ?`, *o.PromotionID); err != nil {
			return false, err
		}
	}
	if err := s.createTickets(ctx, tx, o, items); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'paid', payment_method = ?, paid_at = ?, commission_bps = ?, expires_at = NULL,
		cancelled_at = NULL, updated_at = ? WHERE id = ?`, method, now, ev.CommissionBPS, now, o.ID); err != nil {
		return false, err
	}
	if err := jobs.Enqueue(ctx, tx, now, JobSendOrderTickets, map[string]int64{"order_id": o.ID}); err != nil {
		return false, err
	}
	return true, audit.Record(ctx, tx, now, audit.Entry{Action: "order.paid", EntityType: "order", EntityID: o.ID, OrganiserID: o.OrganiserID,
		Summary: fmt.Sprintf("Order %s paid (%s) and tickets issued", o.Reference, method)})
}

// createTickets issues one ticket per attendee, splitting each line's total exactly.
func (s *Service) createTickets(ctx context.Context, tx *sql.Tx, o orderRow, items []itemRow) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, order_item_id FROM attendees WHERE order_id = ? ORDER BY id`, o.ID)
	if err != nil {
		return err
	}
	byItem := map[int64][]int64{}
	for rows.Next() {
		var id, itemID int64
		if err := rows.Scan(&id, &itemID); err != nil {
			rows.Close()
			return err
		}
		byItem[itemID] = append(byItem[itemID], id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := s.now()
	complimentary := o.Source == "complimentary"
	for _, it := range items {
		attendees := byItem[it.ID]
		if len(attendees) != it.Quantity {
			return fmt.Errorf("order %s: %d attendees for %d %s tickets", o.Reference, len(attendees), it.Quantity, it.TicketTypeName)
		}
		prices := UnitPrices(it.TotalCents, it.Quantity)
		for i, attendeeID := range attendees {
			if _, err := insertWithUniqueCode(tickets.NewCode, func(code string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO tickets (code, order_id, order_item_id, event_id, ticket_type_id, attendee_id,
					price_cents, status, is_complimentary, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'valid', ?, ?, ?)`,
					code, o.ID, it.ID, o.EventID, it.TicketTypeID, attendeeID, prices[i], complimentary, now, now)
				return err
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// createPaymentRefund queues a whole-payment refund (no tickets attached) for finance to process.
func (s *Service) createPaymentRefund(ctx context.Context, tx *sql.Tx, o orderRow, pay paymentRow, reason string) error {
	if pay.AmountCents == 0 {
		return nil
	}
	now := s.now()
	var refundID int64
	_, err := insertWithUniqueCode(tickets.NewRefundReference, func(ref string) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO refunds (reference, order_id, payment_id, amount_cents, currency, reason, status,
			initiated_by, reviewed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'approved', 'system', ?, ?, ?)`,
			ref, o.ID, pay.ID, pay.AmountCents, pay.Currency, reason, now, now, now)
		if err == nil {
			refundID, _ = res.LastInsertId()
		}
		return err
	})
	if err != nil {
		return err
	}
	s.logger.Warn("payment queued for refund", "order", o.Reference, "payment_id", pay.ID, "reason", reason)
	return audit.Record(ctx, tx, now, audit.Entry{Action: "refund.created", EntityType: "refund", EntityID: refundID, OrganiserID: o.OrganiserID,
		Summary: fmt.Sprintf("Automatic refund of %s queued for order %s: %s", money.Format(pay.AmountCents, pay.Currency), o.Reference, reason)})
}

// HandleWebhook verifies and records a gateway webhook, then re-checks the referenced
// transaction with the gateway's API before acting on it.
func (s *Service) HandleWebhook(ctx context.Context, provider string, header http.Header, body []byte) error {
	if provider != s.gateway.Name() {
		return apperr.NotFound("Webhook endpoint")
	}
	ev, err := s.gateway.ParseWebhook(header, body)
	if errors.Is(err, payments.ErrInvalidSignature) {
		s.logger.Warn("rejected webhook with invalid signature", "provider", provider)
		return apperr.New(http.StatusUnauthorized, "invalid_signature", "Invalid webhook signature.")
	}
	if err != nil {
		return apperr.BadRequest("Malformed webhook payload.")
	}

	now := s.now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO webhook_events (provider, event_key, event_type, payload, received_at) VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`, provider, ev.DedupeKey, truncate(ev.Type, 100), rawJSON(ev.Payload), now)
	if err != nil {
		return err
	}
	webhookID, _ := res.LastInsertId()

	switch ev.Kind {
	case payments.WebhookPayment:
		err = s.SyncPayment(ctx, ev.Reference)
	case payments.WebhookRefund:
		err = s.syncRefundsForPayment(ctx, ev.Reference)
	}
	status := ""
	if err != nil {
		status = truncate(err.Error(), 1000)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE webhook_events SET processed_at = ?, error = ? WHERE id = ?`, s.now(), status, webhookID)
	return err
}

// rawJSON returns a value suitable for a JSON column (NULL when empty or invalid).
func rawJSON(raw []byte) any {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	return raw
}
