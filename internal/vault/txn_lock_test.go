package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func requireLocking(t *testing.T) {
	t.Helper()
	if !lockingSupported {
		t.Skip("path locking is not supported on this platform")
	}
}

// TestFileTxnCommitExcludesASecondWriterOnOnePath pins the mutual exclusion the
// custody step depends on: while one commit is between taking custody and placing
// its replacement, no second transaction may reach the same path at all.
func TestFileTxnCommitExcludesASecondWriterOnOnePath(t *testing.T) {
	requireLocking(t)

	root := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	path := filepath.Join(root, "a.md")

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(path, []byte("first\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan: %v", err)
	}

	var innerErr error
	attempted := false
	txn.hooks.beforeCommit = func(string) error {
		if attempted {
			return nil
		}
		attempted = true
		inner, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new inner txn: %v", err)
		}
		inner.lockWait = 20 * time.Millisecond
		if err := inner.Write(path, []byte("second\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan inner: %v", err)
		}
		innerErr = inner.Commit(nil)
		return nil
	}

	if err := txn.Commit(nil); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !attempted {
		t.Fatal("inner commit never ran")
	}
	if !IsTxnLockBusy(innerErr) {
		t.Fatalf("inner commit err = %v, want *TxnLockBusyError", innerErr)
	}
	// A busy lock must not masquerade as a precondition failure: nothing was read
	// or staged, so the caller's plan is still valid.
	if IsTxnConflict(innerErr) {
		t.Fatalf("lock timeout should not be a conflict: %v", innerErr)
	}
	if got := readFileString(t, path); got != "first\n" {
		t.Fatalf("a.md = %q, want the excluded writer to have changed nothing", got)
	}
}

// TestFileTxnCommitReleasesLocksOnEveryOutcome checks that neither a successful
// nor a failed commit leaves a lock held, since a leaked lock would stall every
// later write to that note for the life of the process.
func TestFileTxnCommitReleasesLocksOnEveryOutcome(t *testing.T) {
	requireLocking(t)

	root := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	path := filepath.Join(root, "a.md")

	commit := func(body string, expect FileExpectation) error {
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		txn.lockWait = 50 * time.Millisecond
		if err := txn.Write(path, []byte(body), 0, expect); err != nil {
			t.Fatalf("plan: %v", err)
		}
		return txn.Commit(nil)
	}

	if err := commit("first\n", ExpectAnyContent()); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := commit("second\n", ExpectContent([]byte("stale\n"))); !IsTxnConflict(err) {
		t.Fatalf("conflicting commit err = %v, want conflict", err)
	}
	if err := commit("third\n", ExpectAnyContent()); err != nil {
		t.Fatalf("commit after conflict: %v", err)
	}
	if got := readFileString(t, path); got != "third\n" {
		t.Fatalf("a.md = %q", got)
	}
}

// TestFileTxnLocksSameFileThroughASymlinkedRoot guards the naming rule that makes
// the locks actually exclusive. Planned paths are resolved through EvalSymlinks,
// so lock names must be relative to the resolved root; keying them on the root as
// given would let two processes reaching one vault by different names hash
// different lock files and exclude nobody.
func TestFileTxnLocksSameFileThroughASymlinkedRoot(t *testing.T) {
	requireLocking(t)

	real := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	txn, err := NewFileTxn(link)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(link, "a.md"), []byte("new\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := txn.Commit(nil); err != nil {
		t.Fatalf("commit: %v", err)
	}

	realRoot, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("eval real root: %v", err)
	}
	want := filepath.Join(realRoot, filepath.FromSlash(pathLockRelDir),
		lockFileName(realRoot, filepath.Join(realRoot, "a.md")))
	if _, err := os.Stat(want); err != nil {
		entries, _ := os.ReadDir(filepath.Join(realRoot, filepath.FromSlash(pathLockRelDir)))
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("lock for the resolved path is missing (%v); locks present: %v", err, names)
	}
}
