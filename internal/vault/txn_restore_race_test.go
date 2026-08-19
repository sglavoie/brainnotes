package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
