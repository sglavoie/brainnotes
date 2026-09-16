package vaultfixture

import (
	"sync"
	"testing"
	"time"
)

// TestClockMovesOnlyWhenAdvanced is the property the fixture exists for: two
// reads taken around real work report the same instant, so nothing downstream
// can depend on how long a test took.
func TestClockMovesOnlyWhenAdvanced(t *testing.T) {
	clock := NewClock(t)

	first := clock.Now()
	if !first.Equal(Epoch) {
		t.Fatalf("NewClock().Now() = %v, want %v", first, Epoch)
	}
	time.Sleep(2 * time.Millisecond)
	if second := clock.Now(); !second.Equal(first) {
		t.Fatalf("the clock moved on its own: %v then %v", first, second)
	}

	if got, want := clock.Advance(90*time.Second), Epoch.Add(90*time.Second); !got.Equal(want) {
		t.Fatalf("Advance returned %v, want %v", got, want)
	}
	if got := clock.Now(); !got.Equal(Epoch.Add(90 * time.Second)) {
		t.Fatalf("Now() = %v after Advance, want %v", got, Epoch.Add(90*time.Second))
	}
	if got, want := clock.Tick(), Epoch.Add(91*time.Second); !got.Equal(want) {
		t.Fatalf("Tick returned %v, want %v", got, want)
	}
}

// TestClockNowSatisfiesTheRepositoryClockSeam pins the fixture to the shape
// every package here already injects, so wiring it in is a method value and not
// an adapter.
func TestClockNowSatisfiesTheRepositoryClockSeam(t *testing.T) {
	clock := NewClockAt(time.Date(2027, time.December, 31, 23, 59, 59, 0, time.UTC))
	var seam func() time.Time = clock.Now

	before := seam()
	clock.Advance(time.Second)
	after := seam()
	if !after.After(before) {
		t.Fatalf("the injected seam reported %v then %v, want the second to be later", before, after)
	}
	if after.Location() != time.UTC {
		t.Fatalf("the clock reported %v in %v, want UTC", after, after.Location())
	}
}

// TestClockIsSafeForConcurrentUse covers the real usage: a fixture server reads
// the clock from request goroutines while the test advances it.
func TestClockIsSafeForConcurrentUse(t *testing.T) {
	clock := NewClock(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				clock.Advance(time.Millisecond)
				_ = clock.Now()
			}
		}()
	}
	wg.Wait()

	if got, want := clock.Now(), Epoch.Add(800*time.Millisecond); !got.Equal(want) {
		t.Fatalf("Now() = %v after 800 concurrent advances, want %v", got, want)
	}
}
