// Package sales is the heart of the platform: reservations, checkout, payment confirmation,
// ticket issuing, complimentary tickets and refunds.
//
// Concurrency rules (all inventory changes follow them):
//
//   - Inventory lives in ticket_types.quantity_sold / quantity_reserved and is only changed
//     inside a transaction that holds a row lock on the event first, then on the ticket
//     types (in ascending id order). This serialises sales per event and makes overselling
//     impossible; a CHECK constraint in the schema backs this up.
//   - Locks are always taken in the order: order → payment → refund → event → ticket types
//     → promotion. A consistent order is what prevents deadlocks; the transaction helper
//     retries the rare deadlock InnoDB still reports.
//   - Payment confirmation is idempotent, so webhooks, the customer's return to the site,
//     and the reconciliation job may all report the same payment safely.
package sales

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/payments"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/ratelimit"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// Job types emitted by this package and handled by the notifications worker.
const (
	JobSendOrderTickets    = "email.order_tickets"
	JobSendTicket          = "email.ticket"
	JobSendEFTInstructions = "email.eft_instructions"
	JobSendRefundUpdate    = "email.refund_update"
	JobSendEventCancelled  = "email.event_cancelled"
)

// Order statuses.
const (
	OrderPending           = "pending"
	OrderPaid              = "paid"
	OrderExpired           = "expired"
	OrderCancelled         = "cancelled"
	OrderRefunded          = "refunded"
	OrderPartiallyRefunded = "partially_refunded"
)

// Payment methods.
const (
	MethodOnline        = "online"
	MethodEFT           = "eft"
	MethodFree          = "free"
	MethodComplimentary = "complimentary"
)

const maxTicketsPerOrder = 50

// BankDetails are shown to customers paying by EFT.
type BankDetails struct {
	BankName      string `json:"bank_name"`
	AccountName   string `json:"account_name"`
	AccountNumber string `json:"account_number"`
	BranchCode    string `json:"branch_code"`
}

// Config holds sales settings.
type Config struct {
	AppName        string
	PublicURL      string
	ReservationTTL time.Duration
	EFTEnabled     bool
	EFTWindow      time.Duration
	Bank           BankDetails
}

// Service implements the sales use cases.
type Service struct {
	db      *database.DB
	gateway payments.Gateway
	signer  *tickets.Signer
	limiter *ratelimit.Limiter
	cfg     Config
	now     clock.Func
	logger  *slog.Logger
}

// NewService wires the sales service.
func NewService(db *database.DB, gateway payments.Gateway, signer *tickets.Signer, limiter *ratelimit.Limiter,
	cfg Config, now clock.Func, logger *slog.Logger) *Service {
	return &Service{db: db, gateway: gateway, signer: signer, limiter: limiter, cfg: cfg, now: now, logger: logger}
}

// Gateway exposes the configured payment gateway (for the payment-options endpoint).
func (s *Service) Gateway() payments.Gateway { return s.gateway }

// ---- Row types and locking helpers ----------------------------------------------------

type orderRow struct {
	ID            int64
	EventID       int64
	OrganiserID   int64
	Reference     string
	CustomerID    *int64
	CustomerName  string
	CustomerEmail string
	Source        string
	Status        string
	PaymentMethod *string
	Currency      string
	SubtotalCents int64
	DiscountCents int64
	TotalCents    int64
	RefundedCents int64
	PromotionID   *int64
	ExpiresAt     *time.Time
	PaidAt        *time.Time
	CreatedAt     time.Time
}

const orderColumns = `id, event_id, organiser_id, reference, customer_id, customer_name, customer_email, source, status,
	payment_method, currency, subtotal_cents, discount_cents, total_cents, refunded_cents, promotion_id, expires_at, paid_at, created_at`

func scanOrderRow(row interface{ Scan(...any) error }) (orderRow, error) {
	var o orderRow
	err := row.Scan(&o.ID, &o.EventID, &o.OrganiserID, &o.Reference, &o.CustomerID, &o.CustomerName, &o.CustomerEmail, &o.Source,
		&o.Status, &o.PaymentMethod, &o.Currency, &o.SubtotalCents, &o.DiscountCents, &o.TotalCents, &o.RefundedCents,
		&o.PromotionID, &o.ExpiresAt, &o.PaidAt, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return o, apperr.NotFound("Order")
	}
	return o, err
}

func getOrder(ctx context.Context, q database.Queryer, id int64) (orderRow, error) {
	return scanOrderRow(q.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ?`, id))
}

func lockOrder(ctx context.Context, tx *sql.Tx, id int64) (orderRow, error) {
	return scanOrderRow(tx.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ? FOR UPDATE`, id))
}

type paymentRow struct {
	ID                int64
	OrderID           int64
	Provider          string
	Method            string
	Status            string
	AmountCents       int64
	Currency          string
	ProviderReference string
	TransactionID     string
	RefundedCents     int64
}

const paymentColumns = `id, order_id, provider, method, status, amount_cents, currency, provider_reference, provider_transaction_id, refunded_cents`

func scanPaymentRow(row interface{ Scan(...any) error }) (paymentRow, error) {
	var p paymentRow
	err := row.Scan(&p.ID, &p.OrderID, &p.Provider, &p.Method, &p.Status, &p.AmountCents, &p.Currency, &p.ProviderReference,
		&p.TransactionID, &p.RefundedCents)
	if errors.Is(err, sql.ErrNoRows) {
		return p, apperr.NotFound("Payment")
	}
	return p, err
}

func lockPayment(ctx context.Context, tx *sql.Tx, id int64) (paymentRow, error) {
	return scanPaymentRow(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id = ? FOR UPDATE`, id))
}

// saleEvent is the event information needed to sell tickets.
type saleEvent struct {
	ID                int64
	OrganiserID       int64
	Title             string
	Slug              string
	Status            string
	Currency          string
	Timezone          string
	Capacity          int
	StartsAt          time.Time
	EndsAt            time.Time
	RefundsAllowed    bool
	RefundCutoffHours int
	OrganiserStatus   string
	CommissionBPS     int
}

func (s *Service) loadSaleEvent(ctx context.Context, q database.Queryer, id int64, forUpdate bool) (saleEvent, error) {
	var e saleEvent
	query := `SELECT id, organiser_id, title, slug, status, currency, timezone, capacity, starts_at, ends_at, refunds_allowed, refund_cutoff_hours
		FROM events WHERE id = ?`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	err := q.QueryRowContext(ctx, query, id).Scan(&e.ID, &e.OrganiserID, &e.Title, &e.Slug, &e.Status, &e.Currency, &e.Timezone,
		&e.Capacity, &e.StartsAt, &e.EndsAt, &e.RefundsAllowed, &e.RefundCutoffHours)
	if errors.Is(err, sql.ErrNoRows) {
		return e, apperr.NotFound("Event")
	}
	if err != nil {
		return e, err
	}
	// The organiser row is read without a lock so that sales of different events by the
	// same organiser never block each other.
	err = q.QueryRowContext(ctx, `SELECT status, commission_bps FROM organisers WHERE id = ?`, e.OrganiserID).Scan(&e.OrganiserStatus, &e.CommissionBPS)
	return e, err
}

type lockedType struct {
	ID          int64
	Name        string
	PriceCents  int64
	Total       int
	Sold        int
	Reserved    int
	MinPerOrder int
	MaxPerOrder int
	SalesStart  *time.Time
	SalesEnd    *time.Time
	Hidden      bool
}

func (t lockedType) available() int { return t.Total - t.Sold - t.Reserved }

// lockTicketTypes locks the requested ticket types of an event in ascending id order. The
// caller must already hold the event lock.
func lockTicketTypes(ctx context.Context, tx *sql.Tx, eventID int64, ids []int64) (map[int64]lockedType, error) {
	sorted := append([]int64(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rows, err := tx.QueryContext(ctx, `SELECT id, name, price_cents, quantity_total, quantity_sold, quantity_reserved,
		min_per_order, max_per_order, sales_start_at, sales_end_at, is_hidden
		FROM ticket_types WHERE event_id = ? AND id IN (`+database.Placeholders(len(sorted))+`) ORDER BY id FOR UPDATE`,
		append([]any{eventID}, database.Args(sorted)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]lockedType{}
	for rows.Next() {
		var t lockedType
		if err := rows.Scan(&t.ID, &t.Name, &t.PriceCents, &t.Total, &t.Sold, &t.Reserved, &t.MinPerOrder, &t.MaxPerOrder,
			&t.SalesStart, &t.SalesEnd, &t.Hidden); err != nil {
			return nil, err
		}
		out[t.ID] = t
	}
	return out, rows.Err()
}

// eventCommitted returns how many tickets of an event are sold or reserved. Callers hold the
// event lock, and every writer takes that lock first, so the sum is stable.
func eventCommitted(ctx context.Context, tx *sql.Tx, eventID int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(quantity_sold + quantity_reserved), 0) FROM ticket_types WHERE event_id = ?`, eventID).Scan(&n)
	return n, err
}

type itemRow struct {
	ID             int64
	TicketTypeID   int64
	TicketTypeName string
	UnitPriceCents int64
	Quantity       int
	DiscountCents  int64
	TotalCents     int64
}

func loadItems(ctx context.Context, q database.Queryer, orderID int64) ([]itemRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, ticket_type_id, ticket_type_name, unit_price_cents, quantity, discount_cents, total_cents
		FROM order_items WHERE order_id = ? ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []itemRow
	for rows.Next() {
		var it itemRow
		if err := rows.Scan(&it.ID, &it.TicketTypeID, &it.TicketTypeName, &it.UnitPriceCents, &it.Quantity, &it.DiscountCents, &it.TotalCents); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func itemTypeIDs(items []itemRow) []int64 {
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.TicketTypeID)
	}
	return ids
}

// release returns a pending order's reserved tickets to inventory and closes the order with
// newStatus (expired or cancelled). The order must be locked by the caller.
func (s *Service) release(ctx context.Context, tx *sql.Tx, o orderRow, newStatus string) error {
	if o.Status != OrderPending {
		return nil
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, `UPDATE payments SET status = 'cancelled', updated_at = ? WHERE order_id = ? AND status = 'pending'`, now, o.ID); err != nil {
		return err
	}
	if _, err := s.loadSaleEvent(ctx, tx, o.EventID, true); err != nil {
		return err
	}
	items, err := loadItems(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	if _, err := lockTicketTypes(ctx, tx, o.EventID, itemTypeIDs(items)); err != nil {
		return err
	}
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_types SET quantity_reserved = quantity_reserved - ?, updated_at = ? WHERE id = ?`,
			it.Quantity, now, it.TicketTypeID); err != nil {
			return fmt.Errorf("release ticket type %d: %w", it.TicketTypeID, err)
		}
	}
	if o.PromotionID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE promotions SET times_used = times_used - 1 WHERE id = ? AND times_used > 0`, *o.PromotionID); err != nil {
			return err
		}
	}
	var cancelledAt any
	if newStatus == OrderCancelled {
		cancelledAt = now
	}
	_, err = tx.ExecContext(ctx, `UPDATE orders SET status = ?, cancelled_at = ?, updated_at = ? WHERE id = ?`, newStatus, cancelledAt, now, o.ID)
	return err
}

// expireOrder releases a single pending order whose reservation has lapsed.
func (s *Service) expireOrder(ctx context.Context, orderID int64) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o.Status != OrderPending || o.ExpiresAt == nil || s.now().Before(*o.ExpiresAt) {
			return nil
		}
		return s.release(ctx, tx, o, OrderExpired)
	})
}

// insertWithUniqueCode retries insert with freshly generated codes when a unique key
// collides. MySQL rolls back only the failed statement, so the transaction continues.
func insertWithUniqueCode(generate func() string, insert func(code string) error) (string, error) {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		code := generate()
		if err = insert(code); err == nil {
			return code, nil
		}
		if !database.IsDuplicateKey(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not generate a unique code: %w", err)
}

func ownsOrder(customerID *int64, userID int64) bool {
	return customerID != nil && *customerID == userID
}
