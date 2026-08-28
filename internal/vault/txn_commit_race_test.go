package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// A replacement that fails must fail before it moves anything: the destination
// keeps its content, its mode, and — the property this whole layer exists for —
// its name, which never stops resolving for a moment.
func TestFileTxnCommitLeavesDestinationIntactWhenReplacementIsRefused(t *testing.T) {
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

	t.Run("keeps the destination and rolls back earlier changes", func(t *testing.T) {
		root := txnTestRoot(t, files)
		txn := plan(t, root)

		replaceErr := errors.New("no atomic replacement on this volume")
		txn.hooks.replaceExisting = func(source, path string) (string, error) {
			if strings.HasSuffix(path, "c.md") {
				return "", replaceErr
			}
			return replaceExisting(source, path)
		}

		err := txn.Commit(&captureRecorder{})
		if !errors.Is(err, replaceErr) {
			t.Fatalf("commit should surface the replacement failure, got %v", err)
		}
		if IsTxnRecoveryNeeded(err) {
			t.Fatalf("a refused replacement moves nothing, so nothing needs recovery: %v", err)
		}
		if IsTxnRollbackFailure(err) {
			t.Fatalf("rollback succeeded, so the error must not be a rollback failure: %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
			t.Fatalf("the destination must be untouched, got %q", got)
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

	t.Run("a refusal on an unsupported volume is typed and names the path", func(t *testing.T) {
		root := txnTestRoot(t, files)
		txn := plan(t, root)

		txn.hooks.replaceExisting = func(source, path string) (string, error) {
			if !strings.HasSuffix(path, "c.md") {
				return replaceExisting(source, path)
			}
			return "", &UnsafeReplacementError{Path: path, Reason: "the volume could not be proved"}
		}

		err := txn.Commit(&captureRecorder{})
		if !IsUnsafeReplacement(err) {
			t.Fatalf("commit should surface an unsafe-replacement refusal, got %v", err)
		}
		if !strings.Contains(err.Error(), "c.md") {
			t.Fatalf("the refusal must name the destination, got %q", err.Error())
		}
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
			t.Fatalf("a refused volume must not change the destination, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("transaction temp files left behind: %v", leftovers)
		}
	})

	t.Run("names the destination and the displaced copy when the put-back is refused", func(t *testing.T) {
		root := txnTestRoot(t, files)
		txn := plan(t, root)

		// The editor saves after the destination check passed, so the exchange
		// displaces its bytes; the put-back that would return them then fails.
		txn.hooks.beforeCommit = func(path string) error {
			if !strings.HasSuffix(path, "c.md") {
				return nil
			}
			return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
		}
		restoreErr := errors.New("put-back interrupted")
		txn.hooks.beforeConflictRestore = func(path string) error {
			if !strings.HasSuffix(path, "c.md") {
				return nil
			}
			return restoreErr
		}

		err := txn.Commit(&captureRecorder{})
		var recovery *TxnRecoveryError
		if !errors.As(err, &recovery) {
			t.Fatalf("a refused put-back must report a *TxnRecoveryError, got %v", err)
		}
		if !IsTxnRecoveryNeeded(err) {
			t.Fatalf("IsTxnRecoveryNeeded should be true for %v", err)
		}
		if !recovery.Foreign {
			t.Fatalf("the stranded content belongs to another writer: %#v", recovery)
		}
		if !strings.HasSuffix(recovery.Path, "c.md") {
			t.Fatalf("recovery should name c.md, got %q", recovery.Path)
		}
		if !strings.Contains(err.Error(), recovery.Path) || !strings.Contains(err.Error(), recovery.RecoveryPath) {
			t.Fatalf("mixed-state error must name the destination and the recovery path, got %q", err.Error())
		}
		if got := readFileString(t, recovery.RecoveryPath); got != "editor-saved\n" {
			t.Fatalf("the other writer's version must be preserved, got %q", got)
		}
		// Rollback still runs, and it finds c.md holding exactly this
		// transaction's bytes, so the pre-transaction content goes back.
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
			t.Fatalf("c.md should be rolled back, got %q", got)
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
		txn.hooks.placeNew = func(source, path string) error {
			if strings.HasSuffix(path, "z.md") {
				return placeErr
			}
			return placeNew(nil, source, path)
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
