package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTxnCommitAppliesEveryChangeAndJournalsOnce(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
		"c.md": "doomed\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectContent([]byte("old-a\n"))); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "b.md"), []byte("new-b\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if err := txn.Remove(filepath.Join(root, "c.md"), ExpectAnyContent()); err != nil {
		t.Fatalf("plan c: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "d.md"), []byte("new-d\n"), 0, ExpectMissing()); err != nil {
		t.Fatalf("plan d: %v", err)
	}

	recorder := &captureRecorder{}
	if err := txn.Commit(recorder); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := readFileString(t, filepath.Join(root, "a.md")); got != "new-a\n" {
		t.Fatalf("a.md = %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "new-b\n" {
		t.Fatalf("b.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "c.md")); !os.IsNotExist(err) {
		t.Fatalf("c.md should be removed, stat err = %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "d.md")); got != "new-d\n" {
		t.Fatalf("d.md = %q", got)
	}
	if len(recorder.paths) != 4 {
		t.Fatalf("journal should capture every path once, got %v", recorder.paths)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestFileTxnPreservesExistingModeAndHonorsExplicitMode(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old\n"})
	if err := os.Chmod(filepath.Join(root, "a.md"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "b.md"), []byte("new\n"), 0o640, ExpectMissing()); err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if err := txn.Commit(nil); err != nil {
		t.Fatalf("commit: %v", err)
	}

	infoA, err := os.Stat(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatalf("stat a: %v", err)
	}
	if got := infoA.Mode().Perm(); got != 0o600 {
		t.Fatalf("a.md mode = %v, want 0600", got)
	}
	infoB, err := os.Stat(filepath.Join(root, "b.md"))
	if err != nil {
		t.Fatalf("stat b: %v", err)
	}
	if got := infoB.Mode().Perm(); got != 0o640 {
		t.Fatalf("b.md mode = %v, want 0640", got)
	}
}

func TestFileTxnConcurrentModificationConflictsAndWritesNothing(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectContent([]byte("old-a\n"))); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	// b.md is planned against content that another writer has already replaced.
	if err := txn.Write(filepath.Join(root, "b.md"), []byte("new-b\n"), 0, ExpectContent([]byte("old-b\n"))); err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.md"), []byte("someone-else\n"), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	recorder := &captureRecorder{}
	err = txn.Commit(recorder)
	if !IsTxnConflict(err) {
		t.Fatalf("commit should report a conflict, got %v", err)
	}
	var conflict *TxnConflictError
	if !errors.As(err, &conflict) || !strings.HasSuffix(conflict.Path, "b.md") {
		t.Fatalf("conflict should name b.md, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("a.md must be untouched, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "someone-else\n" {
		t.Fatalf("b.md must keep the concurrent write, got %q", got)
	}
	if len(recorder.paths) != 0 {
		t.Fatalf("no journal entry should be captured, got %v", recorder.paths)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestFileTxnJournalFailureBeforeCommitWritesNothing(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old-a\n"})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}

	sentinel := errors.New("journal unavailable")
	if err := txn.Commit(&captureRecorder{err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("commit should surface the journal failure, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("a.md must be untouched, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestFileTxnFailureDuringCommitRollsBackAppliedPaths(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
		"c.md": "old-c\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Remove(filepath.Join(root, "b.md"), ExpectAnyContent()); err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan c: %v", err)
	}

	sentinel := errors.New("disk gave up")
	txn.hooks.beforeCommit = func(path string) error {
		if strings.HasSuffix(path, "c.md") {
			return sentinel
		}
		return nil
	}

	if err := txn.Commit(nil); !errors.Is(err, sentinel) {
		t.Fatalf("commit should surface the apply failure, got %v", err)
	}
	if IsTxnRollbackFailure(err) {
		t.Fatalf("rollback succeeded, so the error must not be a rollback failure: %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("a.md should be rolled back, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
		t.Fatalf("removed b.md should be restored, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
		t.Fatalf("c.md never applied, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestFileTxnFailedRollbackNamesMixedPaths(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
		"c.md": "old-c\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan %s: %v", name, err)
		}
	}

	commitErr := errors.New("commit exploded")
	rollbackErr := errors.New("rollback exploded")
	txn.hooks.beforeCommit = func(path string) error {
		if strings.HasSuffix(path, "c.md") {
			return commitErr
		}
		return nil
	}
	txn.hooks.beforeRestore = func(path string) error {
		if strings.HasSuffix(path, "a.md") {
			return rollbackErr
		}
		return nil
	}

	err = txn.Commit(nil)
	if !IsTxnRollbackFailure(err) {
		t.Fatalf("failed rollback should be reported as such, got %v", err)
	}
	var failure *TxnRollbackError
	if !errors.As(err, &failure) {
		t.Fatalf("error should unwrap to *TxnRollbackError, got %v", err)
	}
	if !errors.Is(err, commitErr) {
		t.Fatalf("rollback failure must preserve the commit cause, got %v", err)
	}
	if len(failure.Failed) != 1 || !strings.HasSuffix(failure.Failed[0], "a.md") {
		t.Fatalf("failed paths should name a.md, got %v", failure.Failed)
	}
	if len(failure.Restored) != 1 || !strings.HasSuffix(failure.Restored[0], "b.md") {
		t.Fatalf("restored paths should name b.md, got %v", failure.Restored)
	}
	if !strings.Contains(err.Error(), "git diff") {
		t.Fatalf("mixed-state error must point at recovery, got %q", err.Error())
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "new-a.md\n" {
		t.Fatalf("a.md rollback failed, so it keeps the new content, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
		t.Fatalf("b.md should be rolled back, got %q", got)
	}
}

// An external writer that edits an already-applied path before a later path
// fails owns those bytes: compensation must not replay the prior content over
// them, and the transaction reports the path as mixed instead.
