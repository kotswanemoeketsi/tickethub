// Package ratelimit implements fixed-window rate limiting on top of the shared cache.
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Limiter counts events per key within fixed time windows.
type Limiter struct {
	cache cache.Cache
}

// New returns a limiter backed by c.
func New(c cache.Cache) *Limiter { return &Limiter{cache: c} }

// Allow records one event for key and reports whether it is within limit for the window.
// If the cache is unavailable the request is allowed (fail open) and the error is logged:
// a cache outage must not take login or checkout down.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) bool {
	n, err := l.cache.Incr(ctx, windowKey(key, window), window)
	if err != nil {
		slog.WarnContext(ctx, "rate limiter unavailable, allowing request", "error", err)
		return true
	}
	return n <= int64(limit)
}

// Exceeded reports whether key has already used up its limit in the current window, without
// recording an event. Pair it with Allow to count only some outcomes, such as failed sign-ins.
func (l *Limiter) Exceeded(ctx context.Context, key string, limit int, window time.Duration) bool {
	raw, ok, err := l.cache.Get(ctx, windowKey(key, window))
	if err != nil {
		slog.WarnContext(ctx, "rate limiter unavailable, allowing request", "error", err)
		return false
	}
	if !ok {
		return false
	}
	n, _ := strconv.ParseInt(string(raw), 10, 64)
	return n >= int64(limit)
}

func windowKey(key string, window time.Duration) string {
	return fmt.Sprintf("ratelimit:%s:%d", key, time.Now().Unix()/int64(window.Seconds()))
}

// Middleware limits requests per client IP for a named route group.
func (l *Limiter) Middleware(name string, limit int, window time.Duration) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := name + ":" + httpx.ClientMetaFrom(r.Context()).IP
			if !l.Allow(r.Context(), key, limit, window) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				httpx.WriteError(w, r, apperr.TooManyRequests("Too many requests. Please wait a moment and try again."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
