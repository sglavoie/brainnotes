package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func txnTestRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func stagedTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var leftovers []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".txn-") {
			leftovers = append(leftovers, entry.Name())
		}
	}
	return leftovers
}

type captureRecorder struct {
	paths []string
	err   error
}

func (c *captureRecorder) Capture(path string) error {
	if c.err != nil {
		return c.err
	}
	c.paths = append(c.paths, path)
	return nil
}

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

// A file that appears while rollback holds the path in custody is not
// overwritten either: the compensating write is an exclusive create.
func TestFileTxnRollbackRefusesPathRecreatedDuringCustody(t *testing.T) {
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
		t.Fatalf("recovery = %#v, want displaced named custody", recovery)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "recreated\n" {
		t.Fatalf("the recreated file must survive rollback, got %q", got)
	}
	if got := readFileString(t, recovery.RecoveryPath); got != "old-a\n" {
		t.Fatalf("the displaced original must survive in recovery custody, got %q", got)
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

// A write that lands between staging and the rename is still caught: the
// destination is re-verified immediately before it is replaced.
func TestFileTxnDetectsWriteBetweenStagingAndRename(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "b.md"), []byte("new-b\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan b: %v", err)
	}

	// The journal capture stands in for everything that happens after staging
	// and before the first rename.
	err = txn.Commit(interloperRecorder{
		inner: &captureRecorder{},
		on:    "b.md",
	})
	if !IsTxnConflict(err) {
		t.Fatalf("commit should conflict, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("a.md should be rolled back, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "someone-else\n" {
		t.Fatalf("b.md must keep the interloper's bytes, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// The destination check and the mutation that follows it are separate
// filesystem operations. beforeCommit runs after the check has already passed,
// so it reproduces the save a check-then-write commit would overwrite or
// delete: the external bytes must survive and the transaction must conflict.
func TestFileTxnCommitPreservesDestinationChangeAfterValidation(t *testing.T) {
	t.Run("write", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{
			"a.md": "old-a\n",
			"b.md": "old-b\n",
		})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan a: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "b.md"), []byte("new-b\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan b: %v", err)
		}
		txn.hooks.beforeCommit = func(path string) error {
			if !strings.HasSuffix(path, "b.md") {
				return nil
			}
			return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
		}

		err = txn.Commit(&captureRecorder{})
		if !IsTxnConflict(err) {
			t.Fatalf("commit should conflict, got %v", err)
		}
		var conflict *TxnConflictError
		if !errors.As(err, &conflict) || !strings.HasSuffix(conflict.Path, "b.md") {
			t.Fatalf("conflict should name b.md, got %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "b.md")); got != "editor-saved\n" {
			t.Fatalf("the edit that landed after the check must survive, got %q", got)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
			t.Fatalf("a.md should be rolled back, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("staged temp files left behind: %v", leftovers)
		}
	})

	t.Run("removal", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{
			"a.md": "old-a\n",
			"b.md": "old-b\n",
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
		txn.hooks.beforeCommit = func(path string) error {
			if !strings.HasSuffix(path, "b.md") {
				return nil
			}
			return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
		}

		err = txn.Commit(&captureRecorder{})
		if !IsTxnConflict(err) {
			t.Fatalf("commit should conflict, got %v", err)
		}
		var conflict *TxnConflictError
		if !errors.As(err, &conflict) || !strings.HasSuffix(conflict.Path, "b.md") {
			t.Fatalf("conflict should name b.md, got %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "b.md")); got != "editor-saved\n" {
			t.Fatalf("the edit must not be deleted by the removal, got %q", got)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
			t.Fatalf("a.md should be rolled back, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("staged temp files left behind: %v", leftovers)
		}
	})

	t.Run("uncontended commit still applies", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{
			"a.md": "old-a\n",
			"b.md": "old-b\n",
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
		if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectMissing()); err != nil {
			t.Fatalf("plan c: %v", err)
		}

		if err := txn.Commit(&captureRecorder{}); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "new-a\n" {
			t.Fatalf("a.md = %q", got)
		}
		if _, err := os.Stat(filepath.Join(root, "b.md")); !os.IsNotExist(err) {
			t.Fatalf("b.md should be removed, stat err = %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "new-c\n" {
			t.Fatalf("c.md = %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("staged temp files left behind: %v", leftovers)
		}
	})
}

// Replacing a destination moves it into custody before the replacement is
// placed, so a placement failure leaves the path vacant with its only copy in a
// temp file. The transaction must put that copy back rather than report an
// error while the note is missing from the vault.
func TestFileTxnCommitRecoversAfterPlacementFailure(t *testing.T) {
	plan := func(t *testing.T, root string) *FileTxn {
		t.Helper()
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		for _, name := range []string{"a.md", "b.md", "c.md"} {
			if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
				t.Fatalf("plan %s: %v", name, err)
			}
		}
		return txn
	}
	files := map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
		"c.md": "old-c\n",
	}

	t.Run("restores the destination and rolls back earlier changes", func(t *testing.T) {
		root := txnTestRoot(t, files)
		txn := plan(t, root)

		placeErr := errors.New("link unsupported")
		txn.hooks.placeReplacement = func(source, path string) error {
			if strings.HasSuffix(path, "c.md") {
				return placeErr
			}
			return txn.linkExclusive(source, path)
		}

		err := txn.Commit(&captureRecorder{})
		if !errors.Is(err, placeErr) {
			t.Fatalf("commit should surface the placement failure, got %v", err)
		}
		if IsTxnRecoveryNeeded(err) {
			t.Fatalf("a recovered placement failure must not ask for manual recovery: %v", err)
		}
		if IsTxnRollbackFailure(err) {
			t.Fatalf("rollback succeeded, so the error must not be a rollback failure: %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
			t.Fatalf("the destination must be put back, got %q", got)
		}
		info, err := os.Stat(filepath.Join(root, "c.md"))
		if err != nil {
			t.Fatalf("stat c.md: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("c.md mode = %v, want 0644", got)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
			t.Fatalf("a.md should be rolled back, got %q", got)
		}
		if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
			t.Fatalf("b.md should be rolled back, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("transaction temp files left behind: %v", leftovers)
		}
	})

	t.Run("names the destination and recovery file when the put-back is refused", func(t *testing.T) {
		root := txnTestRoot(t, files)
		txn := plan(t, root)

		// The destination is created by another writer while it is vacant, so
		// neither the replacement nor the put-back can be placed there.
		txn.hooks.placeReplacement = func(source, path string) error {
			if !strings.HasSuffix(path, "c.md") {
				return txn.linkExclusive(source, path)
			}
			if err := os.WriteFile(path, []byte("editor-saved\n"), 0o644); err != nil {
				return err
			}
			return txn.linkExclusive(source, path)
		}

		err := txn.Commit(&captureRecorder{})
		var recovery *TxnRecoveryError
		if !errors.As(err, &recovery) {
			t.Fatalf("a refused put-back must report a *TxnRecoveryError, got %v", err)
		}
		if !IsTxnRecoveryNeeded(err) {
			t.Fatalf("IsTxnRecoveryNeeded should be true for %v", err)
		}
		if !strings.HasSuffix(recovery.Path, "c.md") {
			t.Fatalf("recovery should name c.md, got %q", recovery.Path)
		}
		if !strings.Contains(err.Error(), recovery.Path) || !strings.Contains(err.Error(), recovery.RecoveryPath) {
			t.Fatalf("mixed-state error must name the destination and the recovery path, got %q", err.Error())
		}
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "editor-saved\n" {
			t.Fatalf("the file that took the vacant path must survive, got %q", got)
		}
		if got := readFileString(t, recovery.RecoveryPath); got != "old-c\n" {
			t.Fatalf("the displaced content must be preserved, got %q", got)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
			t.Fatalf("a.md should be rolled back, got %q", got)
		}
		if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
			t.Fatalf("b.md should be rolled back, got %q", got)
		}
		leftovers := stagedTempFiles(t, root)
		if len(leftovers) != 1 || leftovers[0] != filepath.Base(recovery.RecoveryPath) {
			t.Fatalf("the recovery file must be the only temp file left and it must be named in the error, got %v", leftovers)
		}
	})

	t.Run("a new file that fails to place strands nothing", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{"a.md": "old-a\n"})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan a: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "z.md"), []byte("new-z\n"), 0, ExpectMissing()); err != nil {
			t.Fatalf("plan z: %v", err)
		}

		placeErr := errors.New("link unsupported")
		txn.hooks.placeReplacement = func(source, path string) error {
			if strings.HasSuffix(path, "z.md") {
				return placeErr
			}
			return txn.linkExclusive(source, path)
		}

		err = txn.Commit(&captureRecorder{})
		if !errors.Is(err, placeErr) {
			t.Fatalf("commit should surface the placement failure, got %v", err)
		}
		if IsTxnRecoveryNeeded(err) {
			t.Fatalf("a path that held nothing needs no recovery file: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "z.md")); !os.IsNotExist(err) {
			t.Fatalf("z.md should not exist, stat err = %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
			t.Fatalf("a.md should be rolled back, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("transaction temp files left behind: %v", leftovers)
		}
	})
}

// Replacement placement and restoration both start from the same hard link, so
// a filesystem that cannot link would fail the put-back for exactly the reason
// it failed the write. Failing the primitive itself is what proves restoration
// no longer depends on it.
func TestFileTxnCommitRecoversWhenHardLinkPlacementFails(t *testing.T) {
	t.Run("restores the destination and rolls back without the failed primitive", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{
			"a.md": "old-a\n",
			"b.md": "old-b\n",
		})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		for _, name := range []string{"a.md", "b.md"} {
			if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
				t.Fatalf("plan %s: %v", name, err)
			}
		}

		linkErr := errors.New("link unsupported")
		unsupported := false
		txn.hooks.hardLink = func(source, path string) error {
			if strings.HasSuffix(path, "b.md") {
				unsupported = true
			}
			if !unsupported {
				return os.Link(source, path)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Errorf("placement fails while %s is vacant, stat err = %v", path, statErr)
			}
			return linkErr
		}

		err = txn.Commit(&captureRecorder{})
		if !errors.Is(err, linkErr) {
			t.Fatalf("commit should surface the link failure, got %v", err)
		}
		if IsTxnRecoveryNeeded(err) {
			t.Fatalf("a recovered placement failure must not ask for manual recovery: %v", err)
		}
		if IsTxnRollbackFailure(err) {
			t.Fatalf("rollback succeeded, so the error must not be a rollback failure: %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
			t.Fatalf("the destination must be put back, got %q", got)
		}
		info, err := os.Stat(filepath.Join(root, "b.md"))
		if err != nil {
			t.Fatalf("stat b.md: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("b.md mode = %v, want 0644", got)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
			t.Fatalf("a.md should be rolled back, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("transaction temp files left behind: %v", leftovers)
		}
	})

	t.Run("a blocked put-back reports the destination missing not taken", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("directory permissions do not block file creation on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		root := txnTestRoot(t, map[string]string{"b.md": "old-b\n"})
		t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "b.md"), []byte("new-b\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan b: %v", err)
		}

		// The directory turns read-only once the destination is in custody, so
		// the link fails and the fallback cannot reserve the path either.
		linkErr := errors.New("link unsupported")
		txn.hooks.hardLink = func(source, path string) error {
			if err := os.Chmod(root, 0o555); err != nil {
				t.Fatalf("chmod root: %v", err)
			}
			return linkErr
		}

		err = txn.Commit(&captureRecorder{})
		var recovery *TxnRecoveryError
		if !errors.As(err, &recovery) {
			t.Fatalf("a failed put-back must report a *TxnRecoveryError, got %v", err)
		}
		if recovery.Displaced {
			t.Fatal("no writer took the path, so the recovery error must not report it displaced")
		}
		if strings.Contains(err.Error(), "another writer") {
			t.Fatalf("a vacant destination must not be reported as taken by another writer, got %q", err.Error())
		}
		if !strings.Contains(err.Error(), recovery.Path) || !strings.Contains(err.Error(), recovery.RecoveryPath) {
			t.Fatalf("mixed-state error must name the destination and the recovery path, got %q", err.Error())
		}
		if _, statErr := os.Stat(filepath.Join(root, "b.md")); !os.IsNotExist(statErr) {
			t.Fatalf("the destination is absent in this failure, stat err = %v", statErr)
		}
		if got := readFileString(t, recovery.RecoveryPath); got != "old-b\n" {
			t.Fatalf("the displaced content must be preserved, got %q", got)
		}
	})
}

// A writer can claim the destination after restoration's hard-link attempt
// fails but before its fallback runs. The fallback must be one atomic
// no-replace operation so that writer survives and the displaced original
// remains in named recovery custody.
func TestFileTxnRestorationPreservesWriterAfterReservation(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	for _, name := range []string{"a.md", "b.md"} {
		if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan %s: %v", name, err)
		}
	}

	linkErr := errors.New("link unsupported")
	hardLinksUnavailable := false
	txn.hooks.hardLink = func(source, path string) error {
		if strings.HasSuffix(path, "b.md") {
			hardLinksUnavailable = true
		}
		if hardLinksUnavailable {
			return linkErr
		}
		return os.Link(source, path)
	}
	writerRan := false
	txn.hooks.beforeFallbackPlacement = func(source, path string) error {
		if !strings.HasSuffix(path, "b.md") {
			return nil
		}
		writerRan = true
		return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
	}

	err = txn.Commit(&captureRecorder{})
	if !writerRan {
		t.Fatal("writer hook did not run at fallback placement")
	}
	var recovery *TxnRecoveryError
	if !errors.As(err, &recovery) {
		t.Fatalf("commit error = %v, want *TxnRecoveryError", err)
	}
	if !recovery.Displaced {
		t.Fatalf("recovery.Displaced = false, want true: %v", recovery)
	}
	if !strings.HasSuffix(recovery.Path, string(filepath.Separator)+"b.md") {
		t.Fatalf("recovery.Path = %q, want b.md", recovery.Path)
	}
	if recovery.RecoveryPath == "" || !strings.Contains(err.Error(), recovery.Path) || !strings.Contains(err.Error(), recovery.RecoveryPath) {
		t.Fatalf("recovery error must name both paths: %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "editor-saved\n" {
		t.Fatalf("writer destination = %q, want editor-saved", got)
	}
	if got := readFileString(t, recovery.RecoveryPath); got != "old-b\n" {
		t.Fatalf("recovery source = %q, want old-b", got)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("earlier change was not rolled back: %q", got)
	}
	leftovers := stagedTempFiles(t, root)
	if len(leftovers) != 1 || leftovers[0] != filepath.Base(recovery.RecoveryPath) {
		t.Fatalf("only the named recovery file may remain, got %v", leftovers)
	}
}

func TestFileTxnRollbackRestorationPreservesWriterDuringFallback(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	for _, name := range []string{"a.md", "b.md"} {
		if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan %s: %v", name, err)
		}
	}

	linkErr := errors.New("link unsupported")
	hardLinksUnavailable := false
	txn.hooks.hardLink = func(source, path string) error {
		if strings.HasSuffix(path, "b.md") {
			hardLinksUnavailable = true
		}
		if hardLinksUnavailable {
			return linkErr
		}
		return os.Link(source, path)
	}
	writerRan := false
	txn.hooks.beforeFallbackPlacement = func(source, path string) error {
		if !strings.HasSuffix(path, "a.md") {
			return nil
		}
		writerRan = true
		return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
	}

	err = txn.Commit(&captureRecorder{})
	if !writerRan {
		t.Fatal("writer hook did not run during rollback fallback placement")
	}
	var rollback *TxnRollbackError
	if !errors.As(err, &rollback) {
		t.Fatalf("commit error = %v, want *TxnRollbackError", err)
	}
	if len(rollback.RollbackErrs) != 1 {
		t.Fatalf("rollback errors = %#v, want one", rollback.RollbackErrs)
	}
	var recovery *TxnRecoveryError
	if !errors.As(rollback.RollbackErrs[0], &recovery) {
		t.Fatalf("rollback error = %v, want *TxnRecoveryError", rollback.RollbackErrs[0])
	}
	if !recovery.Displaced {
		t.Fatalf("recovery.Displaced = false, want true: %v", recovery)
	}
	if !strings.HasSuffix(recovery.Path, string(filepath.Separator)+"a.md") {
		t.Fatalf("recovery.Path = %q, want a.md", recovery.Path)
	}
	if recovery.RecoveryPath == "" || !strings.Contains(err.Error(), recovery.Path) || !strings.Contains(err.Error(), recovery.RecoveryPath) {
		t.Fatalf("rollback error must name both recovery paths: %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "editor-saved\n" {
		t.Fatalf("writer destination = %q, want editor-saved", got)
	}
	if got := readFileString(t, recovery.RecoveryPath); got != "old-a\n" {
		t.Fatalf("recovery source = %q, want old-a", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
		t.Fatalf("failed destination was not restored: %q", got)
	}
	leftovers := stagedTempFiles(t, root)
	if len(leftovers) != 1 || leftovers[0] != filepath.Base(recovery.RecoveryPath) {
		t.Fatalf("only the named recovery file may remain, got %v", leftovers)
	}
}

func TestRenameNoReplace(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("atomic no-replace rename is unsupported on this platform")
	}

	t.Run("vacant destination", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		destination := filepath.Join(root, "destination")
		if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if err := renameNoReplace(source, destination); err != nil {
			t.Fatalf("renameNoReplace: %v", err)
		}
		if _, err := os.Stat(source); !os.IsNotExist(err) {
			t.Fatalf("source should have moved, stat err = %v", err)
		}
		if got := readFileString(t, destination); got != "source" {
			t.Fatalf("destination = %q", got)
		}
	})

	t.Run("occupied destination", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		destination := filepath.Join(root, "destination")
		if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if err := os.WriteFile(destination, []byte("destination"), 0o644); err != nil {
			t.Fatalf("write destination: %v", err)
		}
		if err := renameNoReplace(source, destination); !os.IsExist(err) {
			t.Fatalf("renameNoReplace error = %v, want destination-exists", err)
		}
		if got := readFileString(t, source); got != "source" {
			t.Fatalf("source = %q", got)
		}
		if got := readFileString(t, destination); got != "destination" {
			t.Fatalf("destination = %q", got)
		}
	})
}

func TestFileTxnRequireConflictsWhenDependencyChangedBeforeStaging(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md":            "old-a\n",
		"brainnotes.toml": "type = \"string\"\n",
	})
	dependency := filepath.Join(root, "brainnotes.toml")
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Require(dependency, ExpectContent([]byte("type = \"string\"\n"))); err != nil {
		t.Fatalf("require dependency: %v", err)
	}

	if err := os.WriteFile(dependency, []byte("type = \"list\"\n"), 0o644); err != nil {
		t.Fatalf("rewrite dependency: %v", err)
	}

	if err := txn.Commit(&captureRecorder{err: errors.New("journal must not run")}); !IsTxnConflict(err) {
		t.Fatalf("commit should conflict on the changed dependency, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("a.md must be untouched, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// A dependency that changes after staging is caught too: the requirement is
// re-verified before every rename, so the transaction stops and compensates
// instead of writing the remaining paths under the new content.
func TestFileTxnRequireDetectsDependencyChangeBetweenStagingAndRename(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md":            "old-a\n",
		"b.md":            "old-b\n",
		"brainnotes.toml": "type = \"string\"\n",
	})
	dependency := filepath.Join(root, "brainnotes.toml")
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "b.md"), []byte("new-b\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if err := txn.Require(dependency, ExpectContent([]byte("type = \"string\"\n"))); err != nil {
		t.Fatalf("require dependency: %v", err)
	}
	txn.hooks.beforeCommit = func(path string) error {
		if strings.HasSuffix(path, "a.md") {
			return os.WriteFile(dependency, []byte("type = \"list\"\n"), 0o644)
		}
		return nil
	}

	if err := txn.Commit(&captureRecorder{}); !IsTxnConflict(err) {
		t.Fatalf("commit should conflict on the changed dependency, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("a.md should be rolled back, got %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
		t.Fatalf("b.md never applied, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// The requirement check and the rename are separate filesystem operations, so a
// dependency replaced between them decided nothing about the bytes that land.
// beforeCommit runs after the check has already passed, which is the
// interleaving a check-then-rename commit would leave on disk: the change must
// be compensated rather than remain committed under the new content.
func TestFileTxnRequiredFileChangesAfterValidation(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md":            "old-a\n",
		"brainnotes.toml": "type = \"string\"\n",
	})
	dependency := filepath.Join(root, "brainnotes.toml")
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	if err := txn.Require(dependency, ExpectContent([]byte("type = \"string\"\n"))); err != nil {
		t.Fatalf("require dependency: %v", err)
	}
	txn.hooks.beforeCommit = func(string) error {
		return os.WriteFile(dependency, []byte("type = \"list\"\n"), 0o644)
	}

	err = txn.Commit(&captureRecorder{})
	if !IsTxnConflict(err) {
		t.Fatalf("commit should conflict on the changed dependency, got %v", err)
	}
	var conflict *TxnConflictError
	if !errors.As(err, &conflict) || !strings.HasSuffix(conflict.Path, "brainnotes.toml") {
		t.Fatalf("conflict should name the dependency, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("the note must not stay committed under the replaced dependency, got %q", got)
	}
	if got := readFileString(t, dependency); got != "type = \"list\"\n" {
		t.Fatalf("the dependency is never rolled back, got %q", got)
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// A path cannot be both written and pinned: the write would make its own
// requirement fail the moment it applied.
func TestFileTxnRejectsRequiringAPlannedPath(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"brainnotes.toml": "type = \"string\"\n"})
	dependency := filepath.Join(root, "brainnotes.toml")

	written, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := written.Write(dependency, []byte("type = \"list\"\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan dependency: %v", err)
	}
	if err := written.Require(dependency, ExpectAnyContent()); err == nil {
		t.Fatal("requiring a path the transaction writes must fail")
	}

	required, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := required.Require(dependency, ExpectAnyContent()); err != nil {
		t.Fatalf("require dependency: %v", err)
	}
	if err := required.Write(dependency, []byte("type = \"list\"\n"), 0, ExpectAnyContent()); err == nil {
		t.Fatal("writing a path the transaction requires must fail")
	}
}

// interloperRecorder overwrites one of the transaction's paths from inside the
// journal capture, which is the widest part of the window between staging and
// the first rename.
type interloperRecorder struct {
	inner *captureRecorder
	on    string
}

func (r interloperRecorder) Capture(path string) error {
	if err := r.inner.Capture(path); err != nil {
		return err
	}
	if strings.HasSuffix(path, r.on) {
		return os.WriteFile(path, []byte("someone-else\n"), 0o644)
	}
	return nil
}
