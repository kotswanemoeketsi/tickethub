package sales

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// EventRef is the event information embedded in orders and tickets.
type EventRef struct {
	ID                int64     `json:"id"`
	Title             string    `json:"title"`
	Slug              string    `json:"slug"`
	Status            string    `json:"status"`
	StartsAt          time.Time `json:"starts_at"`
	EndsAt            time.Time `json:"ends_at"`
	Timezone          string    `json:"timezone"`
	ImageURL          string    `json:"image_url"`
	VenueName         string    `json:"venue_name"`
	VenueAddress      string    `json:"venue_address"`
	VenueCity         string    `json:"venue_city"`
	OrganiserID       int64     `json:"organiser_id"`
	OrganiserName     string    `json:"organiser_name"`
	RefundsAllowed    bool      `json:"refunds_allowed"`
	RefundCutoffHours int       `json:"refund_cutoff_hours"`
}

const eventRefColumns = `e.id, e.title, e.slug, e.status, e.starts_at, e.ends_at, e.timezone, e.image_url,
	COALESCE(v.name, ''), TRIM(BOTH ', ' FROM CONCAT_WS(', ', NULLIF(v.address_line, ''), NULLIF(v.city, ''))), COALESCE(v.city, ''),
	o.id, o.name, e.refunds_allowed, e.refund_cutoff_hours`

const eventRefJoins = ` JOIN events e ON e.id = x.event_id JOIN organisers o ON o.id = e.organiser_id LEFT JOIN venues v ON v.id = e.venue_id`

func eventRefDest(e *EventRef) []any {
	return []any{&e.ID, &e.Title, &e.Slug, &e.Status, &e.StartsAt, &e.EndsAt, &e.Timezone, &e.ImageURL,
		&e.VenueName, &e.VenueAddress, &e.VenueCity, &e.OrganiserID, &e.OrganiserName, &e.RefundsAllowed, &e.RefundCutoffHours}
}

// OrderItemView is a line of an order.
type OrderItemView struct {
	ID             int64  `json:"id"`
	TicketTypeID   int64  `json:"ticket_type_id"`
	TicketTypeName string `json:"ticket_type_name"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	Quantity       int    `json:"quantity"`
	DiscountCents  int64  `json:"discount_cents"`
	TotalCents     int64  `json:"total_cents"`
}

// AttendeeView is a person attending on a ticket.
type AttendeeView struct {
	ID          int64  `json:"id"`
	OrderItemID int64  `json:"order_item_id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
}

// TicketView is an issued ticket.
type TicketView struct {
	ID               int64      `json:"id"`
	Code             string     `json:"code"`
	DisplayCode      string     `json:"display_code"`
	OrderID          int64      `json:"order_id"`
	OrderReference   string     `json:"order_reference"`
	TicketTypeID     int64      `json:"ticket_type_id"`
	TicketTypeName   string     `json:"ticket_type_name"`
	AttendeeID       int64      `json:"attendee_id"`
	HolderFirstName  string     `json:"holder_first_name"`
	HolderLastName   string     `json:"holder_last_name"`
	HolderEmail      string     `json:"holder_email"`
	PriceCents       int64      `json:"price_cents"`
	Currency         string     `json:"currency"`
	Status           string     `json:"status"`
	CheckinState     string     `json:"checkin_state"`
	FirstCheckedInAt *time.Time `json:"first_checked_in_at"`
	IsComplimentary  bool       `json:"is_complimentary"`
	QRPayload        string     `json:"qr_payload,omitempty"`
	Event            *EventRef  `json:"event,omitempty"`
	qrVersion        int
}

// PaymentView is a payment attempt.
type PaymentView struct {
	ID                int64      `json:"id"`
	OrderID           int64      `json:"order_id"`
	OrderReference    string     `json:"order_reference"`
	Provider          string     `json:"provider"`
	Method            string     `json:"method"`
	Status            string     `json:"status"`
	AmountCents       int64      `json:"amount_cents"`
	RefundedCents     int64      `json:"refunded_cents"`
	Currency          string     `json:"currency"`
	ProviderReference string     `json:"provider_reference"`
	TransactionID     string     `json:"transaction_id"`
	FailureReason     string     `json:"failure_reason"`
	ConfirmationNote  string     `json:"confirmation_note"`
	PaidAt            *time.Time `json:"paid_at"`
	CreatedAt         time.Time  `json:"created_at"`
	CustomerName      string     `json:"customer_name"`
	CustomerEmail     string     `json:"customer_email"`
	EventTitle        string     `json:"event_title"`
	OrderExpiresAt    *time.Time `json:"order_expires_at"`
	OrderStatus       string     `json:"order_status"`
}

// RefundView is a refund and its state.
type RefundView struct {
	ID               int64      `json:"id"`
	Reference        string     `json:"reference"`
	OrderID          int64      `json:"order_id"`
	OrderReference   string     `json:"order_reference"`
	PaymentMethod    string     `json:"payment_method"`
	AmountCents      int64      `json:"amount_cents"`
	Currency         string     `json:"currency"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	InitiatedBy      string     `json:"initiated_by"`
	ReviewNote       string     `json:"review_note"`
	ReviewedAt       *time.Time `json:"reviewed_at"`
	ProcessedAt      *time.Time `json:"processed_at"`
	ProviderRefundID string     `json:"provider_refund_id"`
	BankReference    string     `json:"bank_reference"`
	FailureReason    string     `json:"failure_reason"`
	CreatedAt        time.Time  `json:"created_at"`
	TicketIDs        []int64    `json:"ticket_ids"`
	CustomerName     string     `json:"customer_name"`
	CustomerEmail    string     `json:"customer_email"`
	EventTitle       string     `json:"event_title"`
}

// EFTInstructions tell a customer how to pay by bank transfer.
type EFTInstructions struct {
	BankDetails
	Reference   string    `json:"reference"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	PayBy       time.Time `json:"pay_by"`
}

// OrderView is the full view of an order.
type OrderView struct {
	ID               int64            `json:"id"`
	Reference        string           `json:"reference"`
	Status           string           `json:"status"`
	Source           string           `json:"source"`
	PaymentMethod    *string          `json:"payment_method"`
	Event            EventRef         `json:"event"`
	CustomerID       *int64           `json:"customer_id"`
	CustomerName     string           `json:"customer_name"`
	CustomerEmail    string           `json:"customer_email"`
	Currency         string           `json:"currency"`
	SubtotalCents    int64            `json:"subtotal_cents"`
	DiscountCents    int64            `json:"discount_cents"`
	TotalCents       int64            `json:"total_cents"`
	RefundedCents    int64            `json:"refunded_cents"`
	PromotionCode    string           `json:"promotion_code"`
	ExpiresAt        *time.Time       `json:"expires_at"`
	PaidAt           *time.Time       `json:"paid_at"`
	CancelledAt      *time.Time       `json:"cancelled_at"`
	CreatedAt        time.Time        `json:"created_at"`
	Notes            string           `json:"notes"`
	Items            []OrderItemView  `json:"items"`
	Attendees        []AttendeeView   `json:"attendees"`
	Tickets          []TicketView     `json:"tickets"`
	Payments         []PaymentView    `json:"payments"`
	Refunds          []RefundView     `json:"refunds"`
	EFT              *EFTInstructions `json:"eft,omitempty"`
	CanRequestRefund bool             `json:"can_request_refund"`
}

// OrderSummary is an order in a list.
type OrderSummary struct {
	ID            int64      `json:"id"`
	Reference     string     `json:"reference"`
	Status        string     `json:"status"`
	Source        string     `json:"source"`
	PaymentMethod *string    `json:"payment_method"`
	EventID       int64      `json:"event_id"`
	EventTitle    string     `json:"event_title"`
	EventStartsAt time.Time  `json:"event_starts_at"`
	EventTimezone string     `json:"event_timezone"`
	CustomerName  string     `json:"customer_name"`
	CustomerEmail string     `json:"customer_email"`
	Currency      string     `json:"currency"`
	TotalCents    int64      `json:"total_cents"`
	RefundedCents int64      `json:"refunded_cents"`
	TicketCount   int        `json:"ticket_count"`
	ExpiresAt     *time.Time `json:"expires_at"`
	PaidAt        *time.Time `json:"paid_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// orderView assembles the full view of an order. withQR adds QR payloads to valid tickets
// and must only be set for the ticket owner.
func (s *Service) orderView(ctx context.Context, q database.Queryer, id int64, withQR bool) (OrderView, error) {
	var v OrderView
	var notes string
	dest := []any{&v.ID, &v.Reference, &v.Status, &v.Source, &v.PaymentMethod, &v.CustomerID, &v.CustomerName, &v.CustomerEmail,
		&v.Currency, &v.SubtotalCents, &v.DiscountCents, &v.TotalCents, &v.RefundedCents, &v.PromotionCode, &v.ExpiresAt,
		&v.PaidAt, &v.CancelledAt, &v.CreatedAt, &notes}
	dest = append(dest, eventRefDest(&v.Event)...)
	err := q.QueryRowContext(ctx, `SELECT x.id, x.reference, x.status, x.source, x.payment_method, x.customer_id, x.customer_name,
		x.customer_email, x.currency, x.subtotal_cents, x.discount_cents, x.total_cents, x.refunded_cents, COALESCE(pr.code, ''),
		x.expires_at, x.paid_at, x.cancelled_at, x.created_at, x.notes, `+eventRefColumns+`
		FROM orders x LEFT JOIN promotions pr ON pr.id = x.promotion_id`+eventRefJoins+` WHERE x.id = ?`, id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return v, apperr.NotFound("Order")
	}
	if err != nil {
		return v, err
	}
	v.Notes = notes

	if v.Items, err = s.orderItemViews(ctx, q, id); err != nil {
		return v, err
	}
	if v.Attendees, err = s.attendeeViews(ctx, q, id); err != nil {
		return v, err
	}
	if v.Tickets, err = s.ticketViews(ctx, q, "t.order_id = ?", []any{id}, withQR); err != nil {
		return v, err
	}
	if v.Payments, err = s.paymentViews(ctx, q, "pa.order_id = ?", []any{id}, "pa.id DESC", 50, 0); err != nil {
		return v, err
	}
	if v.Refunds, err = s.refundViews(ctx, q, "r.order_id = ?", []any{id}, "r.id DESC", 50, 0); err != nil {
		return v, err
	}
	if v.Status == OrderPending && v.PaymentMethod != nil && *v.PaymentMethod == MethodEFT && v.ExpiresAt != nil {
		v.EFT = &EFTInstructions{BankDetails: s.cfg.Bank, Reference: v.Reference, AmountCents: v.TotalCents, Currency: v.Currency, PayBy: *v.ExpiresAt}
	}
	v.CanRequestRefund = s.refundableTickets(v, s.now()) > 0
	return v, nil
}

// refundableTickets counts the tickets a customer could still ask to refund.
func (s *Service) refundableTickets(v OrderView, now time.Time) int {
	if v.Status != OrderPaid && v.Status != OrderPartiallyRefunded {
		return 0
	}
	if !v.Event.RefundsAllowed || v.Event.Status == "cancelled" || v.Source == "complimentary" {
		return 0
	}
	if !now.Before(v.Event.StartsAt.Add(-time.Duration(v.Event.RefundCutoffHours) * time.Hour)) {
		return 0
	}
	open := map[int64]bool{}
	for _, r := range v.Refunds {
		if r.Status == "requested" || r.Status == "approved" || r.Status == "processing" {
			for _, id := range r.TicketIDs {
				open[id] = true
			}
		}
	}
	n := 0
	for _, t := range v.Tickets {
		if t.Status == "valid" && t.CheckinState == "not_checked_in" && !open[t.ID] {
			n++
		}
	}
	return n
}

func (s *Service) orderItemViews(ctx context.Context, q database.Queryer, orderID int64) ([]OrderItemView, error) {
	items, err := loadItems(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]OrderItemView, len(items))
	for i, it := range items {
		out[i] = OrderItemView{ID: it.ID, TicketTypeID: it.TicketTypeID, TicketTypeName: it.TicketTypeName, UnitPriceCents: it.UnitPriceCents,
			Quantity: it.Quantity, DiscountCents: it.DiscountCents, TotalCents: it.TotalCents}
	}
	return out, nil
}

func (s *Service) attendeeViews(ctx context.Context, q database.Queryer, orderID int64) ([]AttendeeView, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, order_item_id, first_name, last_name, email, phone FROM attendees WHERE order_id = ? ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttendeeView{}
	for rows.Next() {
		var a AttendeeView
		if err := rows.Scan(&a.ID, &a.OrderItemID, &a.FirstName, &a.LastName, &a.Email, &a.Phone); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ticketViews loads tickets matching where (aliases: t tickets, a attendees, x orders).
func (s *Service) ticketViews(ctx context.Context, q database.Queryer, where string, args []any, withQR bool) ([]TicketView, error) {
	rows, err := q.QueryContext(ctx, `SELECT t.id, t.code, t.order_id, x.reference, t.ticket_type_id, tt.name, t.attendee_id,
		a.first_name, a.last_name, a.email, t.price_cents, x.currency, t.status, t.checkin_state, t.first_checked_in_at,
		t.is_complimentary, t.qr_version
		FROM tickets t JOIN attendees a ON a.id = t.attendee_id JOIN ticket_types tt ON tt.id = t.ticket_type_id
		JOIN orders x ON x.id = t.order_id
		WHERE `+where+` ORDER BY t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TicketView{}
	for rows.Next() {
		var t TicketView
		if err := rows.Scan(&t.ID, &t.Code, &t.OrderID, &t.OrderReference, &t.TicketTypeID, &t.TicketTypeName, &t.AttendeeID,
			&t.HolderFirstName, &t.HolderLastName, &t.HolderEmail, &t.PriceCents, &t.Currency, &t.Status, &t.CheckinState,
			&t.FirstCheckedInAt, &t.IsComplimentary, &t.qrVersion); err != nil {
			return nil, err
		}
		t.DisplayCode = tickets.FormatCode(t.Code)
		if withQR && t.Status == "valid" {
			t.QRPayload = s.signer.Payload(t.Code, t.qrVersion)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// paymentViews loads payments matching where (aliases: pa payments, x orders, e events).
func (s *Service) paymentViews(ctx context.Context, q database.Queryer, where string, args []any, order string, limit, offset int) ([]PaymentView, error) {
	rows, err := q.QueryContext(ctx, `SELECT pa.id, pa.order_id, x.reference, pa.provider, pa.method, pa.status, pa.amount_cents,
		pa.refunded_cents, pa.currency, pa.provider_reference, pa.provider_transaction_id, pa.failure_reason, pa.confirmation_note,
		pa.paid_at, pa.created_at, x.customer_name, x.customer_email, e.title, x.expires_at, x.status
		FROM payments pa JOIN orders x ON x.id = pa.order_id JOIN events e ON e.id = x.event_id
		WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PaymentView{}
	for rows.Next() {
		var p PaymentView
		if err := rows.Scan(&p.ID, &p.OrderID, &p.OrderReference, &p.Provider, &p.Method, &p.Status, &p.AmountCents, &p.RefundedCents,
			&p.Currency, &p.ProviderReference, &p.TransactionID, &p.FailureReason, &p.ConfirmationNote, &p.PaidAt, &p.CreatedAt,
			&p.CustomerName, &p.CustomerEmail, &p.EventTitle, &p.OrderExpiresAt, &p.OrderStatus); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// refundViews loads refunds matching where (aliases: r refunds, x orders, e events).
func (s *Service) refundViews(ctx context.Context, q database.Queryer, where string, args []any, order string, limit, offset int) ([]RefundView, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.id, r.reference, r.order_id, x.reference, COALESCE(pa.method, ''), r.amount_cents, r.currency,
		r.reason, r.status, r.initiated_by, r.review_note, r.reviewed_at, r.processed_at, r.provider_refund_id, r.bank_reference,
		r.failure_reason, r.created_at, x.customer_name, x.customer_email, e.title
		FROM refunds r JOIN orders x ON x.id = r.order_id JOIN events e ON e.id = x.event_id LEFT JOIN payments pa ON pa.id = r.payment_id
		WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	out := []RefundView{}
	index := map[int64]int{}
	for rows.Next() {
		var r RefundView
		if err := rows.Scan(&r.ID, &r.Reference, &r.OrderID, &r.OrderReference, &r.PaymentMethod, &r.AmountCents, &r.Currency, &r.Reason,
			&r.Status, &r.InitiatedBy, &r.ReviewNote, &r.ReviewedAt, &r.ProcessedAt, &r.ProviderRefundID, &r.BankReference,
			&r.FailureReason, &r.CreatedAt, &r.CustomerName, &r.CustomerEmail, &r.EventTitle); err != nil {
			rows.Close()
			return nil, err
		}
		r.TicketIDs = []int64{}
		index[r.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]any, 0, len(out))
	for _, r := range out {
		ids = append(ids, r.ID)
	}
	itemRows, err := q.QueryContext(ctx, `SELECT refund_id, ticket_id FROM refund_items WHERE refund_id IN (`+database.Placeholders(len(ids))+`) ORDER BY ticket_id`, ids...)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var refundID, ticketID int64
		if err := itemRows.Scan(&refundID, &ticketID); err != nil {
			return nil, err
		}
		out[index[refundID]].TicketIDs = append(out[index[refundID]].TicketIDs, ticketID)
	}
	return out, itemRows.Err()
}
