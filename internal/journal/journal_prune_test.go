package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func makeTestEntry(t *testing.T, vaultRoot, journalRoot, id string, ts time.Time) {
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

func TestPruneOldEntries(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	makeTestEntry(t, vaultRoot, journalRoot, "entry-old-1", now.Add(-96*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "entry-old-2", now.Add(-72*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "entry-new", now.Add(-4*time.Hour))

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

	makeTestEntry(t, vaultRoot, journalRoot, "drop-beta", oldest)
	makeTestEntry(t, vaultRoot, journalRoot, "drop-alpha", oldest)
	makeTestEntry(t, vaultRoot, journalRoot, "keep-cutoff", now.Add(-24*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "keep-new", now.Add(-time.Hour))

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
