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
)

func TestNormalizeWithinRootRejectsSymlinkedAncestorEscape(t *testing.T) {
	vaultRoot := t.TempDir()
	outsideRoot := t.TempDir()
	linkPath := filepath.Join(vaultRoot, "linked")
	if err := os.Symlink(outsideRoot, linkPath); err != nil {
		t.Fatalf("symlink %q -> %q: %v", linkPath, outsideRoot, err)
	}

	_, _, err := normalizeWithinRoot(vaultRoot, filepath.Join("linked", "Escape.md"))
	if err == nil {
		t.Fatal("normalizeWithinRoot should reject symlinked ancestor escape")
	}
	if !strings.Contains(err.Error(), "resolves outside root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecorderCapturesFirstSnapshotAndWritesManifest(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	note := filepath.Join(vaultRoot, "A.md")
	writeFile(t, note, "before\n")

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set A.md status=done",
		WithClock(func() time.Time { return fixed }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-1", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture initial: %v", err)
	}
	writeFile(t, note, "after-first\n")
	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture duplicate: %v", err)
	}
	writeFile(t, note, "after-final\n")

	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if entry.ID != "entry-1" {
		t.Fatalf("entry id = %q, want entry-1", entry.ID)
	}
	if !entry.Timestamp.Equal(fixed) {
		t.Fatalf("timestamp = %s, want %s", entry.Timestamp, fixed)
	}
	if entry.Command != "bn set A.md status=done" {
		t.Fatalf("command = %q", entry.Command)
	}
	if len(entry.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(entry.Files))
	}
	recFile := entry.Files[0]
	if recFile.Path != "A.md" {
		t.Fatalf("record path = %q, want A.md", recFile.Path)
	}
	if recFile.Before.Kind != KindFile {
		t.Fatalf("before kind = %q, want file", recFile.Before.Kind)
	}
	if recFile.After.Kind != KindFile {
		t.Fatalf("after kind = %q, want file", recFile.After.Kind)
	}
	if recFile.Before.SHA256 != hashHex("before\n") {
		t.Fatalf("before hash mismatch: %s", recFile.Before.SHA256)
	}
	if recFile.After.SHA256 != hashHex("after-final\n") {
		t.Fatalf("after hash mismatch: %s", recFile.After.SHA256)
	}

	snapPath := filepath.Join(journalRoot, entry.ID, filepath.FromSlash(recFile.Before.Snapshot))
	snapData, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(snapData) != "before\n" {
		t.Fatalf("snapshot content = %q, want %q", string(snapData), "before\n")
	}

	loaded, err := LoadEntry(journalRoot, entry.ID)
	if err != nil {
		t.Fatalf("load entry: %v", err)
	}
	if !reflect.DeepEqual(*loaded, *entry) {
		t.Fatalf("loaded entry mismatch\nloaded: %#v\nentry:  %#v", *loaded, *entry)
	}
}

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

func TestPruneOldEntries(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	makeEntry := func(id string, ts time.Time) {
		rec, err := NewRecorder(
			vaultRoot,
			journalRoot,
			"bn noop",
			WithClock(func() time.Time { return ts }),
			WithIDGenerator(func(time.Time) (string, error) { return id, nil }),
		)
		if err != nil {
			t.Fatalf("new recorder %q: %v", id, err)
		}
		if _, err := rec.Finalize(); err != nil {
			t.Fatalf("finalize %q: %v", id, err)
		}
	}

	makeEntry("entry-old-1", now.Add(-96*time.Hour))
	makeEntry("entry-old-2", now.Add(-72*time.Hour))
	makeEntry("entry-new", now.Add(-4*time.Hour))

	listed, err := ListEntries(journalRoot)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("len(listed) = %d, want 3", len(listed))
	}
	if listed[0].ID != "entry-new" {
		t.Fatalf("entries should be newest-first, first=%q", listed[0].ID)
	}

	removed, err := Prune(journalRoot, RetentionPolicy{
		MaxEntries: 2,
		MaxAge:     36 * time.Hour,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"entry-old-1", "entry-old-2"}) {
		t.Fatalf("removed = %v, want [entry-old-1 entry-old-2]", removed)
	}

	assertDirMissing(t, filepath.Join(journalRoot, "entry-old-1"))
	assertDirMissing(t, filepath.Join(journalRoot, "entry-old-2"))
	if _, err := os.Stat(filepath.Join(journalRoot, "entry-new")); err != nil {
		t.Fatalf("entry-new should remain: %v", err)
	}
}

func TestLoadEntryRejectsManifestIDMismatch(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	note := filepath.Join(vaultRoot, "A.md")
	writeFile(t, note, "before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set",
		WithIDGenerator(func(time.Time) (string, error) { return "entry-good", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeFile(t, note, "after\n")
	if _, err := rec.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	manifestPath := filepath.Join(journalRoot, "entry-good", ManifestFileName)
	var manifest Entry
	if err := readJSON(manifestPath, &manifest); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest.ID = "entry-other"
	if err := writeJSONAtomic(manifestPath, manifest); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	_, err = LoadEntry(journalRoot, "entry-good")
	if err == nil {
		t.Fatal("LoadEntry should fail when manifest ID mismatches directory")
	}
	if !strings.Contains(err.Error(), `manifest id "entry-other" does not match directory "entry-good"`) {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
}

func TestPruneByAgeHonorsCutoffAndDeterministicOrder(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-24*time.Hour - time.Second)

	makeEntry := func(id string, ts time.Time) {
		t.Helper()
		rec, err := NewRecorder(
			vaultRoot,
			journalRoot,
			"bn noop",
			WithClock(func() time.Time { return ts }),
			WithIDGenerator(func(time.Time) (string, error) { return id, nil }),
		)
		if err != nil {
			t.Fatalf("new recorder %q: %v", id, err)
		}
		if _, err := rec.Finalize(); err != nil {
			t.Fatalf("finalize %q: %v", id, err)
		}
	}

	makeEntry("drop-beta", oldest)
	makeEntry("drop-alpha", oldest)
	makeEntry("keep-cutoff", now.Add(-24*time.Hour))
	makeEntry("keep-new", now.Add(-time.Hour))

	removed, err := Prune(journalRoot, RetentionPolicy{
		MaxAge: 24 * time.Hour,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("prune by age: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"drop-alpha", "drop-beta"}) {
		t.Fatalf("removed = %v, want [drop-alpha drop-beta]", removed)
	}

	assertDirMissing(t, filepath.Join(journalRoot, "drop-alpha"))
	assertDirMissing(t, filepath.Join(journalRoot, "drop-beta"))
	if _, err := os.Stat(filepath.Join(journalRoot, "keep-cutoff")); err != nil {
		t.Fatalf("keep-cutoff should remain at age boundary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(journalRoot, "keep-new")); err != nil {
		t.Fatalf("keep-new should remain: %v", err)
	}
}

func TestPruneRejectsNegativePolicyValues(t *testing.T) {
	journalRoot := filepath.Join(t.TempDir(), ".bnotes", "journal")
	if _, err := Prune(journalRoot, RetentionPolicy{MaxEntries: -1}); err == nil {
		t.Fatal("expected negative max entries to fail")
	}
	if _, err := Prune(journalRoot, RetentionPolicy{MaxAge: -time.Second}); err == nil {
		t.Fatal("expected negative max age to fail")
	}
}

func TestListEntriesSkipsIncompleteEntryDirectories(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn noop",
		WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-good", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if _, err := rec.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(journalRoot, "entry-incomplete", SnapshotsDirName), 0o755); err != nil {
		t.Fatalf("mkdir incomplete entry: %v", err)
	}

	entries, err := ListEntries(journalRoot)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ID != "entry-good" {
		t.Fatalf("entry id = %q, want entry-good", entries[0].ID)
	}
}

func TestPruneSkipsIncompleteEntryDirectories(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	makeEntry := func(id string, ts time.Time) {
		t.Helper()
		rec, err := NewRecorder(
			vaultRoot,
			journalRoot,
			"bn noop",
			WithClock(func() time.Time { return ts }),
			WithIDGenerator(func(time.Time) (string, error) { return id, nil }),
		)
		if err != nil {
			t.Fatalf("new recorder %q: %v", id, err)
		}
		if _, err := rec.Finalize(); err != nil {
			t.Fatalf("finalize %q: %v", id, err)
		}
	}

	makeEntry("entry-old", now.Add(-48*time.Hour))
	makeEntry("entry-new", now.Add(-time.Hour))
	if err := os.MkdirAll(filepath.Join(journalRoot, "entry-incomplete", SnapshotsDirName), 0o755); err != nil {
		t.Fatalf("mkdir incomplete entry: %v", err)
	}

	removed, err := Prune(journalRoot, RetentionPolicy{
		MaxEntries: 1,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"entry-old"}) {
		t.Fatalf("removed = %v, want [entry-old]", removed)
	}

	assertDirMissing(t, filepath.Join(journalRoot, "entry-old"))
	if _, err := os.Stat(filepath.Join(journalRoot, "entry-new")); err != nil {
		t.Fatalf("entry-new should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(journalRoot, "entry-incomplete")); err != nil {
		t.Fatalf("entry-incomplete should remain skipped: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%q = %q, want %q", path, string(data), want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("%q should be missing, stat err=%v", path, err)
	}
}

func assertDirMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("directory %q should be missing, stat err=%v", path, err)
	}
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
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

func TestRestoreRejectsSymlinkedAncestorEscape(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	outsideRoot := t.TempDir()
	linkPath := filepath.Join(vaultRoot, "linked")
	if err := os.MkdirAll(filepath.Join(journalRoot, "entry-escape"), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	if err := os.Symlink(outsideRoot, linkPath); err != nil {
		t.Fatalf("symlink %q -> %q: %v", linkPath, outsideRoot, err)
	}

	outsideNote := filepath.Join(outsideRoot, "Escape.md")
	writeFile(t, outsideNote, "outside\n")
	entry := Entry{
		ID:        "entry-escape",
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Command:   "bn restore",
		Files: []FileRecord{{
			Path: "linked/Escape.md",
			Before: FileState{
				Kind: KindMissing,
			},
			After: FileState{
				Kind:   KindFile,
				SHA256: hashHex("outside\n"),
				Size:   int64(len("outside\n")),
				Mode:   0o644,
			},
		}},
	}
	if err := writeJSONAtomic(filepath.Join(journalRoot, "entry-escape", ManifestFileName), entry); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := Restore(vaultRoot, journalRoot, "entry-escape", RestoreOptions{DryRun: true})
	if err == nil {
		t.Fatal("restore should reject symlinked ancestor escape")
	}
	if !strings.Contains(err.Error(), "resolves outside root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRestoreRejectsSnapshotSymlinkEscape(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	entryID := "entry-snapshot-escape"
	entryDir := filepath.Join(journalRoot, entryID)
	snapshotsDir := filepath.Join(entryDir, SnapshotsDirName)
	outsideRoot := t.TempDir()
	outsideSnapshot := filepath.Join(outsideRoot, "outside.snap")
	notePath := filepath.Join(vaultRoot, "A.md")
	if err := os.MkdirAll(snapshotsDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	writeFile(t, outsideSnapshot, "outside-before\n")
	if err := os.Symlink(outsideSnapshot, filepath.Join(snapshotsDir, "bad.snap")); err != nil {
		t.Fatalf("symlink snapshot: %v", err)
	}
	writeFile(t, notePath, "after\n")

	entry := Entry{
		ID:        entryID,
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Command:   "bn restore",
		Files: []FileRecord{{
			Path: "A.md",
			Before: FileState{
				Kind:     KindFile,
				SHA256:   hashHex("outside-before\n"),
				Size:     int64(len("outside-before\n")),
				Mode:     0o644,
				Snapshot: filepath.ToSlash(filepath.Join(SnapshotsDirName, "bad.snap")),
			},
			After: FileState{
				Kind:   KindFile,
				SHA256: hashHex("after\n"),
				Size:   int64(len("after\n")),
				Mode:   0o644,
			},
		}},
	}
	if err := writeJSONAtomic(filepath.Join(entryDir, ManifestFileName), entry); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := Restore(vaultRoot, journalRoot, entryID, RestoreOptions{})
	if err == nil {
		t.Fatal("restore should reject snapshot symlink escape")
	}
	if !strings.Contains(err.Error(), "snapshot escapes entry dir") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, notePath, "after\n")
}
