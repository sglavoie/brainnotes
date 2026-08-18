package vault

import (
	"fmt"
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
