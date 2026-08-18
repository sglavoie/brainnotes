//go:build darwin || linux

package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// lowDescriptorLimit is a soft RLIMIT_NOFILE whose usable budget lands far
	// below the 256 paths the threshold used to be floored at, while still leaving
	// the test binary itself room to work.
	lowDescriptorLimit = 160
	// lowDescriptorPathCount is the band the floor got wrong: fewer paths than the
	// old 256-path floor, so escalation was skipped, but more paths than
	// lowDescriptorLimit can hold lock descriptors for, so acquisition failed.
	lowDescriptorPathCount = 200
)

// lowerDescriptorLimit drops the process soft descriptor limit for the duration
// of one test. The threshold is derived from the live limit, so this is the only
// way to reach the low-budget band without a hook the production path does not
// have. It must not be used from a parallel test: the limit is process-wide.
func lowerDescriptorLimit(t *testing.T, limit int) {
	t.Helper()

	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
		t.Skipf("cannot read RLIMIT_NOFILE: %v", err)
	}
	if original.Cur <= uint64(limit) {
		t.Skipf("process descriptor limit is already %d, at or below the %d this test lowers to", original.Cur, limit)
	}
	lowered := original
	lowered.Cur = uint64(limit)
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lowered); err != nil {
		t.Skipf("cannot lower RLIMIT_NOFILE to %d: %v", limit, err)
	}
	t.Cleanup(func() {
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
			t.Errorf("restore RLIMIT_NOFILE to %d: %v", original.Cur, err)
		}
	})
}

func seedLockPaths(t *testing.T, root string, count int) []string {
	t.Helper()

	paths := make([]string, 0, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("n%05d.txt", i))
		if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		paths = append(paths, path)
	}
	return paths
}

// TestLowDescriptorLimitEscalatesInsteadOfExhaustingDescriptors covers the band
// between the descriptor budget and the historical 256-path floor. The floor let
// clampLockedPaths claim 256 lockable paths on a process that could not hold that
// many, so a set in this band took one lock per path and failed acquisition on
// EMFILE — even though the single exclusive guard it should have escalated to
// would have committed it.
func TestLowDescriptorLimitEscalatesInsteadOfExhaustingDescriptors(t *testing.T) {
	requireLocking(t)
	lowerDescriptorLimit(t, lowDescriptorLimit)

	budget := MaxLockedPaths()
	if budget >= lowDescriptorPathCount {
		t.Fatalf("MaxLockedPaths() = %d under a %d-descriptor limit, want fewer than the %d planned paths",
			budget, lowDescriptorLimit, lowDescriptorPathCount)
	}
	if budget > lowDescriptorLimit-pathLockFDReserve-pathLockGuardFDs {
		t.Fatalf("MaxLockedPaths() = %d, want no more than the usable descriptor budget", budget)
	}

	root := t.TempDir()
	paths := seedLockPaths(t, root, lowDescriptorPathCount)

	locks, err := acquirePathLocks(root, 20*time.Millisecond, 0, paths)
	if err != nil {
		t.Fatalf("acquirePathLocks over %d paths: %v", len(paths), err)
	}
	if len(locks.files) != 1 {
		locks.release()
		t.Fatalf("held %d descriptors, want only the exclusive vault guard", len(locks.files))
	}
	locks.release()

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	for _, path := range paths {
		if err := txn.Write(path, []byte("new\n"), 0, ExpectContent([]byte("old\n"))); err != nil {
			t.Fatalf("plan %s: %v", path, err)
		}
	}
	captured := &countingJournal{}
	if err := txn.Commit(captured); err != nil {
		t.Fatalf("commit over %d paths under a %d-descriptor limit: %v", len(paths), lowDescriptorLimit, err)
	}
	if captured.paths != len(paths) {
		t.Fatalf("journal captured %d paths, want %d in the one action", captured.paths, len(paths))
	}
	for _, path := range paths {
		if got := readFileString(t, path); got != "new\n" {
			t.Fatalf("%s = %q, want the committed content", path, got)
		}
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("transaction temp files left behind: %d", len(leftovers))
	}
}

// TestLowDescriptorLimitEscalatedCommitStillRefusesAStalePath proves the
// escalated guard bought the low-budget band size without giving up
// all-or-nothing: one path replaced after planning leaves every other path in the
// set untouched and journals nothing.
func TestLowDescriptorLimitEscalatedCommitStillRefusesAStalePath(t *testing.T) {
	requireLocking(t)
	lowerDescriptorLimit(t, lowDescriptorLimit)

	root := t.TempDir()
	paths := seedLockPaths(t, root, lowDescriptorPathCount)

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	for _, path := range paths {
		if err := txn.Write(path, []byte("new\n"), 0, ExpectContent([]byte("old\n"))); err != nil {
			t.Fatalf("plan %s: %v", path, err)
		}
	}

	stale := paths[len(paths)/2]
	if err := os.WriteFile(stale, []byte("someone else\n"), 0o644); err != nil {
		t.Fatalf("concurrent write %s: %v", stale, err)
	}

	captured := &countingJournal{}
	if err := txn.Commit(captured); !IsTxnConflict(err) {
		t.Fatalf("commit err = %v, want *TxnConflictError", err)
	}
	if captured.paths != 0 {
		t.Fatalf("journal captured %d paths, want none", captured.paths)
	}
	for _, path := range paths {
		want := "old\n"
		if path == stale {
			want = "someone else\n"
		}
		if got := readFileString(t, path); got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("transaction temp files left behind: %d", len(leftovers))
	}
}
