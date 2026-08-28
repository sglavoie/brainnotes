package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Creating a file and putting a deleted one back are the two mutations whose
// destination is supposed to be absent, so both go through the same exclusive
// placement, whose primitive is a hard link. A vault on a filesystem without
// link support must lose neither, so the fallback is covered by failing the
// primitive itself rather than by hoping to find such a filesystem.
func TestFileTxnPlacementFallsBackWhenHardLinksAreUnavailable(t *testing.T) {
	linkErr := errors.New("link unsupported")

	t.Run("a create still lands", func(t *testing.T) {
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
		txn.hooks.hardLink = func(source, path string) error { return linkErr }

		if err := txn.Commit(&captureRecorder{}); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "z.md")); got != "new-z\n" {
			t.Fatalf("z.md = %q", got)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "new-a\n" {
			t.Fatalf("a.md = %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("transaction temp files left behind: %v", leftovers)
		}
	})

	t.Run("a rolled-back removal is put back", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{
			"b.md": "old-b\n",
			"c.md": "old-c\n",
		})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.Remove(filepath.Join(root, "b.md"), ExpectAnyContent()); err != nil {
			t.Fatalf("plan b: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan c: %v", err)
		}

		replaceErr := errors.New("no atomic replacement on this volume")
		txn.hooks.replaceExisting = func(source, path string) (string, error) {
			return "", replaceErr
		}
		txn.hooks.hardLink = func(source, path string) error { return linkErr }

		err = txn.Commit(&captureRecorder{})
		if !errors.Is(err, replaceErr) {
			t.Fatalf("commit should surface the replacement failure, got %v", err)
		}
		if IsTxnRecoveryNeeded(err) {
			t.Fatalf("the fallback put the file back, so nothing needs manual recovery: %v", err)
		}
		if IsTxnRollbackFailure(err) {
			t.Fatalf("rollback succeeded, so the error must not be a rollback failure: %v", err)
		}
		if got := readFileString(t, filepath.Join(root, "b.md")); got != "old-b\n" {
			t.Fatalf("the deleted file must be put back, got %q", got)
		}
		info, err := os.Stat(filepath.Join(root, "b.md"))
		if err != nil {
			t.Fatalf("stat b.md: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("b.md mode = %v, want 0644", got)
		}
		if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
			t.Fatalf("c.md was never applied, got %q", got)
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
		root := txnTestRoot(t, map[string]string{
			"b.md": "old-b\n",
			"c.md": "old-c\n",
		})
		t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.Remove(filepath.Join(root, "b.md"), ExpectAnyContent()); err != nil {
			t.Fatalf("plan b: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan c: %v", err)
		}

		replaceErr := errors.New("no atomic replacement on this volume")
		txn.hooks.replaceExisting = func(source, path string) (string, error) {
			return "", replaceErr
		}
		// The directory turns read-only once the deleted file's content has been
		// staged for the put-back, so the link fails and the fallback cannot
		// reserve the path either.
		txn.hooks.hardLink = func(source, path string) error {
			if err := os.Chmod(root, 0o555); err != nil {
				t.Fatalf("chmod root: %v", err)
			}
			return errors.New("link unsupported")
		}

		err = txn.Commit(&captureRecorder{})
		var rollback *TxnRollbackError
		if !errors.As(err, &rollback) {
			t.Fatalf("a failed put-back must be reported as a rollback failure, got %v", err)
		}
		if len(rollback.RollbackErrs) != 1 {
			t.Fatalf("rollback errors = %#v, want one", rollback.RollbackErrs)
		}
		var recovery *TxnRecoveryError
		if !errors.As(rollback.RollbackErrs[0], &recovery) {
			t.Fatalf("a failed put-back must report a *TxnRecoveryError, got %v", rollback.RollbackErrs[0])
		}
		if recovery.Displaced || recovery.Foreign {
			t.Fatalf("no writer took the path, so the recovery error must not report it displaced: %#v", recovery)
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
			t.Fatalf("the deleted content must be preserved, got %q", got)
		}
	})
}

// A writer can claim the vacated path after restoration's hard-link attempt
// fails but before its fallback runs. The fallback must be one atomic
// no-replace operation so that writer survives and the deleted original remains
// in named recovery custody.
func TestFileTxnRestorationPreservesWriterAfterReservation(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"b.md": "old-b\n",
		"c.md": "old-c\n",
	})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Remove(filepath.Join(root, "b.md"), ExpectAnyContent()); err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "c.md"), []byte("new-c\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan c: %v", err)
	}

	replaceErr := errors.New("no atomic replacement on this volume")
	txn.hooks.replaceExisting = func(source, path string) (string, error) {
		return "", replaceErr
	}
	txn.hooks.hardLink = func(source, path string) error {
		return errors.New("link unsupported")
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
	if got := readFileString(t, filepath.Join(root, "c.md")); got != "old-c\n" {
		t.Fatalf("the failed destination was changed: %q", got)
	}
	leftovers := stagedTempFiles(t, root)
	if len(leftovers) != 1 || leftovers[0] != filepath.Base(recovery.RecoveryPath) {
		t.Fatalf("only the named recovery file may remain, got %v", leftovers)
	}
}
