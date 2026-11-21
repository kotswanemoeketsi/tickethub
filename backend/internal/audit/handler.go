package audit

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Log is one audit log row as returned by the API.
type Log struct {
	ID          int64           `json:"id"`
	ActorID     *int64          `json:"actor_id"`
	ActorEmail  string          `json:"actor_email"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *int64          `json:"entity_id"`
	OrganiserID *int64          `json:"organiser_id"`
	Summary     string          `json:"summary"`
	Changes     json.RawMessage `json:"changes,omitempty"`
	IPAddress   string          `json:"ip_address"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Handler serves the audit log.
type Handler struct {
	db *database.DB
}

// NewHandler returns an audit log handler.
func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

// List returns audit entries. Platform users with access to all organisers see everything;
// organiser users see entries about their own organisers only.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(r)
	scope := p.Scope(rbac.AuditView)

	where := []string{}
	args := []any{}
	if !scope.All {
		clause, scopeArgs := scope.SQL("organiser_id")
		where = append(where, clause)
		args = append(args, scopeArgs...)
	}
	if v := httpx.QueryString(r, "action"); v != "" {
		where = append(where, "action LIKE ?")
		args = append(args, database.EscapeLike(v))
	}
	if v := httpx.QueryString(r, "entity_type"); v != "" {
		where = append(where, "entity_type = ?")
		args = append(args, v)
	}
	if v := httpx.QueryInt64(r, "entity_id"); v > 0 {
		where = append(where, "entity_id = ?")
		args = append(args, v)
	}
	if v := httpx.QueryString(r, "actor"); v != "" {
		where = append(where, "actor_email LIKE ?")
		args = append(args, database.EscapeLike(v))
	}
	if v := httpx.QueryInt64(r, "organiser_id"); v > 0 {
		where = append(where, "organiser_id = ?")
		args = append(args, v)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := h.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM audit_logs "+clause, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id, actor_id, actor_email, action, entity_type, entity_id, organiser_id,
		summary, changes, ip_address, created_at FROM audit_logs `+clause+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, page.Size, page.Offset())...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var logs []Log
	for rows.Next() {
		var l Log
		var changes []byte
		if err := rows.Scan(&l.ID, &l.ActorID, &l.ActorEmail, &l.Action, &l.EntityType, &l.EntityID, &l.OrganiserID,
			&l.Summary, &changes, &l.IPAddress, &l.CreatedAt); err != nil {
			return err
		}
		if len(changes) > 0 {
			l.Changes = changes
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return httpx.Paginated(w, logs, total, page)
}
