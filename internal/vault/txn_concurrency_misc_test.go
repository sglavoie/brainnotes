package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestFileTxnRejectsPathsOutsideRoot(t *testing.T) {
	root := txnTestRoot(t, nil)
	outside := filepath.Join(t.TempDir(), "escape.md")
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(outside, []byte("nope\n"), 0, ExpectMissing()); err == nil {
		t.Fatal("planning a path outside the root must fail")
	}
	if err := txn.Write(filepath.Join(root, "..", "escape.md"), []byte("nope\n"), 0, ExpectMissing()); err == nil {
		t.Fatal("planning a traversal path must fail")
	}
}

func TestFileTxnRejectsDuplicatePathPlans(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("one\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan first: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("two\n"), 0, ExpectAnyContent()); err == nil {
		t.Fatal("planning the same path twice must fail")
	}
}

// A transaction detects modifications made before it stages, not after. Two
// transactions racing on the same bytes can both stage and both win; the second
// one only conflicts once the first has already committed. This is the
// compensating-transaction boundary and is asserted here so it is not mistaken
// for a lock.
func TestFileTxnStalePlanConflictsAfterAnotherTransactionCommits(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"shared.md": "start\n"})
	path := filepath.Join(root, "shared.md")
	start := HashBytes([]byte("start\n"))

	first, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new first txn: %v", err)
	}
	if err := first.Write(path, []byte("first\n"), 0, ExpectHash(start)); err != nil {
		t.Fatalf("plan first: %v", err)
	}
	second, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new second txn: %v", err)
	}
	if err := second.Write(path, []byte("second\n"), 0, ExpectHash(start)); err != nil {
		t.Fatalf("plan second: %v", err)
	}

	if err := first.Commit(nil); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := second.Commit(nil); !IsTxnConflict(err) {
		t.Fatalf("second commit should conflict, got %v", err)
	}
	if got := readFileString(t, path); got != "first\n" {
		t.Fatalf("shared.md = %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestFileTxnConcurrentTransactionsOnDistinctPaths(t *testing.T) {
	root := txnTestRoot(t, nil)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("note-%d.md", i)
			txn, err := NewFileTxn(root)
			if err != nil {
				t.Errorf("new txn %d: %v", i, err)
				return
			}
			if err := txn.Write(filepath.Join(root, name), []byte(name+"\n"), 0, ExpectMissing()); err != nil {
				t.Errorf("plan %d: %v", i, err)
				return
			}
			if err := txn.Commit(&captureRecorder{}); err != nil {
				t.Errorf("commit %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		name := fmt.Sprintf("note-%d.md", i)
		if got := readFileString(t, filepath.Join(root, name)); got != name+"\n" {
			t.Fatalf("%s = %q", name, got)
		}
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestFileTxnConflictsOnNonRegularDestination(t *testing.T) {
	root := txnTestRoot(t, nil)
	dir := filepath.Join(root, "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(dir, []byte("nope\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := txn.Commit(nil); !IsTxnConflict(err) {
		t.Fatalf("writing over a directory must conflict, got %v", err)
	}
}

// TestClassifyStateReadErrorTreatsVanishedFileAsConflict pins the stat-then-read
// window in currentFileState: another writer deletes the destination, so the
// read reports the path missing even though the stat saw a regular file. Callers
// retry a conflict and surface anything else, so the classification is what
// keeps that lost race retryable.
func TestClassifyStateReadErrorTreatsVanishedFileAsConflict(t *testing.T) {
	path := filepath.Join("notes", "Inbox.md")

	vanished := classifyStateReadError(path, &os.PathError{Op: "open", Path: path, Err: syscall.ENOENT})
	if !IsTxnConflict(vanished) {
		t.Fatalf("a file that vanished between stat and read must conflict, got %v", vanished)
	}
	var conflict *TxnConflictError
	if !errors.As(vanished, &conflict) {
		t.Fatalf("errors.As: %v", vanished)
	}
	if conflict.Path != path || conflict.Expected != "file" || conflict.Actual != "missing" {
		t.Fatalf("conflict = %+v, want path=%q expected=file actual=missing", conflict, path)
	}

	denied := classifyStateReadError(path, &os.PathError{Op: "open", Path: path, Err: syscall.EACCES})
	if IsTxnConflict(denied) {
		t.Fatalf("an unreadable file is an I/O fault, not a conflict, got %v", denied)
	}
	if !errors.Is(denied, syscall.EACCES) {
		t.Fatalf("denied must wrap the read failure, got %v", denied)
	}
}

func TestFileTxnEmptyCommitIsNoop(t *testing.T) {
	root := txnTestRoot(t, nil)
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if txn.Len() != 0 {
		t.Fatalf("empty transaction length = %d", txn.Len())
	}
	if err := txn.Commit(&captureRecorder{err: errors.New("must not be called")}); err != nil {
		t.Fatalf("empty commit: %v", err)
	}
}

// A write that lands between staging and the exchange is still caught — but not
// by re-reading the destination first, which would have its own gap. The
// exchange itself hands back the bytes it displaced, so the check is made
// against the exact version this transaction overwrote. The interloper's save
// therefore survives at the destination and the transaction is the one that
// backs out, which is the opposite of what a check-then-write would do.
func TestFileTxnOverwriteRejectsAWriterThatLandsAfterStaging(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"c.md": "old-c\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectContent([]byte("old-a\n"))); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan c: %v", err)
	}

	// The editor saves after this transaction staged and verified, in the last
	// window a check-then-write would have left open.
	txn.hooks.beforeCommit = func(path string) error {
		if !strings.HasSuffix(path, "a.md") {
			return nil
		}
		return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
	}

	err = txn.Commit(&captureRecorder{})
	if !IsTxnConflict(err) {
		t.Fatalf("a writer that landed after staging must conflict, got %v", err)
	}
	if IsTxnRecoveryNeeded(err) {
		t.Fatalf("the writer's bytes went back with the same primitive, so nothing needs recovery: %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "editor-saved\n" {
		t.Fatalf("the concurrent writer must keep the path, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
		t.Fatalf("the rest of the transaction must not land, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("transaction temp files left behind: %v", leftovers)
	}
}
