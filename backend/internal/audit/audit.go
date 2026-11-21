// Package audit records who changed what. Entries are written in the same transaction as
// the change they describe, so the log can never disagree with the data.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Entry describes one audited action.
type Entry struct {
	Action      string // e.g. "event.published", "ticket_type.price_changed"
	EntityType  string // e.g. "event"
	EntityID    int64
	OrganiserID int64 // owner of the entity, used to scope who may read the entry
	Summary     string
	Changes     any // optional structured detail, stored as JSON
}

// Record writes e, attributing it to the principal in ctx (or "system" when there is none).
func Record(ctx context.Context, q database.Queryer, now time.Time, e Entry) error {
	var actorID any
	actorEmail := "system"
	if p := auth.FromContext(ctx); p != nil {
		actorID = p.UserID
		actorEmail = p.Email
	}
	meta := httpx.ClientMetaFrom(ctx)

	var changes any
	if e.Changes != nil {
		raw, err := json.Marshal(e.Changes)
		if err != nil {
			return fmt.Errorf("encode audit changes: %w", err)
		}
		changes = raw
	}
	summary := e.Summary
	if len(summary) > 500 {
		summary = summary[:500]
	}
	_, err := q.ExecContext(ctx, `INSERT INTO audit_logs
		(actor_id, actor_email, action, entity_type, entity_id, organiser_id, summary, changes, ip_address, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actorID, actorEmail, e.Action, e.EntityType, database.NullIfZero(e.EntityID), database.NullIfZero(e.OrganiserID),
		summary, changes, meta.IP, meta.UserAgent, now)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// Change is a before/after pair for one field.
type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// Diff collects changed fields between two snapshots for an audit entry.
type Diff map[string]Change

// Add records field when from and to differ.
func (d Diff) Add(field string, from, to any) {
	if fmt.Sprint(from) != fmt.Sprint(to) {
		d[field] = Change{From: from, To: to}
	}
}

// Fields returns the changed field names, for summaries.
func (d Diff) Fields() string {
	names := make([]string, 0, len(d))
	for k := range d {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}
