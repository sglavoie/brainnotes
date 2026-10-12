package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestARolledBackStreamedCreateTakesItsFileBackOut(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 4096)
	attachment := filepath.Join(root, "attachments", "photo.png")
	note := filepath.Join(root, "zz-note.md")

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	// Commit order is path order, so the attachment lands and the note is the
	// change that fails — which is the ordering that exercises restoreCreated
	// against a change holding no bytes of its own.
	if err := txn.WriteFrom(attachment, source, size, digest, 0o644); err != nil {
		t.Fatalf("plan attachment: %v", err)
	}
	if err := txn.Write(note, []byte("body\n"), 0, ExpectMissing()); err != nil {
		t.Fatalf("plan note: %v", err)
	}
	boom := errors.New("placement refused")
	txn.hooks.placeNew = func(source, path string) error {
		if filepath.Base(path) == "zz-note.md" {
			return boom
		}
		return placeNew(nil, source, path)
	}
	if err := txn.Commit(nil); !errors.Is(err, boom) {
		t.Fatalf("commit = %v, want the injected failure", err)
	}
	if _, err := os.Stat(attachment); !os.IsNotExist(err) {
		t.Fatalf("the compensated attachment should be gone, stat = %v", err)
	}
	if _, err := os.Stat(note); !os.IsNotExist(err) {
		t.Fatalf("the failed note should not exist, stat = %v", err)
	}
}

func TestStreamedRemovalRollbackConflictPreservesPreimage(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old-a\n", "z.md": "old-z\n"})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.RemoveStreamed(filepath.Join(root, "a.md"), ExpectAnyContent()); err != nil {
		t.Fatal(err)
	}
	if err := txn.Write(filepath.Join(root, "z.md"), []byte("new-z\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatal(err)
	}
	txn.hooks.beforeCommit = func(path string) error {
		if filepath.Base(path) == "z.md" {
			return errors.New("stop after streamed remove")
		}
		return nil
	}
	txn.hooks.beforeRestore = func(path string) error {
		return os.WriteFile(path, []byte("foreign\n"), 0o644)
	}
	err = txn.Commit(nil)
	assertStreamedRecoveryCopy(t, err, "old-a\n")
}

func TestStreamedOverwriteRollbackConflictPreservesPreimage(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old-a\n", "z.md": "old-z\n"})
	source, digest, size := streamedSource(t, 4096)
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.ReplaceFrom(filepath.Join(root, "a.md"), source, size, digest, 0o644, ExpectAnyContent()); err != nil {
		t.Fatal(err)
	}
	if err := txn.Write(filepath.Join(root, "z.md"), []byte("new-z\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatal(err)
	}
	txn.hooks.beforeCommit = func(path string) error {
		if filepath.Base(path) == "z.md" {
			return errors.New("stop after streamed overwrite")
		}
		return nil
	}
	txn.hooks.beforeRestore = func(path string) error {
		return os.WriteFile(path, []byte("foreign\n"), 0o644)
	}
	err = txn.Commit(nil)
	assertStreamedRecoveryCopy(t, err, "old-a\n")
}

func TestStreamedOverwriteRollbackStrandPreservesForeignBytes(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old-a\n", "z.md": "old-z\n"})
	source, digest, size := streamedSource(t, 4096)
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.ReplaceFrom(filepath.Join(root, "a.md"), source, size, digest, 0o644, ExpectAnyContent()); err != nil {
		t.Fatal(err)
	}
	if err := txn.Write(filepath.Join(root, "z.md"), []byte("new-z\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatal(err)
	}
	txn.hooks.beforeCommit = func(path string) error {
		if filepath.Base(path) == "z.md" {
			return errors.New("stop after streamed overwrite")
		}
		return nil
	}
	txn.hooks.beforeRestore = func(path string) error {
		return os.WriteFile(path, []byte("foreign\n"), 0o644)
	}
	txn.hooks.beforeConflictRestore = func(string) error {
		return errors.New("cannot swap foreign bytes back")
	}
	err = txn.Commit(nil)
	recovery := assertStrandedForeignRecovery(t, err)
	if got := readFileString(t, recovery.RecoveryPath); got != "foreign\n" {
		t.Fatalf("foreign recovery copy = %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "old-a\n" {
		t.Fatalf("destination after stranded rollback = %q, want old preimage", got)
	}
}

func assertStrandedForeignRecovery(t *testing.T, err error) *TxnRecoveryError {
	t.Helper()
	var rollback *TxnRollbackError
	if !errors.As(err, &rollback) || len(rollback.RollbackErrs) != 1 {
		t.Fatalf("Commit = %v, want one rollback error", err)
	}
	var recovery *TxnRecoveryError
	if !errors.As(rollback.RollbackErrs[0], &recovery) || !recovery.Foreign {
		t.Fatalf("rollback error = %v, want stranded foreign recovery", rollback.RollbackErrs[0])
	}
	return recovery
}

func assertStreamedRecoveryCopy(t *testing.T, err error, want string) {
	t.Helper()
	var rollback *TxnRollbackError
	if !errors.As(err, &rollback) {
		t.Fatalf("Commit = %v, want a rollback failure", err)
	}
	var recovery *TxnRecoveryError
	for _, rollbackErr := range rollback.RollbackErrs {
		if errors.As(rollbackErr, &recovery) {
			break
		}
	}
	if recovery == nil || recovery.RecoveryPath == "" {
		t.Fatalf("Commit = %v, want a named streamed recovery copy", err)
	}
	if got := readFileString(t, recovery.RecoveryPath); got != want {
		t.Fatalf("recovery copy = %q, want %q", got, want)
	}
}

func TestRecoveringAStreamedCreateFinishesItFromTheStagedCopy(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 4096)
	dest := filepath.Join(root, "attachments", "photo.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	staged, err := stageTempFileFrom(dest, source, size, digest, 0o644)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	change := txnIntentChange{
		Path:      dest,
		Staged:    staged,
		Mode:      0o644,
		NewSHA256: digest,
		NewSize:   size,
	}

	if err := recoverTxnIntentChange(change); err != nil {
		t.Fatalf("recover: %v", err)
	}
	assertFileDigest(t, dest, digest, size)

	// Recovery runs again on every later Open and mutation, so the second pass
	// has to be a no-op rather than a second placement.
	if err := recoverTxnIntentChange(change); err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("a reached destination should leave no staged copy, stat = %v", err)
	}
}

func TestRecoveryLeavesADestinationAnotherWriterOwns(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 1024)
	dest := filepath.Join(root, "attachments", "photo.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	staged, err := stageTempFileFrom(dest, source, size, digest, 0o644)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := os.WriteFile(dest, []byte("not ours"), 0o644); err != nil {
		t.Fatalf("interloper write: %v", err)
	}
	change := txnIntentChange{Path: dest, Staged: staged, Mode: 0o644, NewSHA256: digest, NewSize: size}

	if err := recoverTxnIntentChange(change); err != nil {
		t.Fatalf("recover should decide, not fail: %v", err)
	}
	if got := readFileString(t, dest); got != "not ours" {
		t.Fatalf("destination = %q, the other writer's file must survive", got)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("the staged bytes exist nowhere else and must be kept: %v", err)
	}
}

func TestWriteFromRefusesAPlanItCannotVerify(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 32)
	dest := filepath.Join(root, "attachments", "photo.png")
	cases := []struct {
		name   string
		source string
		size   int64
		digest string
	}{
		{"no source", "   ", size, digest},
		{"relative source", "upload.bin", size, digest},
		{"no digest", source, size, ""},
		{"short digest", source, size, "abc123"},
		{"negative size", source, -1, digest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txn, err := NewFileTxn(root)
			if err != nil {
				t.Fatalf("new txn: %v", err)
			}
			if err := txn.WriteFrom(dest, tc.source, tc.size, tc.digest, 0o644); err == nil {
				t.Fatal("plan should be refused before anything is staged")
			}
		})
	}
}
