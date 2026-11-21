package ai

import (
	"context"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/events"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
)

// EventSource is the part of the events service the pre-check reads through, so it sees
// exactly what the reviewer may see.
type EventSource interface {
	Authorize(ctx context.Context, p *auth.Principal, id int64, perm string) (events.Event, error)
	TicketTypes(ctx context.Context, p *auth.Principal, eventID int64) ([]events.TicketType, error)
}

// Category is an event category the model may choose from.
type Category struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// categories lists the event categories, cached for a few minutes.
func (s *Service) categories(ctx context.Context) ([]Category, error) {
	const key = "ai:categories"
	var cached []Category
	if hit, _ := cache.GetJSON(ctx, s.cache(), key, &cached); hit {
		return cached, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT slug, name FROM event_categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Slug, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = cache.SetJSON(ctx, s.cache(), key, out, 5*time.Minute)
	return out, nil
}
