package vaultgit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPreflightWaitsForShortBackgroundOperation(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	head := h.head()
	other := h.open(Options{})
	release, err := h.mgr.acquire(safetyLockName, "background snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan error, 1)
	go func() { done <- other.Preflight(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("preflight returned while the background operation held the lock: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preflight did not resume")
	}
	if h.head() != head {
		t.Fatal("waiting for preflight must not create a commit")
	}
}

func TestPreflightWaitIsBoundedAndOnlyRetriesBusy(t *testing.T) {
	busy := safetyErrorf(KindBusy, "preflight", "busy")
	refused := safetyErrorf(KindBlocked, "preflight", "blocked")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"busy", busy}, {"blocked", refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			started := time.Now()
			_, err := waitForPreflight(context.Background(), 60*time.Millisecond, func() (func(), error) {
				calls++
				return nil, tc.err
			})
			if !errors.Is(err, tc.err) || time.Since(started) > time.Second {
				t.Fatalf("wait = %v after %s", err, time.Since(started))
			}
			if tc.err == refused && calls != 1 {
				t.Fatalf("retried a refusal %d times", calls)
			}
			if tc.err == busy && calls < 2 {
				t.Fatal("did not wait for contention")
			}
		})
	}
}

func TestPreflightCancellationStopsAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := waitForPreflight(ctx, time.Second, func() (func(), error) {
		calls++
		cancel()
		return nil, safetyErrorf(KindBusy, "preflight", "busy")
	})
	if !IsBusy(err) || calls != 1 {
		t.Fatalf("canceled wait = %v, %d acquisitions", err, calls)
	}
	_, err = waitForPreflight(ctx, time.Second, func() (func(), error) {
		t.Fatal("acquired after cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
