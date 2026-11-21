package sales

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// ReserveItem is a ticket type and quantity in a reservation request.
type ReserveItem struct {
	TicketTypeID int64 `json:"ticket_type_id"`
	Quantity     int   `json:"quantity"`
}

// ReserveInput starts a purchase.
type ReserveInput struct {
	EventID   int64         `json:"event_id"`
	Items     []ReserveItem `json:"items"`
	PromoCode string        `json:"promo_code"`
}

func (in ReserveInput) validate() error {
	v := validate.New()
	v.Check(in.EventID > 0, "event_id", "is required")
	v.Check(len(in.Items) > 0, "items", "select at least one ticket")
	seen := map[int64]bool{}
	total := 0
	for _, it := range in.Items {
		if it.TicketTypeID <= 0 || it.Quantity < 1 {
			v.Add("items", "each item needs a ticket type and a quantity of at least 1")
			break
		}
		if seen[it.TicketTypeID] {
			v.Add("items", "each ticket type may only appear once")
			break
		}
		seen[it.TicketTypeID] = true
		total += it.Quantity
	}
	v.Check(total <= maxTicketsPerOrder, "items", fmt.Sprintf("a single order can contain at most %d tickets", maxTicketsPerOrder))
	return v.Err()
}

type lockedPromotion struct {
	ID            int64
	Type          string
	Value         int64
	MaxUses       *int
	TimesUsed     int
	StartsAt      *time.Time
	EndsAt        *time.Time
	Active        bool
	TicketTypeIDs map[int64]bool // empty means every ticket type
}

func lockPromotionByCode(ctx context.Context, tx *sql.Tx, eventID int64, code string) (*lockedPromotion, error) {
	var p lockedPromotion
	err := tx.QueryRowContext(ctx, `SELECT id, discount_type, discount_value, max_uses, times_used, starts_at, ends_at, is_active
		FROM promotions WHERE event_id = ? AND code = ? FOR UPDATE`, eventID, code).
		Scan(&p.ID, &p.Type, &p.Value, &p.MaxUses, &p.TimesUsed, &p.StartsAt, &p.EndsAt, &p.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.FieldError("promo_code", "is not valid for this event")
	}
	if err != nil {
		return nil, err
	}
	p.TicketTypeIDs = map[int64]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT ticket_type_id FROM promotion_ticket_types WHERE promotion_id = ?`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		p.TicketTypeIDs[id] = true
	}
	return &p, rows.Err()
}

func (p *lockedPromotion) usable(now time.Time) error {
	switch {
	case !p.Active:
		return apperr.FieldError("promo_code", "is no longer active")
	case p.StartsAt != nil && now.Before(*p.StartsAt):
		return apperr.FieldError("promo_code", "is not active yet")
	case p.EndsAt != nil && !now.Before(*p.EndsAt):
		return apperr.FieldError("promo_code", "has expired")
	case p.MaxUses != nil && p.TimesUsed >= *p.MaxUses:
		return apperr.FieldError("promo_code", "has reached its usage limit")
	}
	return nil
}

func (p *lockedPromotion) appliesTo(ticketTypeID int64) bool {
	return len(p.TicketTypeIDs) == 0 || p.TicketTypeIDs[ticketTypeID]
}

// Reserve holds tickets for a customer while they check out. The hold expires after the
// reservation TTL unless payment completes; the background worker then releases it.
func (s *Service) Reserve(ctx context.Context, p *auth.Principal, in ReserveInput) (OrderView, error) {
	if err := in.validate(); err != nil {
		return OrderView{}, err
	}
	if !s.limiter.Allow(ctx, fmt.Sprintf("reserve:%d", p.UserID), 20, 10*time.Minute) {
		return OrderView{}, apperr.TooManyRequests("You have started too many checkouts. Please wait a few minutes.")
	}
	// One active checkout per customer per event: starting again releases the previous hold,
	// so nobody can tie up inventory with abandoned carts.
	if err := s.releaseCustomerReservations(ctx, p.UserID, in.EventID); err != nil {
		return OrderView{}, err
	}

	code := strings.ToUpper(strings.TrimSpace(in.PromoCode))
	var orderID int64
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		ev, err := s.loadSaleEvent(ctx, tx, in.EventID, true)
		if err != nil {
			return err
		}
		now := s.now()
		if ev.Status != "published" || ev.OrganiserStatus != "active" {
			return apperr.Conflict("event_not_on_sale", "Tickets for this event are not on sale.")
		}
		if !now.Before(ev.EndsAt) {
			return apperr.Conflict("sales_closed", "This event has ended.")
		}

		ids := make([]int64, len(in.Items))
		for i, it := range in.Items {
			ids[i] = it.TicketTypeID
		}
		types, err := lockTicketTypes(ctx, tx, ev.ID, ids)
		if err != nil {
			return err
		}
		committed, err := eventCommitted(ctx, tx, ev.ID)
		if err != nil {
			return err
		}

		var promo *lockedPromotion
		if code != "" {
			if promo, err = lockPromotionByCode(ctx, tx, ev.ID, code); err != nil {
				return err
			}
			if err := promo.usable(now); err != nil {
				return err
			}
		}

		lines := make([]PriceLine, 0, len(in.Items))
		requested := 0
		for _, it := range in.Items {
			t, ok := types[it.TicketTypeID]
			if !ok || t.Hidden {
				return apperr.FieldError("items", "contains a ticket type that is not available for this event")
			}
			if err := checkSaleWindow(t, ev, now); err != nil {
				return err
			}
			if it.Quantity < t.MinPerOrder || it.Quantity > t.MaxPerOrder {
				return apperr.FieldError("items", fmt.Sprintf("%s tickets must be bought in quantities of %d to %d", t.Name, t.MinPerOrder, t.MaxPerOrder))
			}
			if it.Quantity > t.available() {
				return soldOut(t.Name, t.available())
			}
			requested += it.Quantity
			lines = append(lines, PriceLine{TicketTypeID: t.ID, UnitPriceCents: t.PriceCents, Quantity: it.Quantity,
				Eligible: promo != nil && promo.appliesTo(t.ID)})
		}
		if remaining := ev.Capacity - committed; requested > remaining {
			return apperr.Conflict("sold_out", fmt.Sprintf("Only %d ticket(s) are left for this event.", max(remaining, 0)))
		}

		var discount *Discount
		if promo != nil {
			discount = &Discount{Type: promo.Type, Value: promo.Value}
		}
		quote := Price(lines, discount)
		if promo != nil && quote.DiscountCents == 0 {
			return apperr.FieldError("promo_code", "does not apply to the selected tickets")
		}

		var promoID any
		if promo != nil {
			promoID = promo.ID
		}
		expiresAt := now.Add(s.cfg.ReservationTTL)
		_, err = insertWithUniqueCode(tickets.NewOrderReference, func(ref string) error {
			res, err := tx.ExecContext(ctx, `INSERT INTO orders (reference, event_id, organiser_id, customer_id, customer_name, customer_email,
				source, status, currency, subtotal_cents, discount_cents, total_cents, promotion_id, expires_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 'online', 'pending', ?, ?, ?, ?, ?, ?, ?, ?)`,
				ref, ev.ID, ev.OrganiserID, p.UserID, strings.TrimSpace(p.FirstName+" "+p.LastName), p.Email,
				ev.Currency, quote.SubtotalCents, quote.DiscountCents, quote.TotalCents, promoID, expiresAt, now, now)
			if err == nil {
				orderID, _ = res.LastInsertId()
			}
			return err
		})
		if err != nil {
			return err
		}
		for _, l := range quote.Lines {
			if _, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, ticket_type_id, ticket_type_name, unit_price_cents, quantity,
				discount_cents, total_cents, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				orderID, l.TicketTypeID, types[l.TicketTypeID].Name, l.UnitPriceCents, l.Quantity, l.DiscountCents, l.TotalCents, now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE ticket_types SET quantity_reserved = quantity_reserved + ?, updated_at = ? WHERE id = ?`,
				l.Quantity, now, l.TicketTypeID); err != nil {
				return err
			}
		}
		if promo != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE promotions SET times_used = times_used + 1 WHERE id = ?`, promo.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return OrderView{}, err
	}
	return s.CustomerOrder(ctx, p, orderID)
}

func checkSaleWindow(t lockedType, ev saleEvent, now time.Time) error {
	if t.SalesStart != nil && now.Before(*t.SalesStart) {
		return apperr.Conflict("sales_not_started", fmt.Sprintf("Sales for %s have not started yet.", t.Name))
	}
	end := ev.EndsAt
	if t.SalesEnd != nil {
		end = *t.SalesEnd
	}
	if !now.Before(end) {
		return apperr.Conflict("sales_ended", fmt.Sprintf("Sales for %s have ended.", t.Name))
	}
	return nil
}

func soldOut(name string, available int) error {
	if available <= 0 {
		return apperr.Conflict("sold_out", fmt.Sprintf("%s tickets are sold out.", name))
	}
	return apperr.Conflict("sold_out", fmt.Sprintf("Only %d %s ticket(s) are left.", available, name))
}

// releaseCustomerReservations cancels the customer's unpaid checkouts for an event. Orders
// already waiting on an EFT payment are left alone.
func (s *Service) releaseCustomerReservations(ctx context.Context, userID, eventID int64) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM orders WHERE customer_id = ? AND event_id = ? AND status = 'pending'
		AND (payment_method IS NULL OR payment_method = 'online')`, userID, eventID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
			o, err := lockOrder(ctx, tx, id)
			if err != nil {
				return err
			}
			return s.release(ctx, tx, o, OrderCancelled)
		}); err != nil {
			return err
		}
	}
	return nil
}
