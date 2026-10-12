package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTxnRollbackPreservesConcurrentPostCommitEdit(t *testing.T) {
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
	txn.hooks.beforeCommit = func(path string) error {
		if !strings.HasSuffix(path, "c.md") {
			return nil
		}
		// Another process saves a.md after this transaction applied it and
		// before the failure on the later path.
		if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("editor-saved\n"), 0o644); err != nil {
			return err
		}
		return commitErr
	}

	err = txn.Commit(nil)
	if !IsTxnRollbackFailure(err) {
		t.Fatalf("a refused rollback must be reported as a rollback failure, got %v", err)
	}
	var failure *TxnRollbackError
	if !errors.As(err, &failure) {
		t.Fatalf("error should unwrap to *TxnRollbackError, got %v", err)
	}
	if !errors.Is(err, commitErr) {
		t.Fatalf("rollback failure must preserve the commit cause, got %v", err)
	}
	if len(failure.Failed) != 1 || !strings.HasSuffix(failure.Failed[0], "a.md") {
		t.Fatalf("mixed paths should name a.md, got %v", failure.Failed)
	}
	if len(failure.RollbackErrs) != 1 || !IsTxnConflict(failure.RollbackErrs[0]) {
		t.Fatalf("the refusal should be a conflict, got %v", failure.RollbackErrs)
	}
	if len(failure.Restored) != 1 || !strings.HasSuffix(failure.Restored[0], "b.md") {
		t.Fatalf("restored paths should name b.md, got %v", failure.Restored)
	}
	if !strings.Contains(err.Error(), "a.md") {
		t.Fatalf("mixed-state error must name a.md, got %q", err.Error())
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "editor-saved\n" {
		t.Fatalf("the concurrent edit must survive rollback, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
		t.Fatalf("b.md should be rolled back, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
		t.Fatalf("c.md never applied, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// The committed-bytes check and the compensating write are separate filesystem
// operations. A writer that saves in that window still owns the path:
// beforeRestore runs after the check has already passed, so this is the
// interleaving a check-then-write rollback would silently overwrite.
func TestFileTxnRollbackPreservesEditAfterCommittedCheck(t *testing.T) {
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
	txn.hooks.beforeCommit = func(path string) error {
		if strings.HasSuffix(path, "c.md") {
			return commitErr
		}
		return nil
	}
	txn.hooks.beforeRestore = func(path string) error {
		if !strings.HasSuffix(path, "a.md") {
			return nil
		}
		// The editor saves after rollback confirmed a.md still holds this
		// transaction's bytes and before it replays the prior ones.
		return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
	}

	err = txn.Commit(nil)
	if !IsTxnRollbackFailure(err) {
		t.Fatalf("a refused rollback must be reported as a rollback failure, got %v", err)
	}
	var failure *TxnRollbackError
	if !errors.As(err, &failure) {
		t.Fatalf("error should unwrap to *TxnRollbackError, got %v", err)
	}
	if !errors.Is(err, commitErr) {
		t.Fatalf("rollback failure must preserve the commit cause, got %v", err)
	}
	if len(failure.Failed) != 1 || !strings.HasSuffix(failure.Failed[0], "a.md") {
		t.Fatalf("mixed paths should name a.md, got %v", failure.Failed)
	}
	if len(failure.RollbackErrs) != 1 || !IsTxnConflict(failure.RollbackErrs[0]) {
		t.Fatalf("the refusal should be a conflict, got %v", failure.RollbackErrs)
	}
	if len(failure.Restored) != 1 || !strings.HasSuffix(failure.Restored[0], "b.md") {
		t.Fatalf("restored paths should name b.md, got %v", failure.Restored)
	}
	if !strings.Contains(err.Error(), "a.md") {
		t.Fatalf("mixed-state error must name a.md, got %q", err.Error())
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "editor-saved\n" {
		t.Fatalf("the edit that landed after the check must survive, got %q", got)
	}
	info, err := os.Stat(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatalf("stat a.md: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("a.md mode = %v, want 0644", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
		t.Fatalf("b.md should be rolled back, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
		t.Fatalf("c.md never applied, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// A file that appears while rollback is putting a removed path back is not
// overwritten either: that compensating write expects a vacant destination, so
// it places exclusively rather than replacing.
func TestFileTxnRollbackRefusesPathRecreatedDuringPutBack(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"c.md": "old-c\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Remove(filepath.Join(root, "a.md"), ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan c: %v", err)
	}

	commitErr := errors.New("commit exploded")
	txn.hooks.beforeCommit = func(path string) error {
		if strings.HasSuffix(path, "c.md") {
			return commitErr
		}
		return nil
	}
	txn.hooks.beforeRestore = func(path string) error {
		if !strings.HasSuffix(path, "a.md") {
			return nil
		}
		// The removed path is recreated after rollback confirmed it was still
		// absent, so the compensating write must refuse it.
		return os.WriteFile(path, []byte("recreated\n"), 0o644)
	}

	err = txn.Commit(nil)
	var failure *TxnRollbackError
	if !errors.As(err, &failure) {
		t.Fatalf("error should unwrap to *TxnRollbackError, got %v", err)
	}
	if len(failure.Failed) != 1 || !strings.HasSuffix(failure.Failed[0], "a.md") {
		t.Fatalf("mixed paths should name a.md, got %v", failure.Failed)
	}
	if len(failure.RollbackErrs) != 1 {
		t.Fatalf("rollback errors = %#v, want one", failure.RollbackErrs)
	}
	var recovery *TxnRecoveryError
	if !errors.As(failure.RollbackErrs[0], &recovery) {
		t.Fatalf("rollback error = %v, want *TxnRecoveryError", failure.RollbackErrs[0])
	}
	if !recovery.Displaced || recovery.RecoveryPath == "" {
		t.Fatalf("recovery = %#v, want a displaced recovery path", recovery)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "recreated\n" {
		t.Fatalf("the recreated file must survive rollback, got %q", got)
	}
	if got := readFileString(t, recovery.RecoveryPath); got != "old-a\n" {
		t.Fatalf("the removed content must survive at the named recovery path, got %q", got)
	}
	leftovers := stagedTempFiles(t, root)
	if len(leftovers) != 1 || leftovers[0] != filepath.Base(recovery.RecoveryPath) {
		t.Fatalf("only the named recovery file may remain, got %v", leftovers)
	}
}

// A removal is compensated only while the path is still absent; a file another
// writer recreated at that path is left alone.
func TestFileTxnRollbackSkipsRecreatedRemovedPath(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"c.md": "old-c\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Remove(filepath.Join(root, "a.md"), ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan c: %v", err)
	}

	commitErr := errors.New("commit exploded")
	txn.hooks.beforeCommit = func(path string) error {
		if !strings.HasSuffix(path, "c.md") {
			return nil
		}
		if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("recreated\n"), 0o644); err != nil {
			return err
		}
		return commitErr
	}

	err = txn.Commit(nil)
	if !IsTxnRollbackFailure(err) {
		t.Fatalf("a refused rollback must be reported as a rollback failure, got %v", err)
	}
	var failure *TxnRollbackError
	if !errors.As(err, &failure) {
		t.Fatalf("error should unwrap to *TxnRollbackError, got %v", err)
	}
	if len(failure.Failed) != 1 || !strings.HasSuffix(failure.Failed[0], "a.md") {
		t.Fatalf("mixed paths should name a.md, got %v", failure.Failed)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "recreated\n" {
		t.Fatalf("the recreated file must survive rollback, got %q", got)
	}
}
