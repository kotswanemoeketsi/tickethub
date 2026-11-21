package events

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
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

const selectTicketType = `SELECT id, event_id, name, description, category, price_cents, quantity_total, quantity_sold, quantity_reserved,
	min_per_order, max_per_order, sales_start_at, sales_end_at, is_hidden, sort_order FROM ticket_types`

func scanTicketType(row interface{ Scan(...any) error }) (TicketType, error) {
	var t TicketType
	err := row.Scan(&t.ID, &t.EventID, &t.Name, &t.Description, &t.Category, &t.PriceCents, &t.QuantityTotal, &t.QuantitySold,
		&t.QuantityReserved, &t.MinPerOrder, &t.MaxPerOrder, &t.SalesStartAt, &t.SalesEndAt, &t.IsHidden, &t.SortOrder)
	return t, err
}

// decorate fills the derived fields of ticket types for an event.
func decorate(types []TicketType, e Event, now time.Time) {
	remaining := e.Capacity - e.Stats.TicketsSold - e.Stats.TicketsReserved
	for i := range types {
		t := &types[i]
		t.QuantityAvailable = max(0, min(t.QuantityTotal-t.QuantitySold-t.QuantityReserved, remaining))
		t.SaleStatus = SaleStatus(*t, remaining, e.EndsAt, now)
		t.SalesStartAtLocal = localInputPtr(t.SalesStartAt, e.Timezone)
		t.SalesEndAtLocal = localInputPtr(t.SalesEndAt, e.Timezone)
	}
}

func (s *Service) ticketTypes(ctx context.Context, q database.Queryer, e Event) ([]TicketType, error) {
	rows, err := q.QueryContext(ctx, selectTicketType+` WHERE event_id = ? ORDER BY sort_order, id`, e.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketType
	for rows.Next() {
		t, err := scanTicketType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	decorate(out, e, s.now())
	return out, nil
}

// TicketTypes lists an event's ticket types for staff.
func (s *Service) TicketTypes(ctx context.Context, p *auth.Principal, eventID int64) ([]TicketType, error) {
	e, err := s.Authorize(ctx, p, eventID, rbac.EventsView)
	if err != nil {
		return nil, err
	}
	return s.ticketTypes(ctx, s.db, e)
}

// TicketTypeInput creates or edits a ticket type. Sale window times are wall-clock values
// in the event's time zone and are optional.
type TicketTypeInput struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Category      string `json:"category"`
	PriceCents    int64  `json:"price_cents"`
	QuantityTotal int    `json:"quantity_total"`
	MinPerOrder   int    `json:"min_per_order"`
	MaxPerOrder   int    `json:"max_per_order"`
	SalesStartAt  string `json:"sales_start_at"`
	SalesEndAt    string `json:"sales_end_at"`
	IsHidden      bool   `json:"is_hidden"`
	SortOrder     int    `json:"sort_order"`
}

type parsedTicketType struct {
	TicketTypeInput
	salesStart, salesEnd *time.Time
}

func parseTicketType(in TicketTypeInput, e Event) (parsedTicketType, error) {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Category == "" {
		in.Category = "general"
	}
	if in.MinPerOrder == 0 {
		in.MinPerOrder = 1
	}
	if in.MaxPerOrder == 0 {
		in.MaxPerOrder = 10
	}
	v := validate.New()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 100)
	v.MaxLen("description", in.Description, 500)
	v.OneOf("category", in.Category, TicketCategories...)
	v.Range("price_cents", in.PriceCents, 0, 100_000_000)
	v.Range("quantity_total", int64(in.QuantityTotal), 1, 1_000_000)
	v.Range("min_per_order", int64(in.MinPerOrder), 1, 100)
	v.Range("max_per_order", int64(in.MaxPerOrder), int64(in.MinPerOrder), 100)

	out := parsedTicketType{TicketTypeInput: in}
	parseOptional := func(field, value string) *time.Time {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		t, err := validate.ParseLocalDateTime(value, e.Timezone)
		if err != nil {
			v.Add(field, "must be a valid date and time")
			return nil
		}
		return &t
	}
	out.salesStart = parseOptional("sales_start_at", in.SalesStartAt)
	out.salesEnd = parseOptional("sales_end_at", in.SalesEndAt)
	if out.salesStart != nil && out.salesEnd != nil {
		v.Check(out.salesEnd.After(*out.salesStart), "sales_end_at", "must be after the sales start")
	}
	if out.salesEnd != nil {
		v.Check(!out.salesEnd.After(e.EndsAt), "sales_end_at", "must not be after the event ends")
	}
	return out, v.Err()
}

func editableForTickets(e Event) error {
	switch {
	case e.Status == StatusCancelled:
		return apperr.Conflict("event_cancelled", "Ticket types of a cancelled event cannot be changed.")
	case e.Status == StatusPendingReview:
		return apperr.Conflict("event_under_review", "This event is awaiting review. Withdraw it to change ticket types.")
	case e.Phase == PhaseCompleted:
		return apperr.Conflict("event_completed", "Ticket types of a completed event cannot be changed.")
	}
	return nil
}

// CreateTicketType adds a ticket type to an event.
func (s *Service) CreateTicketType(ctx context.Context, p *auth.Principal, eventID int64, in TicketTypeInput) (TicketType, error) {
	var created TicketType
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		e, err := s.lockEvent(ctx, tx, p, eventID, rbac.TicketTypesManage)
		if err != nil {
			return err
		}
		if err := editableForTickets(e); err != nil {
			return err
		}
		parsed, err := parseTicketType(in, e)
		if err != nil {
			return err
		}
		now := s.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO ticket_types (event_id, name, description, category, price_cents, quantity_total,
			min_per_order, max_per_order, sales_start_at, sales_end_at, is_hidden, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			eventID, parsed.Name, parsed.Description, parsed.Category, parsed.PriceCents, parsed.QuantityTotal, parsed.MinPerOrder,
			parsed.MaxPerOrder, parsed.salesStart, parsed.salesEnd, parsed.IsHidden, parsed.SortOrder, now, now)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if created, err = scanTicketType(tx.QueryRowContext(ctx, selectTicketType+` WHERE id = ?`, id)); err != nil {
			return err
		}
		decorateOne(&created, e, now)
		return audit.Record(ctx, tx, now, audit.Entry{Action: "ticket_type.created", EntityType: "ticket_type", EntityID: id, OrganiserID: e.OrganiserID,
			Summary: fmt.Sprintf("Ticket type %q added to %q at %s", parsed.Name, e.Title, formatMoney(parsed.PriceCents, e.Currency)),
			Changes: map[string]any{"price_cents": parsed.PriceCents, "quantity_total": parsed.QuantityTotal}})
	})
	if err != nil {
		return TicketType{}, err
	}
	s.invalidatePublic(ctx)
	return created, nil
}

func decorateOne(t *TicketType, e Event, now time.Time) {
	list := []TicketType{*t}
	decorate(list, e, now)
	*t = list[0]
}

// lockTicketType locks the event and then the ticket type, in that order.
func (s *Service) lockTicketType(ctx context.Context, tx *sql.Tx, p *auth.Principal, id int64) (Event, TicketType, error) {
	var eventID int64
	if err := tx.QueryRowContext(ctx, `SELECT event_id FROM ticket_types WHERE id = ?`, id).Scan(&eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, TicketType{}, apperr.NotFound("Ticket type")
		}
		return Event{}, TicketType{}, err
	}
	e, err := s.lockEvent(ctx, tx, p, eventID, rbac.TicketTypesManage)
	if err != nil {
		return Event{}, TicketType{}, err
	}
	t, err := scanTicketType(tx.QueryRowContext(ctx, selectTicketType+` WHERE id = ? FOR UPDATE`, id))
	if err != nil {
		return Event{}, TicketType{}, err
	}
	return e, t, nil
}

// UpdateTicketType edits a ticket type. Price changes never affect existing orders, which
// keep the price they were sold at, and are recorded explicitly in the audit log.
func (s *Service) UpdateTicketType(ctx context.Context, p *auth.Principal, id int64, in TicketTypeInput) (TicketType, error) {
	var updated TicketType
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		e, existing, err := s.lockTicketType(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if err := editableForTickets(e); err != nil {
			return err
		}
		parsed, err := parseTicketType(in, e)
		if err != nil {
			return err
		}
		if committed := existing.QuantitySold + existing.QuantityReserved; parsed.QuantityTotal < committed {
			return apperr.FieldError("quantity_total", fmt.Sprintf("cannot be lower than the %d tickets already sold or reserved", committed))
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_types SET name = ?, description = ?, category = ?, price_cents = ?, quantity_total = ?,
			min_per_order = ?, max_per_order = ?, sales_start_at = ?, sales_end_at = ?, is_hidden = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
			parsed.Name, parsed.Description, parsed.Category, parsed.PriceCents, parsed.QuantityTotal, parsed.MinPerOrder, parsed.MaxPerOrder,
			parsed.salesStart, parsed.salesEnd, parsed.IsHidden, parsed.SortOrder, now, id); err != nil {
			return err
		}
		if existing.PriceCents != parsed.PriceCents {
			if err := audit.Record(ctx, tx, now, audit.Entry{Action: "ticket_type.price_changed", EntityType: "ticket_type", EntityID: id,
				OrganiserID: e.OrganiserID,
				Summary: fmt.Sprintf("Price of %q (%s) changed from %s to %s", existing.Name, e.Title,
					formatMoney(existing.PriceCents, e.Currency), formatMoney(parsed.PriceCents, e.Currency)),
				Changes: audit.Diff{"price_cents": {From: existing.PriceCents, To: parsed.PriceCents}}}); err != nil {
				return err
			}
		}
		diff := audit.Diff{}
		diff.Add("name", existing.Name, parsed.Name)
		diff.Add("quantity_total", existing.QuantityTotal, parsed.QuantityTotal)
		diff.Add("is_hidden", existing.IsHidden, parsed.IsHidden)
		diff.Add("max_per_order", existing.MaxPerOrder, parsed.MaxPerOrder)
		if len(diff) > 0 {
			if err := audit.Record(ctx, tx, now, audit.Entry{Action: "ticket_type.updated", EntityType: "ticket_type", EntityID: id,
				OrganiserID: e.OrganiserID, Summary: fmt.Sprintf("Ticket type %q updated: %s", parsed.Name, diff.Fields()), Changes: diff}); err != nil {
				return err
			}
		}
		if updated, err = scanTicketType(tx.QueryRowContext(ctx, selectTicketType+` WHERE id = ?`, id)); err != nil {
			return err
		}
		decorateOne(&updated, e, now)
		return nil
	})
	if err != nil {
		return TicketType{}, err
	}
	s.invalidatePublic(ctx)
	return updated, nil
}

// DeleteTicketType removes a ticket type that has never been sold. Types with sales should
// be hidden instead, which keeps reporting intact.
func (s *Service) DeleteTicketType(ctx context.Context, p *auth.Principal, id int64) error {
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		e, existing, err := s.lockTicketType(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if err := editableForTickets(e); err != nil {
			return err
		}
		var items int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_items WHERE ticket_type_id = ?`, id).Scan(&items); err != nil {
			return err
		}
		if items > 0 {
			return apperr.Conflict("ticket_type_has_orders", "This ticket type has orders. Hide it instead of deleting it.")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_types WHERE id = ?`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "ticket_type.deleted", EntityType: "ticket_type", EntityID: id,
			OrganiserID: e.OrganiserID, Summary: fmt.Sprintf("Ticket type %q deleted from %q", existing.Name, e.Title)})
	})
	if err == nil {
		s.invalidatePublic(ctx)
	}
	return err
}

// formatMoney renders cents for human-readable audit summaries, e.g. "ZAR 150.00".
func formatMoney(cents int64, currency string) string {
	return fmt.Sprintf("%s %d.%02d", currency, cents/100, cents%100)
}
