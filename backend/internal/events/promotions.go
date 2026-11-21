package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

const selectPromotion = `SELECT id, event_id, code, description, discount_type, discount_value, max_uses, times_used,
	starts_at, ends_at, is_active, created_at FROM promotions`

func scanPromotion(row interface{ Scan(...any) error }) (Promotion, error) {
	var p Promotion
	err := row.Scan(&p.ID, &p.EventID, &p.Code, &p.Description, &p.DiscountType, &p.DiscountValue, &p.MaxUses, &p.TimesUsed,
		&p.StartsAt, &p.EndsAt, &p.IsActive, &p.CreatedAt)
	return p, err
}

func (s *Service) promotions(ctx context.Context, q database.Queryer, e Event, where string, args ...any) ([]Promotion, error) {
	rows, err := q.QueryContext(ctx, selectPromotion+` WHERE event_id = ? `+where+` ORDER BY created_at DESC`, append([]any{e.ID}, args...)...)
	if err != nil {
		return nil, err
	}
	var out []Promotion
	index := map[int64]int{}
	for rows.Next() {
		p, err := scanPromotion(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		p.TicketTypeIDs = []int64{}
		p.StartsAtLocal, p.EndsAtLocal = localInputPtr(p.StartsAt, e.Timezone), localInputPtr(p.EndsAt, e.Timezone)
		index[p.ID] = len(out)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]any, 0, len(out))
	for _, p := range out {
		ids = append(ids, p.ID)
	}
	rows, err = q.QueryContext(ctx, `SELECT promotion_id, ticket_type_id FROM promotion_ticket_types WHERE promotion_id IN (`+database.Placeholders(len(ids))+`)`, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var promoID, typeID int64
		if err := rows.Scan(&promoID, &typeID); err != nil {
			return nil, err
		}
		out[index[promoID]].TicketTypeIDs = append(out[index[promoID]].TicketTypeIDs, typeID)
	}
	return out, rows.Err()
}

// Promotions lists an event's promotional codes.
func (s *Service) Promotions(ctx context.Context, p *auth.Principal, eventID int64) ([]Promotion, error) {
	e, err := s.Authorize(ctx, p, eventID, rbac.PromotionsManage)
	if err != nil {
		return nil, err
	}
	return s.promotions(ctx, s.db, e, "")
}

// PromotionInput creates or edits a promotional code. For percentage discounts the value is
// in basis points (1500 = 15%); for fixed discounts it is in cents.
type PromotionInput struct {
	Code          string  `json:"code"`
	Description   string  `json:"description"`
	DiscountType  string  `json:"discount_type"`
	DiscountValue int64   `json:"discount_value"`
	MaxUses       *int    `json:"max_uses"`
	StartsAt      string  `json:"starts_at"`
	EndsAt        string  `json:"ends_at"`
	IsActive      bool    `json:"is_active"`
	TicketTypeIDs []int64 `json:"ticket_type_ids"`
}

var promoCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{2,39}$`)

type parsedPromotion struct {
	PromotionInput
	startsAt, endsAt *time.Time
}

func parsePromotion(in PromotionInput, e Event) (parsedPromotion, error) {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Description = strings.TrimSpace(in.Description)
	v := validate.New()
	v.Check(promoCodePattern.MatchString(in.Code), "code", "must be 3-40 letters, numbers, dashes or underscores")
	v.MaxLen("description", in.Description, 255)
	v.OneOf("discount_type", in.DiscountType, "percentage", "fixed")
	if in.DiscountType == "percentage" {
		v.Range("discount_value", in.DiscountValue, 1, 10_000)
	} else {
		v.Range("discount_value", in.DiscountValue, 1, 100_000_000)
	}
	if in.MaxUses != nil {
		v.Range("max_uses", int64(*in.MaxUses), 1, 1_000_000)
	}
	out := parsedPromotion{PromotionInput: in}
	parse := func(field, value string) *time.Time {
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
	out.startsAt, out.endsAt = parse("starts_at", in.StartsAt), parse("ends_at", in.EndsAt)
	if out.startsAt != nil && out.endsAt != nil {
		v.Check(out.endsAt.After(*out.startsAt), "ends_at", "must be after the start")
	}
	return out, v.Err()
}

func checkPromotionTicketTypes(ctx context.Context, q database.Queryer, eventID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	var n int
	args := append([]any{eventID}, database.Args(ids)...)
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_types WHERE event_id = ? AND id IN (`+database.Placeholders(len(ids))+`)`, args...).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return apperr.FieldError("ticket_type_ids", "must only contain this event's ticket types")
	}
	return nil
}

func savePromotionTicketTypes(ctx context.Context, tx *sql.Tx, promotionID int64, ids []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM promotion_ticket_types WHERE promotion_id = ?`, promotionID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO promotion_ticket_types (promotion_id, ticket_type_id) VALUES (?, ?)`, promotionID, id); err != nil {
			return err
		}
	}
	return nil
}

func uniqueInt64(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// CreatePromotion adds a promotional code to an event.
func (s *Service) CreatePromotion(ctx context.Context, p *auth.Principal, eventID int64, in PromotionInput) (Promotion, error) {
	in.TicketTypeIDs = uniqueInt64(in.TicketTypeIDs)
	var promoID int64
	var e Event
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e, err = s.lockEvent(ctx, tx, p, eventID, rbac.PromotionsManage); err != nil {
			return err
		}
		if e.Status == StatusCancelled {
			return apperr.Conflict("event_cancelled", "Promotions cannot be added to a cancelled event.")
		}
		parsed, err := parsePromotion(in, e)
		if err != nil {
			return err
		}
		if err := checkPromotionTicketTypes(ctx, tx, eventID, parsed.TicketTypeIDs); err != nil {
			return err
		}
		now := s.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO promotions (event_id, code, description, discount_type, discount_value, max_uses,
			starts_at, ends_at, is_active, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			eventID, parsed.Code, parsed.Description, parsed.DiscountType, parsed.DiscountValue, parsed.MaxUses,
			parsed.startsAt, parsed.endsAt, parsed.IsActive, p.UserID, now, now)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("code", "is already used for this event")
		}
		if err != nil {
			return err
		}
		promoID, _ = res.LastInsertId()
		if err := savePromotionTicketTypes(ctx, tx, promoID, parsed.TicketTypeIDs); err != nil {
			return err
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "promotion.created", EntityType: "promotion", EntityID: promoID, OrganiserID: e.OrganiserID,
			Summary: fmt.Sprintf("Promo code %s created for %q (%s)", parsed.Code, e.Title, describeDiscount(parsed.DiscountType, parsed.DiscountValue, e.Currency))})
	})
	if err != nil {
		return Promotion{}, err
	}
	return s.promotionByID(ctx, e, promoID)
}

func (s *Service) promotionByID(ctx context.Context, e Event, id int64) (Promotion, error) {
	list, err := s.promotions(ctx, s.db, e, "AND id = ?", id)
	if err != nil {
		return Promotion{}, err
	}
	if len(list) == 0 {
		return Promotion{}, apperr.NotFound("Promotion")
	}
	return list[0], nil
}

func (s *Service) lockPromotion(ctx context.Context, tx *sql.Tx, p *auth.Principal, id int64) (Event, Promotion, error) {
	var eventID int64
	if err := tx.QueryRowContext(ctx, `SELECT event_id FROM promotions WHERE id = ?`, id).Scan(&eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, Promotion{}, apperr.NotFound("Promotion")
		}
		return Event{}, Promotion{}, err
	}
	e, err := s.lockEvent(ctx, tx, p, eventID, rbac.PromotionsManage)
	if err != nil {
		return Event{}, Promotion{}, err
	}
	promo, err := scanPromotion(tx.QueryRowContext(ctx, selectPromotion+` WHERE id = ? FOR UPDATE`, id))
	return e, promo, err
}

// UpdatePromotion edits a promotional code.
func (s *Service) UpdatePromotion(ctx context.Context, p *auth.Principal, id int64, in PromotionInput) (Promotion, error) {
	in.TicketTypeIDs = uniqueInt64(in.TicketTypeIDs)
	var e Event
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var existing Promotion
		var err error
		if e, existing, err = s.lockPromotion(ctx, tx, p, id); err != nil {
			return err
		}
		parsed, err := parsePromotion(in, e)
		if err != nil {
			return err
		}
		if parsed.MaxUses != nil && *parsed.MaxUses < existing.TimesUsed {
			return apperr.FieldError("max_uses", fmt.Sprintf("cannot be lower than the %d times it has already been used", existing.TimesUsed))
		}
		if err := checkPromotionTicketTypes(ctx, tx, e.ID, parsed.TicketTypeIDs); err != nil {
			return err
		}
		now := s.now()
		_, err = tx.ExecContext(ctx, `UPDATE promotions SET code = ?, description = ?, discount_type = ?, discount_value = ?, max_uses = ?,
			starts_at = ?, ends_at = ?, is_active = ?, updated_at = ? WHERE id = ?`,
			parsed.Code, parsed.Description, parsed.DiscountType, parsed.DiscountValue, parsed.MaxUses, parsed.startsAt, parsed.endsAt,
			parsed.IsActive, now, id)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("code", "is already used for this event")
		}
		if err != nil {
			return err
		}
		if err := savePromotionTicketTypes(ctx, tx, id, parsed.TicketTypeIDs); err != nil {
			return err
		}
		diff := audit.Diff{}
		diff.Add("code", existing.Code, parsed.Code)
		diff.Add("discount", describeDiscount(existing.DiscountType, existing.DiscountValue, e.Currency), describeDiscount(parsed.DiscountType, parsed.DiscountValue, e.Currency))
		diff.Add("is_active", existing.IsActive, parsed.IsActive)
		return audit.Record(ctx, tx, now, audit.Entry{Action: "promotion.updated", EntityType: "promotion", EntityID: id, OrganiserID: e.OrganiserID,
			Summary: fmt.Sprintf("Promo code %s updated", parsed.Code), Changes: diff})
	})
	if err != nil {
		return Promotion{}, err
	}
	return s.promotionByID(ctx, e, id)
}

// DeletePromotion removes an unused promotional code; used codes should be deactivated.
func (s *Service) DeletePromotion(ctx context.Context, p *auth.Principal, id int64) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		e, existing, err := s.lockPromotion(ctx, tx, p, id)
		if err != nil {
			return err
		}
		var orders int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE promotion_id = ?`, id).Scan(&orders); err != nil {
			return err
		}
		if orders > 0 {
			return apperr.Conflict("promotion_used", "This code has been used on orders. Deactivate it instead of deleting it.")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM promotions WHERE id = ?`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "promotion.deleted", EntityType: "promotion", EntityID: id, OrganiserID: e.OrganiserID,
			Summary: fmt.Sprintf("Promo code %s deleted from %q", existing.Code, e.Title)})
	})
}

func describeDiscount(kind string, value int64, currency string) string {
	if kind == "percentage" {
		return fmt.Sprintf("%d.%02d%% off", value/100, value%100)
	}
	return formatMoney(value, currency) + " off"
}
