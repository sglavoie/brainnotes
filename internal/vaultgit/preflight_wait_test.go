package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/restorestate"
)

func TestBarrierJoinErrorOnlyTreatsAHeldCheckpointAsBusy(t *testing.T) {
	t.Parallel()
	held := fmt.Errorf("%w: it is copying the vault", checkpoint.ErrBarrierHeld)
	if err := barrierJoinError("preflight", "the safety.lock was not taken", held); !IsBusy(err) || !errors.Is(err, checkpoint.ErrBarrierHeld) {
		t.Fatalf("held barrier = %v (kind %q), want busy wrapping ErrBarrierHeld", err, KindOf(err))
	}
	cause := errors.New("checkpoint record is unreadable")
	err := barrierJoinError("preflight", "the safety.lock was not taken", cause)
	if IsBusy(err) || KindOf(err) != KindRepositoryState || !errors.Is(err, cause) {
		t.Fatalf("other join failure = %v (kind %q), want repository_state wrapping its cause", err, KindOf(err))
	}
}

func TestPreflightDoesNotReportAPendingRestoreAsBusy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	root, err := filepath.EvalSymlinks(h.root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, restorestate.RelDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	intent := fmt.Sprintf(`{"version":1,"archive":%q,"phase":"content"}`, filepath.Join(dir, "archive.tar.gz"))
	if err := os.WriteFile(filepath.Join(dir, "intent.json"), []byte(intent), 0o600); err != nil {
		t.Fatal(err)
	}
	err = h.mgr.Preflight(context.Background())
	if err == nil || IsBusy(err) || !errors.Is(err, checkpoint.ErrRestorePending) {
		t.Fatalf("preflight with a pending restore = %v (kind %q), want a non-busy error wrapping ErrRestorePending", err, KindOf(err))
	}
}

func TestPreflightWaitsForShortBackgroundOperation(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
