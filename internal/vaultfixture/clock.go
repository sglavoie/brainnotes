package vaultfixture

import (
	"sync"
	"testing"
	"time"
)

// Epoch is the instant every controlled clock starts at unless a test names
// another one. It is a fixed UTC time with a nonzero wall-clock component, so a
// formatting or truncation bug cannot hide behind midnight or a zero Time.
var Epoch = time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)

// Clock is a controlled clock. It never moves on its own: a test advances it
// explicitly, so an ordering assertion depends on the operations under test
// rather than on how long the test took to run, and two events a real clock
// would have stamped identically are distinguishable here.
//
// Its Now method value has type func() time.Time, which is the clock seam every
// package in this repository already accepts — journal.WithClock,
// index.WithClock, vaultgit.Options.Now, noteops.WithTrashClock and the rest —
// so a fixture is wired in as `WithClock(clock.Now)`.
//
// It is safe for concurrent use: the web server reads a clock from request
// goroutines while a test advances it.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock stopped at Epoch, registered with the test only so
// the constructor reads like the other fixtures in this package.
func NewClock(t *testing.T) *Clock {
	t.Helper()
	return NewClockAt(Epoch)
}

// NewClockAt returns a clock stopped at start.
func NewClockAt(start time.Time) *Clock {
	return &Clock{now: start.UTC()}
}

// Now reports the current fixture time in UTC.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and returns the new time. A negative or
// zero d is allowed: a test that needs two events to share a timestamp, or to
// arrive out of order, is testing something real.
func (c *Clock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// Set moves the clock to an absolute instant and returns it.
func (c *Clock) Set(at time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at.UTC()
	return c.now
}

// Tick advances the clock by one second and returns the new time. It is the
// common case — "a later, distinguishable instant" — spelled once so tests do
// not each invent their own step.
func (c *Clock) Tick() time.Time {
	return c.Advance(time.Second)
}
