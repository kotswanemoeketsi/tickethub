package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/slug"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// CancellationHook reacts to a cancelled event: the sales module refunds paid orders and
// releases pending reservations. It is an interface so this package does not depend on sales.
type CancellationHook interface {
	EventCancelled(ctx context.Context, eventID int64, reason string) error
}

// Config holds event defaults.
type Config struct {
	DefaultCurrency string
	DefaultTimezone string
}

// Service implements event management.
type Service struct {
	db     *database.DB
	cache  cache.Cache
	cfg    Config
	now    clock.Func
	logger *slog.Logger
	hook   CancellationHook
}

// NewService wires the events service.
func NewService(db *database.DB, c cache.Cache, cfg Config, now clock.Func, logger *slog.Logger) *Service {
	return &Service{db: db, cache: c, cfg: cfg, now: now, logger: logger}
}

// SetCancellationHook registers the handler invoked after an event is cancelled.
func (s *Service) SetCancellationHook(h CancellationHook) { s.hook = h }

const selectEvent = `SELECT e.id, e.organiser_id, o.name, e.category_id, COALESCE(c.name, ''),
	v.id, COALESCE(v.name, ''), COALESCE(v.address_line, ''), COALESCE(v.city, ''), COALESCE(v.province, ''), COALESCE(v.country, ''),
	e.title, e.slug, e.summary, e.description, e.image_url, e.starts_at, e.ends_at, e.timezone, e.currency, e.capacity,
	e.status, e.allow_reentry, e.refunds_allowed, e.refund_cutoff_hours, e.review_notes,
	e.submitted_at, e.reviewed_at, e.published_at, e.cancelled_at, e.cancellation_reason, e.created_at, e.updated_at,
	(SELECT COALESCE(SUM(tt.quantity_sold), 0) FROM ticket_types tt WHERE tt.event_id = e.id),
	(SELECT COALESCE(SUM(tt.quantity_reserved), 0) FROM ticket_types tt WHERE tt.event_id = e.id),
	(SELECT COUNT(*) FROM ticket_types tt WHERE tt.event_id = e.id),
	(SELECT COUNT(*) FROM tickets t WHERE t.event_id = e.id AND t.checkin_state <> 'not_checked_in'),
	(SELECT COUNT(*) FROM orders x WHERE x.event_id = e.id AND x.status IN ('paid', 'partially_refunded')),
	(SELECT COALESCE(SUM(x.total_cents - x.refunded_cents), 0) FROM orders x WHERE x.event_id = e.id AND x.status IN ('paid', 'partially_refunded', 'refunded'))
	FROM events e
	JOIN organisers o ON o.id = e.organiser_id
	LEFT JOIN event_categories c ON c.id = e.category_id
	LEFT JOIN venues v ON v.id = e.venue_id`

func (s *Service) scanEvent(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var venueID *int64
	var venue VenueSummary
	err := row.Scan(&e.ID, &e.OrganiserID, &e.OrganiserName, &e.CategoryID, &e.CategoryName,
		&venueID, &venue.Name, &venue.AddressLine, &venue.City, &venue.Province, &venue.Country,
		&e.Title, &e.Slug, &e.Summary, &e.Description, &e.ImageURL, &e.StartsAt, &e.EndsAt, &e.Timezone, &e.Currency, &e.Capacity,
		&e.Status, &e.AllowReentry, &e.RefundsAllowed, &e.RefundCutoffHours, &e.ReviewNotes,
		&e.SubmittedAt, &e.ReviewedAt, &e.PublishedAt, &e.CancelledAt, &e.CancellationReason, &e.CreatedAt, &e.UpdatedAt,
		&e.Stats.TicketsSold, &e.Stats.TicketsReserved, &e.Stats.TicketTypesActive, &e.Stats.CheckedIn, &e.Stats.OrdersPaid, &e.Stats.NetRevenueCents)
	if err != nil {
		return e, err
	}
	if venueID != nil {
		venue.ID = *venueID
		e.Venue = &venue
	}
	e.Phase = Phase(e.Status, e.StartsAt, e.EndsAt, s.now())
	e.StartsAtLocal = localInput(e.StartsAt, e.Timezone)
	e.EndsAtLocal = localInput(e.EndsAt, e.Timezone)
	e.Stats.TicketsAvailable = max(0, e.Capacity-e.Stats.TicketsSold-e.Stats.TicketsReserved)
	return e, nil
}

func (s *Service) load(ctx context.Context, q database.Queryer, id int64) (Event, error) {
	e, err := s.scanEvent(q.QueryRowContext(ctx, selectEvent+` WHERE e.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, apperr.NotFound("Event")
	}
	return e, err
}

// Authorize loads an event and checks perm against its organiser. Events outside the
// caller's scope are reported as not found.
func (s *Service) Authorize(ctx context.Context, p *auth.Principal, id int64, perm string) (Event, error) {
	e, err := s.load(ctx, s.db, id)
	if err != nil {
		return e, err
	}
	if !p.CanFor(e.OrganiserID, perm) {
		if p.CanFor(e.OrganiserID, rbac.EventsView) {
			return Event{}, auth.ErrForbidden()
		}
		return Event{}, apperr.NotFound("Event")
	}
	return e, nil
}

// lockEvent takes a row lock on the event and checks perm. Every inventory-changing
// transaction locks the event row first, giving all of them the same lock order.
func (s *Service) lockEvent(ctx context.Context, tx *sql.Tx, p *auth.Principal, id int64, perm string) (Event, error) {
	var organiserID int64
	err := tx.QueryRowContext(ctx, `SELECT organiser_id FROM events WHERE id = ? FOR UPDATE`, id).Scan(&organiserID)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, apperr.NotFound("Event")
	}
	if err != nil {
		return Event{}, err
	}
	if !p.CanFor(organiserID, perm) {
		return Event{}, apperr.NotFound("Event")
	}
	return s.load(ctx, tx, id)
}

// Filter narrows the admin event list.
type Filter struct {
	Status      string // stored status, or a phase: upcoming, live, completed
	OrganiserID int64
	CategoryID  int64
	Query       string
}

// List returns events in the caller's scope.
func (s *Service) List(ctx context.Context, p *auth.Principal, f Filter, page httpx.Page) ([]Event, int64, error) {
	scope := p.Scope(rbac.EventsView).Narrow(f.OrganiserID)
	scopeSQL, args := scope.SQL("e.organiser_id")
	where := []string{scopeSQL}
	now := s.now()
	switch f.Status {
	case "":
	case PhaseUpcoming:
		where = append(where, "e.status = 'published' AND e.starts_at > ?")
		args = append(args, now)
	case PhaseLive:
		where = append(where, "e.status = 'published' AND e.starts_at <= ? AND e.ends_at > ?")
		args = append(args, now, now)
	case PhaseCompleted:
		where = append(where, "e.status = 'published' AND e.ends_at <= ?")
		args = append(args, now)
	default:
		where = append(where, "e.status = ?")
		args = append(args, f.Status)
	}
	if f.CategoryID > 0 {
		where = append(where, "e.category_id = ?")
		args = append(args, f.CategoryID)
	}
	if f.Query != "" {
		where = append(where, "(e.title LIKE ? OR o.name LIKE ?)")
		like := database.EscapeLike(f.Query)
		args = append(args, like, like)
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events e JOIN organisers o ON o.id = e.organiser_id`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, selectEvent+clause+` ORDER BY e.starts_at DESC, e.id DESC LIMIT ? OFFSET ?`, append(args, page.Size, page.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := s.scanEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// Input creates or edits an event. Dates are wall-clock times in Timezone.
type Input struct {
	OrganiserID       int64  `json:"organiser_id"`
	CategoryID        int64  `json:"category_id"`
	VenueID           int64  `json:"venue_id"`
	Title             string `json:"title"`
	Summary           string `json:"summary"`
	Description       string `json:"description"`
	ImageURL          string `json:"image_url"`
	StartsAt          string `json:"starts_at"`
	EndsAt            string `json:"ends_at"`
	Timezone          string `json:"timezone"`
	Capacity          int    `json:"capacity"`
	AllowReentry      bool   `json:"allow_reentry"`
	RefundsAllowed    bool   `json:"refunds_allowed"`
	RefundCutoffHours int    `json:"refund_cutoff_hours"`
}

type parsedInput struct {
	Input
	startsAt, endsAt time.Time
}

func (s *Service) parse(in Input) (parsedInput, error) {
	in.Title, in.Summary, in.Description, in.ImageURL = strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary), strings.TrimSpace(in.Description), strings.TrimSpace(in.ImageURL)
	if in.Timezone == "" {
		in.Timezone = s.cfg.DefaultTimezone
	}
	v := validate.New()
	v.Required("title", in.Title)
	v.MaxLen("title", in.Title, 200)
	v.MaxLen("summary", in.Summary, 300)
	v.Required("description", in.Description)
	v.MaxLen("description", in.Description, 20_000)
	v.MaxLen("image_url", in.ImageURL, 500)
	v.Check(in.ImageURL == "" || strings.HasPrefix(in.ImageURL, "/uploads/") || strings.HasPrefix(in.ImageURL, "https://"), "image_url", "must be an uploaded image or an https URL")
	v.Range("capacity", int64(in.Capacity), 1, 1_000_000)
	v.Range("refund_cutoff_hours", int64(in.RefundCutoffHours), 0, 24*90)
	out := parsedInput{Input: in}
	var err error
	if out.startsAt, err = validate.ParseLocalDateTime(in.StartsAt, in.Timezone); err != nil {
		v.Add("starts_at", "must be a valid date and time")
	}
	if out.endsAt, err = validate.ParseLocalDateTime(in.EndsAt, in.Timezone); err != nil {
		v.Add("ends_at", "must be a valid date and time")
	}
	if !v.Has("starts_at") && !v.Has("ends_at") {
		v.Check(out.endsAt.After(out.startsAt), "ends_at", "must be after the start time")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		v.Add("timezone", "is not a recognised time zone")
	}
	return out, v.Err()
}

// checkReferences verifies the category exists and the venue is usable by the organiser.
func checkReferences(ctx context.Context, q database.Queryer, organiserID, categoryID, venueID int64) error {
	v := validate.New()
	if categoryID > 0 {
		var one int
		if err := q.QueryRowContext(ctx, `SELECT 1 FROM event_categories WHERE id = ?`, categoryID).Scan(&one); err != nil {
			v.Add("category_id", "is not a valid category")
		}
	}
	if venueID > 0 {
		var one int
		if err := q.QueryRowContext(ctx, `SELECT 1 FROM venues WHERE id = ? AND (organiser_id IS NULL OR organiser_id = ?)`, venueID, organiserID).Scan(&one); err != nil {
			v.Add("venue_id", "is not a venue this organiser can use")
		}
	}
	return v.Err()
}

// Create adds a draft event for an organiser the caller may create events for.
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (Event, error) {
	parsed, err := s.parse(in)
	if err != nil {
		return Event{}, err
	}
	if parsed.OrganiserID == 0 {
		return Event{}, apperr.FieldError("organiser_id", "is required")
	}
	if !p.CanFor(parsed.OrganiserID, rbac.EventsCreate) {
		return Event{}, auth.ErrForbidden()
	}
	if !parsed.startsAt.After(s.now()) {
		return Event{}, apperr.FieldError("starts_at", "must be in the future")
	}
	var id int64
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM organisers WHERE id = ?`, parsed.OrganiserID).Scan(&status); err != nil {
			return apperr.FieldError("organiser_id", "is not a valid organiser")
		}
		if status != "active" {
			return apperr.Conflict("organiser_inactive", "Events can only be created for active organisers.")
		}
		if err := checkReferences(ctx, tx, parsed.OrganiserID, parsed.CategoryID, parsed.VenueID); err != nil {
			return err
		}
		eventSlug, err := slug.Unique(ctx, tx, "events", "slug", slug.Make(parsed.Title))
		if err != nil {
			return err
		}
		now := s.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO events (organiser_id, category_id, venue_id, title, slug, summary, description, image_url,
			starts_at, ends_at, timezone, currency, capacity, status, allow_reentry, refunds_allowed, refund_cutoff_hours, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'draft', ?, ?, ?, ?, ?, ?)`,
			parsed.OrganiserID, database.NullIfZero(parsed.CategoryID), database.NullIfZero(parsed.VenueID), parsed.Title, eventSlug,
			parsed.Summary, parsed.Description, parsed.ImageURL, parsed.startsAt, parsed.endsAt, parsed.Timezone, s.cfg.DefaultCurrency,
			parsed.Capacity, parsed.AllowReentry, parsed.RefundsAllowed, parsed.RefundCutoffHours, p.UserID, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return audit.Record(ctx, tx, now, audit.Entry{Action: "event.created", EntityType: "event", EntityID: id, OrganiserID: parsed.OrganiserID,
			Summary: fmt.Sprintf("Event %q created as a draft", parsed.Title)})
	})
	if err != nil {
		return Event{}, err
	}
	return s.load(ctx, s.db, id)
}

// Update edits an event. Events under review must be withdrawn first; cancelled events are
// read-only; capacity can never drop below the tickets already sold or reserved.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id int64, in Input) (Event, error) {
	parsed, err := s.parse(in)
	if err != nil {
		return Event{}, err
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := s.lockEvent(ctx, tx, p, id, rbac.EventsUpdate)
		if err != nil {
			return err
		}
		switch existing.Status {
		case StatusCancelled:
			return apperr.Conflict("event_cancelled", "Cancelled events cannot be edited.")
		case StatusPendingReview:
			return apperr.Conflict("event_under_review", "This event is awaiting review. Withdraw it to make changes.")
		}
		if existing.Phase == PhaseCompleted {
			return apperr.Conflict("event_completed", "Completed events cannot be edited.")
		}
		if committed := existing.Stats.TicketsSold + existing.Stats.TicketsReserved; parsed.Capacity < committed {
			return apperr.FieldError("capacity", fmt.Sprintf("cannot be lower than the %d tickets already sold or reserved", committed))
		}
		if err := checkReferences(ctx, tx, existing.OrganiserID, parsed.CategoryID, parsed.VenueID); err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE events SET category_id = ?, venue_id = ?, title = ?, summary = ?, description = ?, image_url = ?,
			starts_at = ?, ends_at = ?, timezone = ?, capacity = ?, allow_reentry = ?, refunds_allowed = ?, refund_cutoff_hours = ?, updated_at = ?
			WHERE id = ?`,
			database.NullIfZero(parsed.CategoryID), database.NullIfZero(parsed.VenueID), parsed.Title, parsed.Summary, parsed.Description,
			parsed.ImageURL, parsed.startsAt, parsed.endsAt, parsed.Timezone, parsed.Capacity, parsed.AllowReentry, parsed.RefundsAllowed,
			parsed.RefundCutoffHours, now, id); err != nil {
			return err
		}
		diff := audit.Diff{}
		diff.Add("title", existing.Title, parsed.Title)
		diff.Add("starts_at", existing.StartsAt.Format(time.RFC3339), parsed.startsAt.Format(time.RFC3339))
		diff.Add("ends_at", existing.EndsAt.Format(time.RFC3339), parsed.endsAt.Format(time.RFC3339))
		diff.Add("capacity", existing.Capacity, parsed.Capacity)
		diff.Add("timezone", existing.Timezone, parsed.Timezone)
		diff.Add("allow_reentry", existing.AllowReentry, parsed.AllowReentry)
		diff.Add("refunds_allowed", existing.RefundsAllowed, parsed.RefundsAllowed)
		var venueID int64
		if existing.Venue != nil {
			venueID = existing.Venue.ID
		}
		diff.Add("venue_id", venueID, parsed.VenueID)
		if existing.Description != parsed.Description || existing.Summary != parsed.Summary || existing.ImageURL != parsed.ImageURL {
			diff["content"] = audit.Change{From: "(previous)", To: "(updated)"}
		}
		if len(diff) == 0 {
			return nil
		}
		return audit.Record(ctx, tx, now, audit.Entry{Action: "event.updated", EntityType: "event", EntityID: id, OrganiserID: existing.OrganiserID,
			Summary: "Updated " + diff.Fields(), Changes: diff})
	})
	if err != nil {
		return Event{}, err
	}
	s.invalidatePublic(ctx)
	return s.load(ctx, s.db, id)
}

// Delete removes a draft or rejected event that has never had an order.
func (s *Service) Delete(ctx context.Context, p *auth.Principal, id int64) error {
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := s.lockEvent(ctx, tx, p, id, rbac.EventsDelete)
		if err != nil {
			return err
		}
		if existing.Status != StatusDraft && existing.Status != StatusRejected {
			return apperr.Conflict("event_not_deletable", "Only draft or rejected events can be deleted. Cancel the event instead.")
		}
		var orders int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE event_id = ?`, id).Scan(&orders); err != nil {
			return err
		}
		if orders > 0 {
			return apperr.Conflict("event_has_orders", "This event has orders and cannot be deleted. Cancel it instead.")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, s.now(), audit.Entry{Action: "event.deleted", EntityType: "event", EntityID: id, OrganiserID: existing.OrganiserID,
			Summary: fmt.Sprintf("Event %q deleted", existing.Title)})
	})
	if err == nil {
		s.invalidatePublic(ctx)
	}
	return err
}

// transition moves an event between statuses inside a transaction.
func (s *Service) transition(ctx context.Context, p *auth.Principal, id int64, perm string, apply func(tx *sql.Tx, e Event, now time.Time) (audit.Entry, error)) (Event, error) {
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		e, err := s.lockEvent(ctx, tx, p, id, perm)
		if err != nil {
			return err
		}
		now := s.now()
		entry, err := apply(tx, e, now)
		if err != nil {
			return err
		}
		entry.EntityType, entry.EntityID, entry.OrganiserID = "event", id, e.OrganiserID
		return audit.Record(ctx, tx, now, entry)
	})
	if err != nil {
		return Event{}, err
	}
	s.invalidatePublic(ctx)
	return s.load(ctx, s.db, id)
}

// Submit sends a draft or rejected event for administrator review.
func (s *Service) Submit(ctx context.Context, p *auth.Principal, id int64) (Event, error) {
	return s.transition(ctx, p, id, rbac.EventsUpdate, func(tx *sql.Tx, e Event, now time.Time) (audit.Entry, error) {
		if e.Status != StatusDraft && e.Status != StatusRejected {
			return audit.Entry{}, apperr.Conflict("invalid_status", "Only draft or rejected events can be submitted for review.")
		}
		v := validate.New()
		v.Check(e.Venue != nil, "venue_id", "is required before submitting for review")
		v.Check(e.StartsAt.After(now), "starts_at", "must be in the future")
		var sellable int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_types WHERE event_id = ? AND is_hidden = FALSE`, e.ID).Scan(&sellable); err != nil {
			return audit.Entry{}, err
		}
		v.Check(sellable > 0, "ticket_types", "add at least one ticket type that is on sale before submitting")
		if err := v.Err(); err != nil {
			return audit.Entry{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET status = 'pending_review', submitted_at = ?, updated_at = ? WHERE id = ?`, now, now, e.ID); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Action: "event.submitted", Summary: fmt.Sprintf("Event %q submitted for review", e.Title)}, nil
	})
}

// Withdraw returns an event under review to draft.
func (s *Service) Withdraw(ctx context.Context, p *auth.Principal, id int64) (Event, error) {
	return s.transition(ctx, p, id, rbac.EventsUpdate, func(tx *sql.Tx, e Event, now time.Time) (audit.Entry, error) {
		if e.Status != StatusPendingReview {
			return audit.Entry{}, apperr.Conflict("invalid_status", "Only events awaiting review can be withdrawn.")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET status = 'draft', updated_at = ? WHERE id = ?`, now, e.ID); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Action: "event.withdrawn", Summary: fmt.Sprintf("Event %q withdrawn from review", e.Title)}, nil
	})
}

// Approve publishes an event under review to the marketplace.
func (s *Service) Approve(ctx context.Context, p *auth.Principal, id int64, notes string) (Event, error) {
	return s.transition(ctx, p, id, rbac.EventsReview, func(tx *sql.Tx, e Event, now time.Time) (audit.Entry, error) {
		if e.Status != StatusPendingReview {
			return audit.Entry{}, apperr.Conflict("invalid_status", "Only events awaiting review can be approved.")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET status = 'published', review_notes = ?, reviewed_at = ?, reviewed_by = ?, published_at = ?, updated_at = ? WHERE id = ?`,
			truncate(notes, 1000), now, p.UserID, now, now, e.ID); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Action: "event.published", Summary: fmt.Sprintf("Event %q approved and published", e.Title)}, nil
	})
}

// Reject returns an event to the organiser with review notes.
func (s *Service) Reject(ctx context.Context, p *auth.Principal, id int64, notes string) (Event, error) {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return Event{}, apperr.FieldError("notes", "explain what the organiser needs to change")
	}
	return s.transition(ctx, p, id, rbac.EventsReview, func(tx *sql.Tx, e Event, now time.Time) (audit.Entry, error) {
		if e.Status != StatusPendingReview {
			return audit.Entry{}, apperr.Conflict("invalid_status", "Only events awaiting review can be rejected.")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET status = 'rejected', review_notes = ?, reviewed_at = ?, reviewed_by = ?, updated_at = ? WHERE id = ?`,
			truncate(notes, 1000), now, p.UserID, now, e.ID); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Action: "event.rejected", Summary: fmt.Sprintf("Event %q rejected: %s", e.Title, truncate(notes, 200))}, nil
	})
}

// Cancel cancels an event. Sales stop immediately; the cancellation hook then queues refunds
// for paid orders and releases pending reservations.
func (s *Service) Cancel(ctx context.Context, p *auth.Principal, id int64, reason string) (Event, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Event{}, apperr.FieldError("reason", "is required so attendees know why")
	}
	e, err := s.transition(ctx, p, id, rbac.EventsCancel, func(tx *sql.Tx, e Event, now time.Time) (audit.Entry, error) {
		if e.Status == StatusCancelled {
			return audit.Entry{}, apperr.Conflict("invalid_status", "This event is already cancelled.")
		}
		if e.Phase == PhaseCompleted {
			return audit.Entry{}, apperr.Conflict("event_completed", "Completed events cannot be cancelled.")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET status = 'cancelled', cancelled_at = ?, cancellation_reason = ?, updated_at = ? WHERE id = ?`,
			now, truncate(reason, 500), now, e.ID); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Action: "event.cancelled", Summary: fmt.Sprintf("Event %q cancelled: %s", e.Title, truncate(reason, 200))}, nil
	})
	if err != nil {
		return Event{}, err
	}
	if s.hook != nil {
		if err := s.hook.EventCancelled(ctx, id, reason); err != nil {
			// The event is cancelled either way; refunds can be raised again from the
			// refunds screen, so report the problem rather than failing the request.
			s.logger.Error("process event cancellation", "event_id", id, "error", err)
		}
	}
	return s.load(ctx, s.db, e.ID)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
