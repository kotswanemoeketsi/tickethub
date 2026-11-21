package events

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Public catalogue responses are cached briefly. Availability may therefore lag by a few
// seconds, which is safe: the reservation step is the authority on what can be bought.
const (
	publicVersionKey = "public:version"
	publicListTTL    = 20 * time.Second
	publicDetailTTL  = 10 * time.Second
)

// PublicEvent is an event card on the marketplace.
type PublicEvent struct {
	ID             int64     `json:"id"`
	Slug           string    `json:"slug"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	ImageURL       string    `json:"image_url"`
	StartsAt       time.Time `json:"starts_at"`
	EndsAt         time.Time `json:"ends_at"`
	Timezone       string    `json:"timezone"`
	Currency       string    `json:"currency"`
	OrganiserName  string    `json:"organiser_name"`
	CategoryName   string    `json:"category_name"`
	CategorySlug   string    `json:"category_slug"`
	VenueName      string    `json:"venue_name"`
	VenueCity      string    `json:"venue_city"`
	FromPriceCents *int64    `json:"from_price_cents"`
	SoldOut        bool      `json:"sold_out"`
	// SellingFast marks events where most tickets are already sold or held in checkouts.
	SellingFast bool   `json:"selling_fast"`
	Phase       string `json:"phase"`
	// DistanceKM is set when events are searched by distance from the buyer.
	DistanceKM *float64 `json:"distance_km,omitempty"`
}

// PublicTicketType is a ticket option shown to buyers.
type PublicTicketType struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Category     string     `json:"category"`
	PriceCents   int64      `json:"price_cents"`
	Available    int        `json:"available"`
	MinPerOrder  int        `json:"min_per_order"`
	MaxPerOrder  int        `json:"max_per_order"`
	SaleStatus   string     `json:"sale_status"`
	SalesStartAt *time.Time `json:"sales_start_at"`
	SalesEndAt   *time.Time `json:"sales_end_at"`
}

// PublicEventDetail is the event page.
type PublicEventDetail struct {
	PublicEvent
	Description       string             `json:"description"`
	Venue             *VenueSummary      `json:"venue"`
	RefundsAllowed    bool               `json:"refunds_allowed"`
	RefundCutoffHours int                `json:"refund_cutoff_hours"`
	TicketTypes       []PublicTicketType `json:"ticket_types"`
}

// PublicFilter narrows the marketplace listing.
type PublicFilter struct {
	Query    string
	Category string
	City     string
	Province string
	// MaxPriceCents keeps events whose cheapest ticket costs at most this; 0 for any price.
	MaxPriceCents int64
	From          *time.Time
	To            *time.Time
	// Sort is "date" (soonest first, the default), "popular" (most sold first) or
	// "price" (cheapest first).
	Sort string
}

func (f PublicFilter) cacheKey(page httpx.Page) string {
	parts := fmt.Sprintf("%s|%s|%s|%s|%d|%v|%v|%s|%d|%d", f.Query, f.Category, f.City, f.Province, f.MaxPriceCents, f.From, f.To, f.Sort, page.Number, page.Size)
	sum := sha256.Sum256([]byte(strings.ToLower(parts)))
	return hex.EncodeToString(sum[:12])
}

func (s *Service) publicVersion(ctx context.Context) string {
	if raw, ok, err := s.cache.Get(ctx, publicVersionKey); err == nil && ok {
		return string(raw)
	}
	return "0"
}

// invalidatePublic makes every cached marketplace response stale at once.
func (s *Service) invalidatePublic(ctx context.Context) {
	if _, err := s.cache.Incr(ctx, publicVersionKey, 0); err != nil {
		s.logger.Warn("invalidate public cache", "error", err)
	}
}

type publicPage struct {
	Items []PublicEvent `json:"items"`
	Total int64         `json:"total"`
}

const publicEventColumns = `e.id, e.slug, e.title, e.summary, e.image_url, e.starts_at, e.ends_at, e.timezone, e.currency, o.name,
	COALESCE(c.name, ''), COALESCE(c.slug, ''), COALESCE(v.name, ''), COALESCE(v.city, ''),
	(SELECT MIN(tt.price_cents) FROM ticket_types tt WHERE tt.event_id = e.id AND tt.is_hidden = FALSE),
	e.capacity,
	(SELECT COALESCE(SUM(tt.quantity_sold + tt.quantity_reserved), 0) FROM ticket_types tt WHERE tt.event_id = e.id),
	(SELECT COALESCE(SUM(tt.quantity_total), 0) FROM ticket_types tt WHERE tt.event_id = e.id AND tt.is_hidden = FALSE)`

const publicEventJoins = `
	FROM events e
	JOIN organisers o ON o.id = e.organiser_id
	LEFT JOIN event_categories c ON c.id = e.category_id
	LEFT JOIN venues v ON v.id = e.venue_id`

const selectPublicEvent = `SELECT ` + publicEventColumns + publicEventJoins

// publicOnSale limits a query to events buyers can still go to.
const publicOnSale = `e.status = 'published' AND e.ends_at > ? AND o.status = 'active'`

// scanPublic reads the publicEventColumns, followed by any extra columns into extra.
func (s *Service) scanPublic(row interface{ Scan(...any) error }, extra ...any) (PublicEvent, error) {
	var e PublicEvent
	var capacity, committed, allocated int
	dest := append([]any{&e.ID, &e.Slug, &e.Title, &e.Summary, &e.ImageURL, &e.StartsAt, &e.EndsAt, &e.Timezone, &e.Currency,
		&e.OrganiserName, &e.CategoryName, &e.CategorySlug, &e.VenueName, &e.VenueCity, &e.FromPriceCents, &capacity, &committed, &allocated}, extra...)
	err := row.Scan(dest...)
	e.SoldOut = committed >= capacity || committed >= allocated
	limit := min(capacity, allocated)
	e.SellingFast = !e.SoldOut && limit > 0 && committed*10 >= limit*6
	e.Phase = Phase(StatusPublished, e.StartsAt, e.EndsAt, s.now())
	return e, err
}

// ListPublic returns published, not-yet-finished events of active organisers.
func (s *Service) ListPublic(ctx context.Context, f PublicFilter, page httpx.Page) ([]PublicEvent, int64, error) {
	key := "public:v" + s.publicVersion(ctx) + ":list:" + f.cacheKey(page)
	var cached publicPage
	if hit, _ := cache.GetJSON(ctx, s.cache, key, &cached); hit {
		return cached.Items, cached.Total, nil
	}

	where := []string{publicOnSale}
	args := []any{s.now()}
	if f.Query != "" {
		where = append(where, "(e.title LIKE ? OR e.summary LIKE ? OR v.name LIKE ? OR o.name LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like, like, like)
	}
	if f.Category != "" {
		where = append(where, "c.slug = ?")
		args = append(args, f.Category)
	}
	if f.City != "" {
		where = append(where, "v.city = ?")
		args = append(args, f.City)
	}
	if f.Province != "" {
		where = append(where, "v.province = ?")
		args = append(args, f.Province)
	}
	if f.MaxPriceCents > 0 {
		where = append(where, "(SELECT MIN(tt.price_cents) FROM ticket_types tt WHERE tt.event_id = e.id AND tt.is_hidden = FALSE) <= ?")
		args = append(args, f.MaxPriceCents)
	}
	if f.From != nil {
		where = append(where, "e.ends_at >= ?")
		args = append(args, *f.From)
	}
	if f.To != nil {
		where = append(where, "e.starts_at < ?")
		args = append(args, *f.To)
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+publicEventJoins+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := " ORDER BY e.starts_at, e.id"
	switch f.Sort {
	case "popular":
		order = ` ORDER BY (SELECT COALESCE(SUM(tt.quantity_sold), 0) FROM ticket_types tt WHERE tt.event_id = e.id) / GREATEST(e.capacity, 1) DESC, e.starts_at, e.id`
	case "price":
		order = ` ORDER BY (SELECT MIN(tt.price_cents) FROM ticket_types tt WHERE tt.event_id = e.id AND tt.is_hidden = FALSE) IS NULL,
			(SELECT MIN(tt.price_cents) FROM ticket_types tt WHERE tt.event_id = e.id AND tt.is_hidden = FALSE), e.starts_at, e.id`
	}
	rows, err := s.db.QueryContext(ctx, selectPublicEvent+clause+order+` LIMIT ? OFFSET ?`, append(args, page.Size, page.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []PublicEvent{}
	for rows.Next() {
		e, err := s.scanPublic(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	_ = cache.SetJSON(ctx, s.cache, key, publicPage{Items: items, Total: total}, publicListTTL)
	return items, total, nil
}

// GetPublic returns the public page of an event by slug.
func (s *Service) GetPublic(ctx context.Context, eventSlug string) (PublicEventDetail, error) {
	key := "public:v" + s.publicVersion(ctx) + ":event:" + eventSlug
	var cached PublicEventDetail
	if hit, _ := cache.GetJSON(ctx, s.cache, key, &cached); hit {
		return cached, nil
	}

	// Cancelled events stay reachable by link so ticket holders can see the notice.
	var eventID int64
	err := s.db.QueryRowContext(ctx, `SELECT e.id FROM events e JOIN organisers o ON o.id = e.organiser_id
		WHERE e.slug = ? AND e.status IN ('published', 'cancelled') AND o.status = 'active'`, eventSlug).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicEventDetail{}, apperr.NotFound("Event")
	}
	if err != nil {
		return PublicEventDetail{}, err
	}
	full, err := s.load(ctx, s.db, eventID)
	if err != nil {
		return PublicEventDetail{}, err
	}
	card, err := s.scanPublic(s.db.QueryRowContext(ctx, selectPublicEvent+` WHERE e.id = ?`, eventID))
	if err != nil {
		return PublicEventDetail{}, err
	}
	if full.Status == StatusCancelled {
		card.Phase = StatusCancelled
	}
	detail := PublicEventDetail{
		PublicEvent: card, Description: full.Description, Venue: full.Venue,
		RefundsAllowed: full.RefundsAllowed, RefundCutoffHours: full.RefundCutoffHours, TicketTypes: []PublicTicketType{},
	}
	types, err := s.ticketTypes(ctx, s.db, full)
	if err != nil {
		return PublicEventDetail{}, err
	}
	for _, t := range types {
		if t.IsHidden {
			continue
		}
		status := t.SaleStatus
		if full.Status == StatusCancelled {
			status = SaleEnded
		}
		detail.TicketTypes = append(detail.TicketTypes, PublicTicketType{
			ID: t.ID, Name: t.Name, Description: t.Description, Category: t.Category, PriceCents: t.PriceCents,
			Available: t.QuantityAvailable, MinPerOrder: t.MinPerOrder, MaxPerOrder: t.MaxPerOrder, SaleStatus: status,
			SalesStartAt: t.SalesStartAt, SalesEndAt: t.SalesEndAt,
		})
	}
	_ = cache.SetJSON(ctx, s.cache, key, detail, publicDetailTTL)
	return detail, nil
}
