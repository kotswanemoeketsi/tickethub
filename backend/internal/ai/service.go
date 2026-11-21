package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/ratelimit"
)

// Feature names, as recorded in the usage log.
const (
	FeatureSearch = "search"
	FeatureWriter = "event_writer"
	FeatureReview = "event_review"
)

// Config selects the models and limits.
type Config struct {
	Model           string // everyday features
	ReviewModel     string // the event pre-check, which needs more judgement
	DailyTokenLimit int    // across the platform, 0 for no limit
	Timezone        *time.Location
}

// Deps are the services the AI features use.
type Deps struct {
	// LLM answers the requests; nil switches AI off and the platform works as before.
	LLM     Completer
	DB      *database.DB
	Cache   cache.Cache
	Limiter *ratelimit.Limiter
	Now     clock.Func
	Events  EventSource
	Logger  *slog.Logger
}

// Service runs the AI features.
type Service struct {
	Deps
	llm Completer
	db  *database.DB
	now clock.Func
	cfg Config
}

// NewService builds the service.
func NewService(d Deps, cfg Config) *Service {
	if cfg.Timezone == nil {
		cfg.Timezone = time.UTC
	}
	return &Service{Deps: d, llm: d.LLM, db: d.DB, now: d.Now, cfg: cfg}
}

func (s *Service) cache() cache.Cache { return s.Cache }

// Enabled reports whether AI features are available.
func (s *Service) Enabled() bool { return s.llm != nil }

var (
	errOff         = apperr.New(http.StatusServiceUnavailable, "ai_disabled", "AI features are not switched on.")
	errUnavailable = apperr.New(http.StatusServiceUnavailable, "ai_unavailable", "The AI assistant isn't available right now. Please try again in a minute.")
	errBudget      = apperr.New(http.StatusServiceUnavailable, "ai_limit_reached", "The AI assistant has reached today's usage limit. It will be back tomorrow.")
	errBadAnswer   = apperr.New(http.StatusServiceUnavailable, "ai_unavailable", "The AI assistant gave an answer that couldn't be used. Please try again.")
)

// allow applies a per-user (or per-IP) limit to a feature.
func (s *Service) allow(ctx context.Context, key string, limit int, window time.Duration) error {
	if !s.Limiter.Allow(ctx, "ai:"+key, limit, window) {
		return apperr.TooManyRequests("You've used the AI assistant a lot in a short time. Please wait a little and try again.")
	}
	return nil
}

// call sends one request, records what it cost and decodes the JSON answer into out.
func (s *Service) call(ctx context.Context, feature string, userID int64, req Request, out any) error {
	if s.llm == nil {
		return errOff
	}
	if s.cfg.DailyTokenLimit > 0 {
		used, err := s.tokensToday(ctx)
		if err != nil {
			return err
		}
		if used >= int64(s.cfg.DailyTokenLimit) {
			s.record(ctx, feature, userID, req.Model, Usage{}, "rejected", 0)
			return errBudget
		}
	}

	started := time.Now()
	text, usage, err := s.llm.Complete(ctx, req)
	latency := time.Since(started)
	if err != nil {
		s.record(ctx, feature, userID, req.Model, usage, "failed", latency)
		if errors.Is(err, context.Canceled) {
			return err
		}
		s.Logger.WarnContext(ctx, "AI request failed", "feature", feature, "model", req.Model, "error", err)
		return errUnavailable
	}
	s.record(ctx, feature, userID, req.Model, usage, "ok", latency)
	if err := json.Unmarshal([]byte(text), out); err != nil {
		s.Logger.WarnContext(ctx, "AI answer was not the expected JSON", "feature", feature, "error", err)
		return errBadAnswer
	}
	return nil
}

func (s *Service) tokensToday(ctx context.Context) (int64, error) {
	now := s.now().In(s.cfg.Timezone)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.cfg.Timezone)
	var used int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(prompt_tokens + completion_tokens), 0) FROM ai_usage WHERE created_at >= ?`, start.UTC()).Scan(&used)
	return used, err
}

// record writes the usage log. A failure here is logged, not returned: the user's request
// already succeeded or failed on its own merits.
func (s *Service) record(ctx context.Context, feature string, userID int64, model string, u Usage, status string, latency time.Duration) {
	_, err := s.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO ai_usage
		(feature, user_id, model, prompt_tokens, cached_tokens, completion_tokens, status, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		feature, database.NullIfZero(userID), model, u.PromptTokens, u.CachedTokens, u.CompletionTokens, status, latency.Milliseconds(), s.now())
	if err != nil {
		s.Logger.WarnContext(ctx, "record AI usage", "error", err)
	}
}

// Personal details are removed from free text before it leaves the platform, so a model
// provider never receives them even when someone types them in.
var (
	emailPattern = regexp.MustCompile(`[\p{L}0-9._%+\-]+@[\p{L}0-9.\-]+\.[\p{L}]{2,}`)
	// Long digit runs, allowing spaces, dashes, dots and brackets: phone, ID, card and
	// account numbers. Short numbers such as prices, years and times are kept.
	numberPattern = regexp.MustCompile(`\+?\(?\d[\d\s().\-]{7,}\d`)
)

func redact(text string) string {
	text = emailPattern.ReplaceAllString(text, "[email removed]")
	return numberPattern.ReplaceAllStringFunc(text, func(m string) string {
		digits := 0
		for _, r := range m {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits < 9 {
			return m
		}
		return "[number removed]"
	})
}

// clip shortens text to at most n runes.
func clip(text string, n int) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return text
}

// asData encodes untrusted content for the user message. The system prompts say the user
// message is data, never instructions; JSON makes the boundary unambiguous.
func asData(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
