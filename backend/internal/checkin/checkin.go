// Package checkin validates tickets at the door and records attendance.
package checkin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
)

// Rejection reasons returned to the scanner.
const (
	ReasonInvalidCode    = "invalid_code"
	ReasonNotFound       = "not_found"
	ReasonWrongEvent     = "wrong_event"
	ReasonNotPaid        = "not_paid"
	ReasonTicketVoid     = "ticket_void"
	ReasonRefunded       = "refunded"
	ReasonEventCancelled = "event_cancelled"
	ReasonTooEarly       = "too_early"
	ReasonEventEnded     = "event_ended"
	ReasonAlreadyInside  = "already_checked_in"
	ReasonAlreadyUsed    = "already_used"
	ReasonNotCheckedIn   = "not_checked_in"
	ReasonRevokedQRCode  = "qr_revoked"
)

// Scan results and directions.
const (
	ResultAccepted = "accepted"
	ResultRejected = "rejected"
	DirectionIn    = "in"
	DirectionOut   = "out"
)

const (
	stateCheckedIn        = "checked_in"
	stateCheckedOut       = "checked_out"
	maxScanPayloadLength  = 200
	recentScansDefaultMax = 25
)

var messages = map[string]string{
	ReasonInvalidCode:    "This is not a valid ticket QR code.",
	ReasonNotFound:       "Ticket not found.",
	ReasonWrongEvent:     "This ticket is for a different event.",
	ReasonNotPaid:        "This ticket has not been paid for.",
	ReasonTicketVoid:     "This ticket has been cancelled.",
	ReasonRefunded:       "This ticket was refunded.",
	ReasonEventCancelled: "This event has been cancelled.",
	ReasonTooEarly:       "Check-in has not opened yet for this ticket.",
	ReasonEventEnded:     "Check-in has closed for this event.",
	ReasonAlreadyInside:  "Already checked in.",
	ReasonAlreadyUsed:    "This ticket has already been used and re-entry is not allowed.",
	ReasonNotCheckedIn:   "This ticket is not checked in, so it cannot be checked out.",
	ReasonRevokedQRCode:  "This QR code was replaced when the ticket was transferred. Ask for the latest ticket.",
}

// Config holds the check-in window settings.
type Config struct {
	OpensBefore    time.Duration // how long before the start doors may open
	ClosesAfterEnd time.Duration // grace period after the scheduled end
}

// Service implements ticket scanning.
type Service struct {
	db     *database.DB
	signer *tickets.Signer
	cfg    Config
	now    clock.Func
}

// NewService wires the check-in service.
func NewService(db *database.DB, signer *tickets.Signer, cfg Config, now clock.Func) *Service {
	return &Service{db: db, signer: signer, cfg: cfg, now: now}
}

// ScanInput is one scan from a device.
type ScanInput struct {
	EventID   int64  `json:"event_id"`
	Code      string `json:"code"` // QR payload or a typed ticket code
	Direction string `json:"direction"`
	Gate      string `json:"gate"`
}

// TicketInfo is what the door staff see about a scanned ticket.
type TicketInfo struct {
	ID               int64      `json:"id"`
	DisplayCode      string     `json:"display_code"`
	HolderName       string     `json:"holder_name"`
	TicketTypeName   string     `json:"ticket_type_name"`
	OrderReference   string     `json:"order_reference"`
	CheckinState     string     `json:"checkin_state"`
	FirstCheckedInAt *time.Time `json:"first_checked_in_at"`
	IsComplimentary  bool       `json:"is_complimentary"`
	EventTitle       string     `json:"event_title,omitempty"`
}

// ScanResult is returned for every scan, accepted or not.
type ScanResult struct {
	Result    string      `json:"result"`
	Reason    string      `json:"reason,omitempty"`
	Message   string      `json:"message"`
	Direction string      `json:"direction"`
	Ticket    *TicketInfo `json:"ticket,omitempty"`
	Stats     Stats       `json:"stats"`
	ScannedAt time.Time   `json:"scanned_at"`
}

type scanEvent struct {
	ID           int64
	OrganiserID  int64
	Title        string
	Status       string
	StartsAt     time.Time
	EndsAt       time.Time
	Timezone     string
	AllowReentry bool
}

// loadEvent loads an event the caller holds any of perms for.
func (s *Service) loadEvent(ctx context.Context, p *auth.Principal, id int64, perms ...string) (scanEvent, error) {
	var e scanEvent
	err := s.db.QueryRowContext(ctx, `SELECT id, organiser_id, title, status, starts_at, ends_at, timezone, allow_reentry FROM events WHERE id = ?`, id).
		Scan(&e.ID, &e.OrganiserID, &e.Title, &e.Status, &e.StartsAt, &e.EndsAt, &e.Timezone, &e.AllowReentry)
	if errors.Is(err, sql.ErrNoRows) {
		return e, apperr.NotFound("Event")
	}
	if err != nil {
		return e, err
	}
	for _, perm := range perms {
		if p.CanFor(e.OrganiserID, perm) {
			return e, nil
		}
	}
	return e, apperr.NotFound("Event")
}

// Scan validates a ticket and records the attempt. Business rejections (already used, wrong
// event, ...) are successful API calls with result "rejected"; only malformed requests and
// access problems are errors.
//
// The ticket row is locked for the duration of the decision, so when two devices scan the
// same ticket at the same moment exactly one of them is accepted.
func (s *Service) Scan(ctx context.Context, p *auth.Principal, in ScanInput) (ScanResult, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Gate = strings.TrimSpace(in.Gate)
	if in.Direction == "" {
		in.Direction = DirectionIn
	}
	if in.Direction != DirectionIn && in.Direction != DirectionOut {
		return ScanResult{}, apperr.FieldError("direction", "must be in or out")
	}
	if in.Code == "" || len(in.Code) > maxScanPayloadLength {
		return ScanResult{}, apperr.FieldError("code", "scan a ticket QR code or type the ticket code")
	}
	if len(in.Gate) > 50 {
		in.Gate = in.Gate[:50]
	}
	ev, err := s.loadEvent(ctx, p, in.EventID, rbac.CheckinScan)
	if err != nil {
		return ScanResult{}, err
	}

	result := ScanResult{Direction: in.Direction}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		result.ScannedAt = now
		ticketID, reason, info, err := s.evaluate(ctx, tx, ev, in, now)
		if err != nil {
			return err
		}
		result.Ticket = info
		if reason != "" {
			result.Result, result.Reason, result.Message = ResultRejected, reason, messages[reason]
			if reason == ReasonAlreadyInside && info != nil && info.FirstCheckedInAt != nil {
				result.Message = fmt.Sprintf("Already checked in at %s.", info.FirstCheckedInAt.In(location(ev.Timezone)).Format("15:04"))
			}
		} else {
			result.Result = ResultAccepted
			if in.Direction == DirectionIn {
				result.Message = "Welcome! Ticket accepted."
			} else {
				result.Message = "Checked out. The ticket can be scanned again to re-enter."
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO check_ins (event_id, ticket_id, direction, result, reason, gate, scanned_by, scanned_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, ev.ID, database.NullIfZero(ticketID), in.Direction, result.Result, reason, in.Gate, p.UserID, now)
		return err
	})
	if err != nil {
		return ScanResult{}, err
	}
	stats, err := s.stats(ctx, ev.ID)
	if err != nil {
		return ScanResult{}, err
	}
	result.Stats = stats
	return result, nil
}

// evaluate applies the admission rules and, when they pass, updates the ticket. It returns
// the ticket ID (0 if unknown) and a rejection reason ("" when accepted).
func (s *Service) evaluate(ctx context.Context, tx *sql.Tx, ev scanEvent, in ScanInput, now time.Time) (int64, string, *TicketInfo, error) {
	code, signature, err := tickets.Parse(in.Code)
	if err != nil {
		return 0, ReasonInvalidCode, nil, nil
	}

	var (
		info                      TicketInfo
		eventID                   int64
		qrVersion                 int
		ticketStatus, orderStatus string
	)
	err = tx.QueryRowContext(ctx, `SELECT t.id, t.event_id, t.qr_version, t.status, t.checkin_state, t.first_checked_in_at, t.is_complimentary,
		CONCAT(a.first_name, ' ', a.last_name), tt.name, x.reference, x.status, e.title
		FROM tickets t
		JOIN attendees a ON a.id = t.attendee_id
		JOIN ticket_types tt ON tt.id = t.ticket_type_id
		JOIN orders x ON x.id = t.order_id
		JOIN events e ON e.id = t.event_id
		WHERE t.code = ? FOR UPDATE OF t`, code).
		Scan(&info.ID, &eventID, &qrVersion, &ticketStatus, &info.CheckinState, &info.FirstCheckedInAt, &info.IsComplimentary,
			&info.HolderName, &info.TicketTypeName, &info.OrderReference, &orderStatus, &info.EventTitle)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ReasonNotFound, nil, nil
	}
	if err != nil {
		return 0, "", nil, err
	}
	info.DisplayCode = tickets.FormatCode(code)

	if signature != "" && !s.signer.Verify(code, qrVersion, signature) {
		if s.anyOlderVersionMatches(code, qrVersion, signature) {
			return info.ID, ReasonRevokedQRCode, &info, nil
		}
		return info.ID, ReasonInvalidCode, nil, nil
	}
	if eventID != ev.ID {
		// Do not reveal another event's details to staff of a different event.
		return info.ID, ReasonWrongEvent, &TicketInfo{ID: info.ID, DisplayCode: info.DisplayCode}, nil
	}
	info.EventTitle = ""
	switch {
	case ev.Status == "cancelled":
		return info.ID, ReasonEventCancelled, &info, nil
	case ticketStatus == "refunded":
		return info.ID, ReasonRefunded, &info, nil
	case ticketStatus == "void":
		return info.ID, ReasonTicketVoid, &info, nil
	case orderStatus != "paid" && orderStatus != "partially_refunded":
		return info.ID, ReasonNotPaid, &info, nil
	}
	if reason := windowReason(ev, now, s.cfg); reason != "" {
		return info.ID, reason, &info, nil
	}

	switch in.Direction {
	case DirectionIn:
		switch {
		case info.CheckinState == stateCheckedIn:
			return info.ID, ReasonAlreadyInside, &info, nil
		case info.CheckinState == stateCheckedOut && !ev.AllowReentry:
			return info.ID, ReasonAlreadyUsed, &info, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET checkin_state = 'checked_in', first_checked_in_at = COALESCE(first_checked_in_at, ?),
			last_scanned_at = ?, updated_at = ? WHERE id = ?`, now, now, now, info.ID); err != nil {
			return 0, "", nil, err
		}
		if info.FirstCheckedInAt == nil {
			info.FirstCheckedInAt = &now
		}
		info.CheckinState = stateCheckedIn
	case DirectionOut:
		if info.CheckinState != stateCheckedIn {
			return info.ID, ReasonNotCheckedIn, &info, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET checkin_state = 'checked_out', last_scanned_at = ?, updated_at = ? WHERE id = ?`,
			now, now, info.ID); err != nil {
			return 0, "", nil, err
		}
		info.CheckinState = stateCheckedOut
	}
	return info.ID, "", &info, nil
}

// anyOlderVersionMatches detects a QR code that was valid before the ticket was transferred.
func (s *Service) anyOlderVersionMatches(code string, current int, signature string) bool {
	for v := current - 1; v >= 1 && v >= current-20; v-- {
		if s.signer.Verify(code, v, signature) {
			return true
		}
	}
	return false
}

func location(tz string) *time.Location {
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.UTC
}

// windowReason checks that now falls inside the event's check-in window.
func windowReason(ev scanEvent, now time.Time, cfg Config) string {
	if now.Before(ev.StartsAt.Add(-cfg.OpensBefore)) {
		return ReasonTooEarly
	}
	if now.After(ev.EndsAt.Add(cfg.ClosesAfterEnd)) {
		return ReasonEventEnded
	}
	return ""
}

// Stats is the live attendance picture for an event.
type Stats struct {
	TicketsIssued   int `json:"tickets_issued"`
	CheckedIn       int `json:"checked_in"`
	CurrentlyInside int `json:"currently_inside"`
	RejectedScans   int `json:"rejected_scans"`
}

func (s *Service) stats(ctx context.Context, eventID int64) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(first_checked_in_at IS NOT NULL), 0),
		COALESCE(SUM(checkin_state = 'checked_in'), 0)
		FROM tickets WHERE event_id = ? AND status = 'valid'`, eventID).Scan(&st.TicketsIssued, &st.CheckedIn, &st.CurrentlyInside)
	if err != nil {
		return st, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM check_ins WHERE event_id = ? AND result = 'rejected'`, eventID).Scan(&st.RejectedScans)
	return st, err
}

// Scan record shown in the recent-activity list.
type Scan struct {
	ID          int64     `json:"id"`
	Direction   string    `json:"direction"`
	Result      string    `json:"result"`
	Reason      string    `json:"reason"`
	Gate        string    `json:"gate"`
	ScannedAt   time.Time `json:"scanned_at"`
	ScannedBy   string    `json:"scanned_by"`
	DisplayCode string    `json:"display_code"`
	HolderName  string    `json:"holder_name"`
	TicketType  string    `json:"ticket_type"`
}

// Overview returns stats and recent scans for an event.
func (s *Service) Overview(ctx context.Context, p *auth.Principal, eventID int64, limit int) (Stats, []Scan, error) {
	ev, err := s.loadEvent(ctx, p, eventID, rbac.CheckinScan, rbac.AttendeesView)
	if err != nil {
		return Stats{}, nil, err
	}
	stats, err := s.stats(ctx, ev.ID)
	if err != nil {
		return Stats{}, nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = recentScansDefaultMax
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.direction, c.result, c.reason, c.gate, c.scanned_at,
		CONCAT(u.first_name, ' ', u.last_name), COALESCE(t.code, ''), COALESCE(CONCAT(a.first_name, ' ', a.last_name), ''), COALESCE(tt.name, '')
		FROM check_ins c
		JOIN users u ON u.id = c.scanned_by
		LEFT JOIN tickets t ON t.id = c.ticket_id AND t.event_id = c.event_id
		LEFT JOIN attendees a ON a.id = t.attendee_id
		LEFT JOIN ticket_types tt ON tt.id = t.ticket_type_id
		WHERE c.event_id = ? ORDER BY c.id DESC LIMIT ?`, ev.ID, limit)
	if err != nil {
		return Stats{}, nil, err
	}
	defer rows.Close()
	scans := []Scan{}
	for rows.Next() {
		var sc Scan
		var code string
		if err := rows.Scan(&sc.ID, &sc.Direction, &sc.Result, &sc.Reason, &sc.Gate, &sc.ScannedAt, &sc.ScannedBy, &code, &sc.HolderName, &sc.TicketType); err != nil {
			return Stats{}, nil, err
		}
		sc.DisplayCode = tickets.FormatCode(code)
		scans = append(scans, sc)
	}
	return stats, scans, rows.Err()
}

// ScannableEvent is an event a scanner may choose.
type ScannableEvent struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Timezone string    `json:"timezone"`
	Venue    string    `json:"venue"`
}

// Events lists published events whose check-in window is open or opens within a day.
func (s *Service) Events(ctx context.Context, p *auth.Principal) ([]ScannableEvent, error) {
	scopeSQL, args := p.Scope(rbac.CheckinScan).SQL("e.organiser_id")
	now := s.now()
	args = append(args, now.Add(24*time.Hour+s.cfg.OpensBefore), now.Add(-s.cfg.ClosesAfterEnd))
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.title, e.starts_at, e.ends_at, e.timezone, COALESCE(v.name, '')
		FROM events e LEFT JOIN venues v ON v.id = e.venue_id
		WHERE `+scopeSQL+` AND e.status = 'published' AND e.starts_at <= ? AND e.ends_at >= ?
		ORDER BY e.starts_at LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScannableEvent{}
	for rows.Next() {
		var e ScannableEvent
		if err := rows.Scan(&e.ID, &e.Title, &e.StartsAt, &e.EndsAt, &e.Timezone, &e.Venue); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- HTTP -----------------------------------------------------------------------------

// Handler exposes check-in endpoints.
type Handler struct{ svc *Service }

// NewHandler returns the check-in handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in ScanInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Scan(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	stats, scans, err := h.svc.Overview(r.Context(), p, id, int(httpx.QueryInt64(r, "limit")))
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"stats": stats, "recent_scans": scans})
}

func (h *Handler) Events(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	list, err := h.svc.Events(r.Context(), p)
	if err != nil {
		return err
	}
	return httpx.List(w, list)
}
