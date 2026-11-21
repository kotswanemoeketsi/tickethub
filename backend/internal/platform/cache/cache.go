// Package cache provides a small key/value cache abstraction backed by Redis in production
// and by process memory when Redis is not configured (local development, tests).
package cache

import (
	"context"
	"encoding/json"
	"time"
)

// Cache is a byte-oriented key/value store with expiry.
type Cache interface {
	// Get returns the value and true, or nil and false when the key is missing.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores value; a ttl of zero means no expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	// Incr atomically increments a counter, starting the ttl window on first increment.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	Close() error
}

// GetJSON decodes a cached JSON value into dst. It reports false on a miss.
func GetJSON(ctx context.Context, c Cache, key string, dst any) (bool, error) {
	raw, ok, err := c.Get(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		// A value we cannot decode is treated as a miss and removed.
		_ = c.Delete(ctx, key)
		return false, nil
	}
	return true, nil
}

// SetJSON stores v as JSON.
func SetJSON(ctx context.Context, c Cache, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, raw, ttl)
}
