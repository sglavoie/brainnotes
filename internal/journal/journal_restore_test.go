package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
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

// TestRestoreForceRefusesWhenTheAuthorizedStateMovedAgain is the reason
// RestoreOptions.Require exists.
//
// Force is a promise about *specific* newer work: somebody was shown what would
// be replaced and said yes to that. Everything a caller can check itself runs
// before the vault's locks and before the Git preflight, so a writer that lands
// in the window — bn-vault-sync applying a peer's change, most realistically —
// replaces the bytes without changing which paths are in conflict, and no
// re-check outside the transaction can see it. Pinning the authorized state is
// what turns the consent into a precondition of the write.
func TestRestoreForceRefusesWhenTheAuthorizedStateMovedAgain(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	note := filepath.Join(vaultRoot, "A.md")
	entry := recordOneRewrite(t, vaultRoot, journalRoot, note, "entry-require")

	// What a person was shown, and agreed to lose.
	writeFile(t, note, "the edit they were shown\n")
	authorized := map[string]vault.FileExpectation{"A.md": vault.ExpectHash(sha256Of(t, note))}

	// What landed afterwards, while they were reading the confirmation. Same
	// path, so the conflict set a caller could re-compute is identical.
	writeFile(t, note, "an edit nobody has seen\n")

	_, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{Force: true, Require: authorized})
	if err == nil {
		t.Fatal("forced restore should refuse a state nobody authorized")
	}
	if !vault.IsTxnConflict(err) {
		t.Fatalf("restore error = %v, want a transaction conflict", err)
	}
	assertFile(t, note, "an edit nobody has seen\n")
}

// TestRestoreRefusesARecordedPathTheAuthorizationDoesNotName pins the
// name-them-all rule. A path left out of a supplied map would silently fall back
// to the unpinned reading of disk, which is the exact behavior the map was
// supplied to replace — so a partial map is a caller bug, not a partial pin.
func TestRestoreRefusesARecordedPathTheAuthorizationDoesNotName(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	first := filepath.Join(vaultRoot, "A.md")
	second := filepath.Join(vaultRoot, "B.md")
	writeFile(t, first, "before\n")
	writeFile(t, second, "before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithIDGenerator(func(time.Time) (string, error) { return "entry-partial", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	for _, path := range []string{first, second} {
		if err := rec.Capture(path); err != nil {
			t.Fatalf("capture %s: %v", path, err)
		}
		writeFile(t, path, "after\n")
	}
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	partial := map[string]vault.FileExpectation{"A.md": vault.ExpectHash(sha256Of(t, first))}
	_, err = Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{Force: true, Require: partial})
	if err == nil {
		t.Fatal("restore should refuse an authorization that names only some of the entry")
	}
	if !strings.Contains(err.Error(), "no authorized state") || !strings.Contains(err.Error(), "B.md") {
		t.Fatalf("restore error = %v, want it to name B.md as unauthorized", err)
	}
	assertFile(t, first, "after\n")
	assertFile(t, second, "after\n")
}

// TestRestoreRequireRestoresWhenTheAuthorizedStateStillHolds is the other half:
// the pin is a precondition, not a refusal. A forced restore over exactly the
// work somebody agreed to replace still runs.
func TestRestoreRequireRestoresWhenTheAuthorizedStateStillHolds(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	note := filepath.Join(vaultRoot, "A.md")
	entry := recordOneRewrite(t, vaultRoot, journalRoot, note, "entry-require-ok")

	writeFile(t, note, "the edit they were shown\n")
	authorized := map[string]vault.FileExpectation{"A.md": vault.ExpectHash(sha256Of(t, note))}

	if _, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{Force: true, Require: authorized}); err != nil {
		t.Fatalf("restore --force with an authorization that holds: %v", err)
	}
	assertFile(t, note, "before\n")
}

// recordOneRewrite journals one note's rewrite from "before" to "after" and
// returns the finalized entry, leaving "after" on disk.
func recordOneRewrite(t *testing.T, vaultRoot, journalRoot, notePath, entryID string) *Entry {
	t.Helper()
	writeFile(t, notePath, "before\n")
	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithIDGenerator(func(time.Time) (string, error) { return entryID, nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(notePath); err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeFile(t, notePath, "after\n")
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return entry
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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

// TestRestoreAbortsBeforeAnyWriteWhenASnapshotIsUnreadable proves restore
// inherits the vault transaction's staging discipline: every snapshot is read
// and every precondition re-checked before the first file moves, so one broken
// snapshot leaves the whole entry unapplied instead of half-applied.
func TestRestoreAbortsBeforeAnyWriteWhenASnapshotIsUnreadable(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")

	firstPath := filepath.Join(vaultRoot, "A.md")
	secondPath := filepath.Join(vaultRoot, "B.md")
	writeFile(t, firstPath, "a-before\n")
	writeFile(t, secondPath, "b-before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-atomic", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	for _, p := range []string{firstPath, secondPath} {
		if err := rec.Capture(p); err != nil {
			t.Fatalf("capture %q: %v", p, err)
		}
	}
	writeFile(t, firstPath, "a-after\n")
	writeFile(t, secondPath, "b-after\n")
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var secondRecord FileRecord
	for _, rec := range entry.Files {
		if strings.HasSuffix(rec.Path, "B.md") {
			secondRecord = rec
		}
	}
	if strings.TrimSpace(secondRecord.Before.Snapshot) == "" {
		t.Fatal("B.md should have a snapshot")
	}
	snapshot := filepath.Join(journalRoot, entry.ID, filepath.FromSlash(secondRecord.Before.Snapshot))
	if err := os.Remove(snapshot); err != nil {
		t.Fatalf("remove snapshot: %v", err)
	}

	if _, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{}); err == nil {
		t.Fatal("restore should fail when a snapshot is unreadable")
	} else if !strings.Contains(err.Error(), "read snapshot") {
		t.Fatalf("error should name the snapshot read failure: %v", err)
	}

	assertFile(t, firstPath, "a-after\n")
	assertFile(t, secondPath, "b-after\n")
}

// TestRestoreForceStillDetectsANonRegularDestination shows --force overrides
// the journaled-state conflict check but not the transaction's own
// preconditions: it will not clobber something that is not a regular file.
func TestRestoreForceStillDetectsANonRegularDestination(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")

	notePath := filepath.Join(vaultRoot, "A.md")
	writeFile(t, notePath, "a-before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-nonregular", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(notePath); err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeFile(t, notePath, "a-after\n")
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if err := os.Remove(notePath); err != nil {
		t.Fatalf("remove note: %v", err)
	}
	if err := os.MkdirAll(notePath, 0o755); err != nil {
		t.Fatalf("mkdir in place of note: %v", err)
	}

	if _, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{Force: true}); err == nil {
		t.Fatal("restore must refuse to overwrite a directory even with --force")
	}
	info, err := os.Stat(notePath)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory should survive: info=%v err=%v", info, err)
	}
}
