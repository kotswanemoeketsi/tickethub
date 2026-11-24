package sales

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/payments"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// AttendeeInput is the person attending on one ticket.
type AttendeeInput struct {
	OrderItemID int64  `json:"order_item_id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
}

func (a *AttendeeInput) normalize() {
	a.FirstName, a.LastName, a.Phone = strings.TrimSpace(a.FirstName), strings.TrimSpace(a.LastName), strings.TrimSpace(a.Phone)
	a.Email = validate.NormalizeEmail(a.Email)
}

func (a AttendeeInput) validate(v validate.Errors, prefix string) {
	v.Required(prefix+".first_name", a.FirstName)
	v.MaxLen(prefix+".first_name", a.FirstName, 100)
	v.Required(prefix+".last_name", a.LastName)
	v.MaxLen(prefix+".last_name", a.LastName, 100)
	v.Required(prefix+".email", a.Email)
	v.Email(prefix+".email", a.Email)
	v.Phone(prefix+".phone", a.Phone)
}

// CheckoutInput completes the attendee details and chooses how to pay.
type CheckoutInput struct {
	Attendees     []AttendeeInput `json:"attendees"`
	PaymentMethod string          `json:"payment_method"` // online | eft; ignored for free orders
}

// CheckoutResult tells the client what to do next.
type CheckoutResult struct {
	Order       OrderView        `json:"order"`
	Next        string           `json:"next"` // complete | redirect | eft
	RedirectURL string           `json:"redirect_url,omitempty"`
	EFT         *EFTInstructions `json:"eft,omitempty"`
}

var errReservationExpired = apperr.Conflict("reservation_expired", "Your reservation has expired. Please select your tickets again.")

// Checkout records attendees and starts payment. Free orders are completed immediately;
// online payments return the gateway's hosted page; EFT returns bank details.
func (s *Service) Checkout(ctx context.Context, p *auth.Principal, orderID int64, in CheckoutInput) (CheckoutResult, error) {
	for i := range in.Attendees {
		in.Attendees[i].normalize()
	}
	var (
		next            string
		expired         bool
		pay             paymentRow
		eventTitle      string
		orderRef        string
		orderEmail      string
		eftDeadline     time.Time
		checkoutExpires time.Time
	)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !ownsOrder(o.CustomerID, p.UserID) {
			return apperr.NotFound("Order")
		}
		if o.Status == OrderPaid {
			return apperr.Conflict("order_already_paid", "This order has already been paid.")
		}
		if o.Status != OrderPending {
			return errReservationExpired
		}
		now := s.now()
		if o.ExpiresAt != nil && !now.Before(*o.ExpiresAt) {
			expired = true
			return errReservationExpired
		}
		items, err := loadItems(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if err := saveAttendees(ctx, tx, o.ID, items, in.Attendees, now); err != nil {
			return err
		}
		ev, err := s.loadSaleEvent(ctx, tx, o.EventID, false)
		if err != nil {
			return err
		}
		eventTitle, orderRef, orderEmail = ev.Title, o.Reference, o.CustomerEmail

		if o.TotalCents == 0 {
			next = "complete"
			pay, err = s.insertPayment(ctx, tx, o, MethodFree, MethodFree, "succeeded", o.Reference)
			if err != nil {
				return err
			}
			return s.applySuccessfulPayment(ctx, tx, o, pay)
		}

		switch in.PaymentMethod {
		case MethodOnline:
			next = "redirect"
			if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'cancelled', updated_at = ? WHERE order_id = ? AND status = 'pending'`, now, o.ID); err != nil {
				return err
			}
			pay, err = s.insertPayment(ctx, tx, o, s.gateway.Name(), MethodOnline, "pending", "")
			if err != nil {
				return err
			}
			// Give the customer time on the gateway's page, within a hard cap per order.
			expires := maxTime(*o.ExpiresAt, now.Add(10*time.Minute))
			expires = minTime(expires, o.CreatedAt.Add(45*time.Minute))
			checkoutExpires = expires
			_, err = tx.ExecContext(ctx, `UPDATE orders SET payment_method = 'online', expires_at = ?, updated_at = ? WHERE id = ?`, expires, now, o.ID)
			return err

		case MethodEFT:
			next = "eft"
			if !s.cfg.EFTEnabled {
				return apperr.FieldError("payment_method", "EFT payments are not available")
			}
			eftDeadline = minTime(now.Add(s.cfg.EFTWindow), ev.StartsAt.Add(-2*time.Hour))
			if eftDeadline.Sub(now) < time.Hour {
				return apperr.Conflict("eft_unavailable", "This event starts too soon to pay by EFT. Please pay online.")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'cancelled', updated_at = ? WHERE order_id = ? AND status = 'pending' AND method <> 'eft'`, now, o.ID); err != nil {
				return err
			}
			// The EFT payment uses the order reference, so an earlier (cancelled) EFT attempt
			// is reopened rather than duplicated.
			var existing int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM payments WHERE order_id = ? AND provider = 'manual_eft' ORDER BY id DESC LIMIT 1`, o.ID).Scan(&existing)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				if pay, err = s.insertPayment(ctx, tx, o, "manual_eft", MethodEFT, "pending", o.Reference); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'pending', failure_reason = '', updated_at = ? WHERE id = ?`, now, existing); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE orders SET payment_method = 'eft', expires_at = ?, updated_at = ? WHERE id = ?`, eftDeadline, now, o.ID); err != nil {
				return err
			}
			return jobs.Enqueue(ctx, tx, now, JobSendEFTInstructions, map[string]int64{"order_id": o.ID})

		default:
			return apperr.FieldError("payment_method", "choose online or eft")
		}
	})
	if expired {
		if err := s.expireOrder(ctx, orderID); err != nil {
			s.logger.Error("expire order during checkout", "order_id", orderID, "error", err)
		}
	}
	if err != nil {
		return CheckoutResult{}, err
	}

	result := CheckoutResult{Next: next}
	if next == "redirect" {
		session, err := s.gateway.CreateCheckout(ctx, payments.CheckoutRequest{
			ExpiresAt:   checkoutExpires,
			Reference:   pay.ProviderReference,
			AmountCents: pay.AmountCents,
			Currency:    pay.Currency,
			Email:       orderEmail,
			CallbackURL: fmt.Sprintf("%s/orders/%d/payment-return", s.cfg.PublicURL, orderID),
			Description: fmt.Sprintf("%s – order %s", eventTitle, orderRef),
			Metadata:    map[string]string{"order_id": strconv.FormatInt(orderID, 10), "order_reference": orderRef},
		})
		if err != nil {
			s.logger.Error("create gateway checkout", "order_id", orderID, "error", err)
			_, _ = s.db.ExecContext(ctx, `UPDATE payments SET status = 'failed', failure_reason = ?, updated_at = ? WHERE id = ?`,
				truncate("Could not start payment: "+err.Error(), 500), s.now(), pay.ID)
			return CheckoutResult{}, apperr.New(http.StatusBadGateway, "payment_unavailable",
				"We could not reach the payment provider. Please try again or choose EFT.")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE payments SET checkout_url = ?, provider_request_id = ?, updated_at = ? WHERE id = ?`, session.RedirectURL, session.ProviderRequestID, s.now(), pay.ID); err != nil {
			return CheckoutResult{}, err
		}
		result.RedirectURL = session.RedirectURL
	}
	view, err := s.orderView(ctx, s.db, orderID, true)
	if err != nil {
		return CheckoutResult{}, err
	}
	result.Order, result.EFT = view, view.EFT
	return result, nil
}

// saveAttendees replaces the order's attendees, checking there is one per ticket.
func saveAttendees(ctx context.Context, tx *sql.Tx, orderID int64, items []itemRow, attendees []AttendeeInput, now time.Time) error {
	v := validate.New()
	perItem := map[int64]int{}
	for i, a := range attendees {
		a.validate(v, fmt.Sprintf("attendees.%d", i))
		perItem[a.OrderItemID]++
	}
	for _, it := range items {
		if perItem[it.ID] != it.Quantity {
			v.Add("attendees", fmt.Sprintf("enter details for each of the %d %s ticket(s)", it.Quantity, it.TicketTypeName))
		}
		delete(perItem, it.ID)
	}
	if len(perItem) > 0 {
		v.Add("attendees", "contains tickets that are not part of this order")
	}
	if err := v.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM attendees WHERE order_id = ?`, orderID); err != nil {
		return err
	}
	for _, a := range attendees {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attendees (order_id, order_item_id, first_name, last_name, email, phone, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, orderID, a.OrderItemID, a.FirstName, a.LastName, a.Email, a.Phone, now, now); err != nil {
			return err
		}
	}
	return nil
}

// insertPayment creates a payment row. An empty reference generates a gateway reference.
func (s *Service) insertPayment(ctx context.Context, tx *sql.Tx, o orderRow, provider, method, status, reference string) (paymentRow, error) {
	now := s.now()
	var paidAt any
	if status == "succeeded" {
		paidAt = now
	}
	pay := paymentRow{OrderID: o.ID, Provider: provider, Method: method, Status: status, AmountCents: o.TotalCents, Currency: o.Currency}
	gen := tickets.NewPaymentReference
	if reference != "" {
		gen = func() string { return reference }
	}
	ref, err := insertWithUniqueCode(gen, func(ref string) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO payments (order_id, provider, method, status, amount_cents, currency, provider_reference, paid_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, o.ID, provider, method, status, o.TotalCents, o.Currency, ref, paidAt, now, now)
		if err == nil {
			pay.ID, _ = res.LastInsertId()
		}
		return err
	})
	pay.ProviderReference = ref
	return pay, err
}

// CancelOrder lets a customer abandon an unpaid order, releasing the tickets.
func (s *Service) CancelOrder(ctx context.Context, p *auth.Principal, orderID int64) (OrderView, error) {
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if !ownsOrder(o.CustomerID, p.UserID) {
			return apperr.NotFound("Order")
		}
		if o.Status != OrderPending {
			return apperr.Conflict("order_not_pending", "Only unpaid orders can be cancelled. Request a refund instead.")
		}
		return s.release(ctx, tx, o, OrderCancelled)
	})
	if err != nil {
		return OrderView{}, err
	}
	return s.orderView(ctx, s.db, orderID, true)
}

// VerifyOrderPayment asks the gateway, server to server, for the state of the customer's
// latest online payment. It is called when the customer returns from the payment page, so
// tickets appear immediately even if the webhook is delayed.
func (s *Service) VerifyOrderPayment(ctx context.Context, p *auth.Principal, orderID int64) (OrderView, error) {
	o, err := getOrder(ctx, s.db, orderID)
	if err != nil {
		return OrderView{}, err
	}
	if !ownsOrder(o.CustomerID, p.UserID) {
		return OrderView{}, apperr.NotFound("Order")
	}
	if o.Status == OrderPending || o.Status == OrderExpired || o.Status == OrderCancelled {
		var reference string
		err := s.db.QueryRowContext(ctx, `SELECT provider_reference FROM payments WHERE order_id = ? AND provider = ? AND method = 'online'
			ORDER BY id DESC LIMIT 1`, orderID, s.gateway.Name()).Scan(&reference)
		if err == nil {
			if err := s.SyncPayment(ctx, reference); err != nil {
				s.logger.Warn("verify payment on return", "order_id", orderID, "error", err)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return OrderView{}, err
		}
	}
	return s.orderView(ctx, s.db, orderID, true)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
