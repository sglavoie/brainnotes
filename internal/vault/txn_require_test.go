package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
