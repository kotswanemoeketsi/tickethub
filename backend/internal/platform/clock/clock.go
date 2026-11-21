// Package clock lets services read the current time through an injectable function, so
// time-dependent behaviour (reservation expiry, check-in windows) is testable.
package clock

import (
	"sync"
	"time"
)

// Func returns the current time.
type Func func() time.Time

// System returns the real current time in UTC.
func System() time.Time { return time.Now().UTC() }

// Fake is a manually controlled clock for tests.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC()} }

// Now returns the fake current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the fake clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Set moves the fake clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}
