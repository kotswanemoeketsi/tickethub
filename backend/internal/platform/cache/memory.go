package cache

import (
	"context"
	"strconv"
	"sync"
	"time"
)

type memoryItem struct {
	value     []byte
	expiresAt time.Time // zero means no expiry
}

func (i memoryItem) expired(now time.Time) bool {
	return !i.expiresAt.IsZero() && now.After(i.expiresAt)
}

// Memory is an in-process Cache. It is safe for concurrent use but not shared between
// processes, so it is intended for single-instance development and tests.
type Memory struct {
	mu    sync.Mutex
	items map[string]memoryItem
	sets  int
}

// NewMemory returns an empty in-memory cache.
func NewMemory() *Memory { return &Memory{items: map[string]memoryItem{}} }

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[key]
	if !ok || item.expired(time.Now()) {
		delete(m.items, key)
		return nil, false, nil
	}
	return append([]byte(nil), item.value...), true, nil
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = memoryItem{value: append([]byte(nil), value...), expiresAt: expiry(ttl)}
	m.sets++
	if m.sets%1000 == 0 {
		m.sweepLocked()
	}
	return nil
}

func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.items, k)
	}
	return nil
}

func (m *Memory) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	item, ok := m.items[key]
	if !ok || item.expired(now) {
		item = memoryItem{value: []byte("0"), expiresAt: expiry(ttl)}
	}
	n, _ := strconv.ParseInt(string(item.value), 10, 64)
	n++
	item.value = []byte(strconv.FormatInt(n, 10))
	m.items[key] = item
	return n, nil
}

func (m *Memory) Close() error { return nil }

func (m *Memory) sweepLocked() {
	now := time.Now()
	for k, item := range m.items {
		if item.expired(now) {
			delete(m.items, k)
		}
	}
}

func expiry(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}
