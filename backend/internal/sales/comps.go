package sales

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// ComplimentaryInput issues free tickets to a guest.
type ComplimentaryInput struct {
	TicketTypeID int64  `json:"ticket_type_id"`
	Quantity     int    `json:"quantity"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Email        string `json:"email"`
	Note         string `json:"note"`
}

// IssueComplimentary creates a paid-in-full order at no charge. Complimentary tickets use real
// inventory, so they can never push an event over capacity, and are reported separately.
func (s *Service) IssueComplimentary(ctx context.Context, p *auth.Principal, eventID int64, in ComplimentaryInput) (OrderView, error) {
	in.FirstName, in.LastName, in.Note = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName), strings.TrimSpace(in.Note)
	in.Email = validate.NormalizeEmail(in.Email)
	v := validate.New()
	v.Check(in.TicketTypeID > 0, "ticket_type_id", "is required")
	v.Range("quantity", int64(in.Quantity), 1, maxTicketsPerOrder)
	v.Required("first_name", in.FirstName)
	v.Required("last_name", in.LastName)
	v.Required("email", in.Email)
	v.Email("email", in.Email)
	v.MaxLen("note", in.Note, 500)
	if err := v.Err(); err != nil {
		return OrderView{}, err
	}

	var orderID int64
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		ev, err := s.loadSaleEvent(ctx, tx, eventID, true)
		if err != nil {
			return err
		}
		if !p.CanFor(ev.OrganiserID, rbac.TicketsComplimentary) {
			return apperr.NotFound("Event")
		}
		now := s.now()
		if ev.Status != "published" || !now.Before(ev.EndsAt) {
			return apperr.Conflict("event_not_on_sale", "Complimentary tickets can only be issued for published events that have not ended.")
		}
		types, err := lockTicketTypes(ctx, tx, ev.ID, []int64{in.TicketTypeID})
		if err != nil {
			return err
		}
		t, ok := types[in.TicketTypeID]
		if !ok {
			return apperr.FieldError("ticket_type_id", "is not a ticket type of this event")
		}
		if in.Quantity > t.available() {
			return soldOut(t.Name, t.available())
		}
		committed, err := eventCommitted(ctx, tx, ev.ID)
		if err != nil {
			return err
		}
		if committed+in.Quantity > ev.Capacity {
			return apperr.Conflict("sold_out", fmt.Sprintf("Only %d ticket(s) are left for this event.", max(ev.Capacity-committed, 0)))
		}

		var customerID *int64
		var existing int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, in.Email).Scan(&existing)
		switch {
		case err == nil:
			customerID = &existing
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		name := in.FirstName + " " + in.LastName
		var reference string
		reference, err = insertWithUniqueCode(tickets.NewOrderReference, func(ref string) error {
			res, err := tx.ExecContext(ctx, `INSERT INTO orders (reference, event_id, organiser_id, customer_id, customer_name, customer_email,
				source, status, payment_method, currency, subtotal_cents, discount_cents, total_cents, commission_bps, paid_at, notes,
				created_by, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 'complimentary', 'pending', 'complimentary', ?, 0, 0, 0, ?, ?, ?, ?, ?, ?)`,
				ref, ev.ID, ev.OrganiserID, customerID, name, in.Email, ev.Currency, ev.CommissionBPS, now, in.Note, p.UserID, now, now)
			if err == nil {
				orderID, _ = res.LastInsertId()
			}
			return err
		})
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, ticket_type_id, ticket_type_name, unit_price_cents, quantity,
			discount_cents, total_cents, created_at) VALUES (?, ?, ?, 0, ?, 0, 0, ?)`, orderID, t.ID, t.Name, in.Quantity, now)
		if err != nil {
			return err
		}
		itemID, _ := res.LastInsertId()
		for i := 0; i < in.Quantity; i++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO attendees (order_id, order_item_id, first_name, last_name, email, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, orderID, itemID, in.FirstName, in.LastName, in.Email, now, now); err != nil {
				return err
			}
		}
		// Hold the seats, then convert them through the normal issuing path.
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_types SET quantity_reserved = quantity_reserved + ?, updated_at = ? WHERE id = ?`,
			in.Quantity, now, t.ID); err != nil {
			return err
		}
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if _, err := s.insertPayment(ctx, tx, o, MethodComplimentary, MethodComplimentary, "succeeded", reference); err != nil {
			return err
		}
		if _, err := s.issueTickets(ctx, tx, o, MethodComplimentary); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "tickets.complimentary_issued", EntityType: "order", EntityID: orderID,
			OrganiserID: ev.OrganiserID, Summary: fmt.Sprintf("%d complimentary %s ticket(s) for %q issued to %s (%s, face value %s each)",
				in.Quantity, t.Name, ev.Title, in.Email, reference, money.Format(t.PriceCents, ev.Currency))})
	})
	if err != nil {
		return OrderView{}, err
	}
	return s.orderView(ctx, s.db, orderID, false)
}

// ConfirmEFTInput records a bank transfer received from a customer.
type ConfirmEFTInput struct {
	AmountCents   int64  `json:"amount_cents"`
	BankReference string `json:"bank_reference"`
	Note          string `json:"note"`
}

// ConfirmEFT lets finance confirm a manual EFT payment after seeing it on the bank statement.
// Confirmation goes through the same fulfilment path as gateway payments, including the
// late-payment and duplicate-payment safeguards.
func (s *Service) ConfirmEFT(ctx context.Context, p *auth.Principal, paymentID int64, in ConfirmEFTInput) (PaymentView, error) {
	in.BankReference, in.Note = strings.TrimSpace(in.BankReference), strings.TrimSpace(in.Note)
	v := validate.New()
	v.Required("bank_reference", in.BankReference)
	v.MaxLen("bank_reference", in.BankReference, 100)
	v.MaxLen("note", in.Note, 500)
	if err := v.Err(); err != nil {
		return PaymentView{}, err
	}
	var orderID int64
	if err := s.db.QueryRowContext(ctx, `SELECT order_id FROM payments WHERE id = ?`, paymentID).Scan(&orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaymentView{}, apperr.NotFound("Payment")
		}
		return PaymentView{}, err
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !p.CanFor(o.OrganiserID, rbac.PaymentsConfirm) {
			return apperr.NotFound("Payment")
		}
		pay, err := lockPayment(ctx, tx, paymentID)
		if err != nil {
			return err
		}
		if pay.Method != MethodEFT {
			return apperr.Conflict("not_eft", "Only EFT payments are confirmed manually.")
		}
		if pay.Status == "succeeded" {
			return apperr.Conflict("already_confirmed", "This payment has already been confirmed.")
		}
		if in.AmountCents != pay.AmountCents {
			return apperr.FieldError("amount_cents", fmt.Sprintf("must equal the order total of %s; resolve short or over-payments with the customer first",
				money.Format(pay.AmountCents, pay.Currency)))
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'succeeded', provider_transaction_id = ?, confirmed_by = ?, confirmation_note = ?,
			paid_at = ?, failure_reason = '', updated_at = ? WHERE id = ?`, in.BankReference, p.UserID, in.Note, now, now, pay.ID); err != nil {
			return err
		}
		pay.Status = "succeeded"
		if err := audit.Record(ctx, tx, now, audit.Entry{Action: "payment.eft_confirmed", EntityType: "payment", EntityID: pay.ID, OrganiserID: o.OrganiserID,
			Summary: fmt.Sprintf("EFT of %s for order %s confirmed (bank ref %s)", money.Format(pay.AmountCents, pay.Currency), o.Reference, in.BankReference)}); err != nil {
			return err
		}
		return s.applySuccessfulPayment(ctx, tx, o, pay)
	})
	if err != nil {
		return PaymentView{}, err
	}
	list, err := s.paymentViews(ctx, s.db, "pa.id = ?", []any{paymentID}, "pa.id", 1, 0)
	if err != nil || len(list) == 0 {
		return PaymentView{}, err
	}
	return list[0], nil
}
