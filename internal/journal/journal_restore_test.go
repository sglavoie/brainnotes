package journal

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRestoreHandlesRewriteDeleteCreateAndMove(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")

	rewritePath := filepath.Join(vaultRoot, "Rewrite.md")
	deletePath := filepath.Join(vaultRoot, "Delete.md")
	createPath := filepath.Join(vaultRoot, "Created.md")
	moveSrc := filepath.Join(vaultRoot, "Move.md")
	moveDst := filepath.Join(vaultRoot, "Folder", "Move.md")

	writeFile(t, rewritePath, "rewrite-before\n")
	writeFile(t, deletePath, "delete-before\n")
	writeFile(t, moveSrc, "move-before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn mixed-op",
		WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-restore", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	for _, p := range []string{rewritePath, deletePath, createPath, moveSrc, moveDst} {
		if err := rec.Capture(p); err != nil {
			t.Fatalf("capture %q: %v", p, err)
		}
	}

	writeFile(t, rewritePath, "rewrite-after\n")
	if err := os.Remove(deletePath); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	writeFile(t, createPath, "created-after\n")
	if err := os.MkdirAll(filepath.Dir(moveDst), 0o755); err != nil {
		t.Fatalf("mkdir move dst: %v", err)
	}
	if err := os.Rename(moveSrc, moveDst); err != nil {
		t.Fatalf("rename move: %v", err)
	}

	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	plan, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if plan.EntryID != entry.ID {
		t.Fatalf("plan entry id = %q, want %q", plan.EntryID, entry.ID)
	}
	if len(plan.Changes) != 5 {
		t.Fatalf("len(changes) = %d, want 5", len(plan.Changes))
	}
	gotPaths := make([]string, len(plan.Changes))
	for i := range plan.Changes {
		gotPaths[i] = plan.Changes[i].Path
	}
	wantPaths := append([]string(nil), gotPaths...)
	sort.Strings(wantPaths)
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("changes not sorted by path: %v", gotPaths)
	}

	assertFile(t, rewritePath, "rewrite-before\n")
	assertFile(t, deletePath, "delete-before\n")
	assertMissing(t, createPath)
	assertFile(t, moveSrc, "move-before\n")
	assertMissing(t, moveDst)
}

func TestRestoreFailsFastOnConflict(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	rewritePath := filepath.Join(vaultRoot, "A.md")
	createPath := filepath.Join(vaultRoot, "B.md")

	writeFile(t, rewritePath, "before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithIDGenerator(func(time.Time) (string, error) { return "entry-conflict", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(rewritePath); err != nil {
		t.Fatalf("capture rewrite: %v", err)
	}
	if err := rec.Capture(createPath); err != nil {
		t.Fatalf("capture create: %v", err)
	}

	writeFile(t, rewritePath, "after\n")
	writeFile(t, createPath, "created\n")
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	writeFile(t, rewritePath, "after-external-edit\n")

	_, err = Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{})
	if err == nil {
		t.Fatal("restore should fail on conflict")
	}
	var conflictErr *RestoreConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("restore error type = %T, want *RestoreConflictError (%v)", err, err)
	}
	if len(conflictErr.Conflicts) != 1 {
		t.Fatalf("len(conflicts) = %d, want 1", len(conflictErr.Conflicts))
	}
	if conflictErr.Conflicts[0].Path != "A.md" {
		t.Fatalf("conflict path = %q, want A.md", conflictErr.Conflicts[0].Path)
	}

	assertFile(t, rewritePath, "after-external-edit\n")
	assertFile(t, createPath, "created\n")
}

func TestRestoreForceAppliesDespiteConflict(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	note := filepath.Join(vaultRoot, "A.md")
	writeFile(t, note, "before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithIDGenerator(func(time.Time) (string, error) { return "entry-force", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeFile(t, note, "after\n")
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	writeFile(t, note, "after-external-edit\n")

	if _, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{Force: true}); err != nil {
		t.Fatalf("restore --force: %v", err)
	}
	assertFile(t, note, "before\n")
}

func TestRestoreConflictErrorMessageDeterministic(t *testing.T) {
	err := &RestoreConflictError{
		EntryID: "entry-1",
		Conflicts: []RestoreConflict{
			{Path: "b.md", Expected: "file(sha256=111,size=3)", Actual: "missing"},
			{Path: "a.md", Expected: "missing", Actual: "file(sha256=222,size=4)"},
		},
	}
	msg := err.Error()
	if !strings.Contains(msg, "entry-1") {
		t.Fatalf("error should include entry id: %q", msg)
	}
	if !strings.Contains(msg, "b.md expected=file(sha256=111,size=3) actual=missing") ||
		!strings.Contains(msg, "a.md expected=missing actual=file(sha256=222,size=4)") {
		t.Fatalf("error missing conflict details: %q", msg)
	}
}
