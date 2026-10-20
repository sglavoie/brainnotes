package vault

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// A cancelled wait reports the context's error, not a busy lock, returns well
// before the budget, and leaves nothing held.
func TestLockFileContextStopsWaitingOnCancel(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "locks", "named.lock")
	release, err := LockFile(lockPath, true, time.Second)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = LockFileContext(ctx, lockPath, false, 10*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LockFileContext error = %v, want context.DeadlineExceeded", err)
	}
	if IsTxnLockBusy(err) {
		t.Fatalf("a cancelled wait was reported as a busy lock: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("cancelled wait took %s", took)
	}

	release()
	again, err := LockFile(lockPath, true, -1)
	if err != nil {
		t.Fatalf("lock after cancelled wait: %v", err)
	}
	again()
}
