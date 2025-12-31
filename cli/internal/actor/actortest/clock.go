package actortest

import (
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/actor"
)

// FakeClock is a deterministic Clock for tests.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

var _ actor.Clock = (*FakeClock)(nil)

// NewFakeClock returns a FakeClock starting at the given time.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now implements actor.Clock.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set sets the current clock time.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves time forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

