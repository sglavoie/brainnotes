package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNoteSaveDoesNotStrandTransactionContent races a writer that goes through
// FileTxn against one that does not. Note.Save is a plain temp-file rename with
// no custody step, so before it took the path lock it could land in the vacancy a
// transaction opens while holding the destination in custody. The transaction's
// exclusive placement then failed, and the note's prior content was left in a
// hidden recovery file for a person to merge by hand.
//
// A conflict is a fine outcome here; a *TxnRecoveryError is not.
func TestNoteSaveDoesNotStrandTransactionContent(t *testing.T) {
	requireLocking(t)

	const rounds = 60
	root := txnTestRoot(t, map[string]string{"a.md": "---\ntitle: A\n---\nstart\n"})
	path := filepath.Join(root, "a.md")

	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			txn, err := NewFileTxn(root)
			if err != nil {
				errs <- err
				return
			}
			body := fmt.Sprintf("---\ntitle: A\n---\ntxn %d\n", i)
			if err := txn.Write(path, []byte(body), 0, ExpectAnyContent()); err != nil {
				errs <- err
				return
			}
			errs <- txn.Commit(nil)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			note := NewNote(root, path)
			if err := note.Load(); err != nil {
				errs <- err
				continue
			}
			_ = note.SetBody(fmt.Sprintf("save %d\n", i))
			errs <- note.Save(nil)
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		if IsTxnRecoveryNeeded(err) {
			t.Fatalf("a writer stranded content in a recovery file: %v", err)
		}
		if IsTxnConflict(err) || IsTxnLockBusy(err) {
			continue
		}
		t.Fatalf("unexpected error: %v", err)
	}

	// Whoever wrote last, the note must be one writer's complete document — never a
	// vacancy and never a partial file.
	data := readFileString(t, path)
	if !strings.HasPrefix(data, "---\ntitle: A\n---\n") {
		t.Fatalf("a.md is not a complete document:\n%q", data)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("transaction temp files left behind: %v", leftovers)
	}
}

// TestLockPathsExcludesATransactionOnTheSamePath checks the standalone lock and
// the transaction lock actually meet on one lock file. They normalize paths
// independently, so a naming drift between them would leave both writers
// believing they hold the path.
func TestLockPathsExcludesATransactionOnTheSamePath(t *testing.T) {
	requireLocking(t)

	root := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	path := filepath.Join(root, "a.md")

	release, err := LockPaths(root, path)
	if err != nil {
		t.Fatalf("LockPaths: %v", err)
	}

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	txn.lockWait = 20 * time.Millisecond
	if err := txn.Write(path, []byte("new\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := txn.Commit(nil); !IsTxnLockBusy(err) {
		release()
		t.Fatalf("commit err = %v, want *TxnLockBusyError", err)
	}
	if got := readFileString(t, path); got != "old\n" {
		release()
		t.Fatalf("a.md = %q, want unchanged", got)
	}

	release()
	if err := txn.Commit(nil); err != nil {
		t.Fatalf("commit after release: %v", err)
	}
	if got := readFileString(t, path); got != "new\n" {
		t.Fatalf("a.md = %q, want new", got)
	}
}

// TestLockPathsIsIdempotentOnDuplicatePaths guards the duplicate collapse. Two
// names for one file must not be locked twice, since the second acquisition would
// wait out the full budget against the caller's own lock.
func TestLockPathsIsIdempotentOnDuplicatePaths(t *testing.T) {
	requireLocking(t)

	root := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	path := filepath.Join(root, "a.md")

	release, err := LockPaths(root, path, filepath.Join(root, ".", "a.md"), path)
	if err != nil {
		t.Fatalf("LockPaths with duplicates: %v", err)
	}
	release()
}

// TestClampLockedPathsFollowsTheDescriptorBudget pins the shape of the bound one
// transaction locks against: it grows with the process descriptor budget so a
// whole-list rewrite is not refused on a machine that can hold the locks, never
// claims more paths than the process has descriptors for, and never grows past
// the point where one transaction would stall the whole vault. There is
// deliberately no floor — a threshold above the budget makes a set below it take
// one lock per path and fail on EMFILE instead of escalating.
func TestClampLockedPathsFollowsTheDescriptorBudget(t *testing.T) {
	for name, tc := range map[string]struct {
		limit int
		want  int
	}{
		"limit under the reserve locks nothing individually": {limit: 64, want: 0},
		"reserve plus the guard is the zero point":           {limit: pathLockFDReserve + pathLockGuardFDs, want: 0},
		"a low limit stays under the historical floor":       {limit: 160, want: 160 - pathLockFDReserve - pathLockGuardFDs},
		"budget above the reserve grows":                     {limit: 1024, want: 1024 - pathLockFDReserve - pathLockGuardFDs},
		"huge limit stops at the ceiling":                    {limit: 1 << 20, want: maxLockedPathsCeiling},
	} {
		t.Run(name, func(t *testing.T) {
			if got := clampLockedPaths(tc.limit); got != tc.want {
				t.Fatalf("clampLockedPaths(%d) = %d, want %d", tc.limit, got, tc.want)
			}
		})
	}
}

// TestEscalatedLockUsesOneDescriptorAndExcludesEveryPath is the mechanism the
// ceiling used to stand in for. A set larger than the per-path budget takes the
// vault guard exclusively instead of one lock per path, so its descriptor cost
// is constant, and every other writer — even one touching a path the set does
// not name — is excluded for the duration.
func TestEscalatedLockUsesOneDescriptorAndExcludesEveryPath(t *testing.T) {
	requireLocking(t)

	root := txnTestRoot(t, map[string]string{"a.md": "a\n", "b.md": "b\n", "c.md": "c\n"})
	paths := []string{filepath.Join(root, "a.md"), filepath.Join(root, "b.md")}

	locks, err := acquirePathLocks(root, 20*time.Millisecond, 1, paths)
	if err != nil {
		t.Fatalf("acquirePathLocks escalated: %v", err)
	}
	if len(locks.files) != 1 {
		t.Fatalf("escalated lock held %d descriptors, want 1", len(locks.files))
	}

	unrelated, err := acquirePathLocks(root, 20*time.Millisecond, 0, []string{filepath.Join(root, "c.md")})
	if !IsTxnLockBusy(err) {
		unrelated.release()
		locks.release()
		t.Fatalf("unrelated writer err = %v, want *TxnLockBusyError", err)
	}

	locks.release()
	after, err := acquirePathLocks(root, 20*time.Millisecond, 0, []string{filepath.Join(root, "c.md")})
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	after.release()
}

// TestOrdinaryLocksShareTheVaultGuard checks the guard did not accidentally
// serialize the ordinary case: two writers on different paths must still run at
// the same time, or every concurrent bn write in the vault would queue.
func TestOrdinaryLocksShareTheVaultGuard(t *testing.T) {
	requireLocking(t)

	root := txnTestRoot(t, map[string]string{"a.md": "a\n", "b.md": "b\n"})

	first, err := acquirePathLocks(root, 20*time.Millisecond, 0, []string{filepath.Join(root, "a.md")})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.release()
	second, err := acquirePathLocks(root, 20*time.Millisecond, 0, []string{filepath.Join(root, "b.md")})
	if err != nil {
		t.Fatalf("second acquire on a different path: %v", err)
	}
	second.release()
}

// TestFileTxnAtomicCommitBeyondLockedPathCeiling is the boundary itself: a
// transaction planning more paths than MaxLockedPaths reports must still commit
// as one action. It used to be impossible — every planned path needed its own
// held descriptor, so the ceiling was a hard limit on how many notes one
// ordering could span.
func TestFileTxnAtomicCommitBeyondLockedPathCeiling(t *testing.T) {
	requireLocking(t)

	count := MaxLockedPaths() + 8
	root := t.TempDir()
	paths := make([]string, 0, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("n%05d.txt", i))
		if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		paths = append(paths, path)
	}

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
		t.Fatalf("commit over %d paths: %v", count, err)
	}
	if captured.paths != count {
		t.Fatalf("journal captured %d paths, want %d in the one action", captured.paths, count)
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

type countingJournal struct{ paths int }

func (c *countingJournal) Capture(string) error {
	c.paths++
	return nil
}

// TestMaxLockedPathsAllowsMoreThanTheHistoricalCap is why the budget is derived
// at all: a whole-list pin renormalize can exceed the fixed 256 paths one
// transaction used to be refused above, so on a machine with descriptors to
// spare the answer has to be larger than that. The skip is decided from the
// operating system's limit rather than from the code under test, so lowering the
// ceiling back to the historical cap fails here instead of quietly turning the
// large-list tests in noteops and web into skips.
func TestMaxLockedPathsAllowsMoreThanTheHistoricalCap(t *testing.T) {
	const historicalCap = 256
	if limit, ok := openFileLimit(); ok && limit <= historicalCap+pathLockFDReserve+pathLockGuardFDs {
		t.Skipf("process descriptor limit is %d, too small to lock more than %d paths", limit, historicalCap)
	}
	if got := MaxLockedPaths(); got <= historicalCap {
		t.Fatalf("MaxLockedPaths() = %d, want more than the historical %d-path cap", got, historicalCap)
	}
}
