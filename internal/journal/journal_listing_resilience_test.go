package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func corruptManifest(t *testing.T, journalRoot, id string, content []byte) {
	t.Helper()
	manifest := filepath.Join(journalRoot, id, ManifestFileName)
	if err := os.WriteFile(manifest, content, 0o600); err != nil {
		t.Fatalf("corrupt %q: %v", id, err)
	}
}

func warningIDs(warnings []Warning) []string {
	ids := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		ids = append(ids, warning.ID)
	}
	return ids
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The point of the change: a journal holding valid, manifest-less, malformed and
// unreadable entries all at once still hands back every valid entry, in order,
// and names each unusable one exactly once.
func TestListEntries_MixedValidAndCorruptEntries(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	makeTestEntry(t, vaultRoot, journalRoot, "entry-a-oldest", base.Add(-72*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "entry-b-middle", base.Add(-48*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "entry-c-newest", base.Add(-1*time.Hour))

	// Truncated JSON: the failure mode this vault actually has, where a manifest
	// holds a raw newline inside a string.
	makeTestEntry(t, vaultRoot, journalRoot, "entry-malformed", base.Add(-24*time.Hour))
	corruptManifest(t, journalRoot, "entry-malformed", []byte(`{"id": "entry-malformed", "command": "bn `+"\n"+`noop"`))

	// Structurally valid JSON that fails validation.
	makeTestEntry(t, vaultRoot, journalRoot, "entry-invalid", base.Add(-25*time.Hour))
	corruptManifest(t, journalRoot, "entry-invalid", []byte(`{"version":1,"id":"","command":"","timestamp":"","files":[]}`))

	// A manifest whose recorded ID disagrees with its directory. Restoring it
	// would undo a different command than the one the user asked for.
	makeTestEntry(t, vaultRoot, journalRoot, "entry-mismatched", base.Add(-26*time.Hour))
	mismatched, err := os.ReadFile(filepath.Join(journalRoot, "entry-mismatched", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	corruptManifest(t, journalRoot, "entry-mismatched",
		[]byte(strings.Replace(string(mismatched), `"entry-mismatched"`, `"entry-someone-else"`, 1)))

	// An entry directory with no manifest at all: what an in-flight recording
	// looks like from another process.
	if err := os.MkdirAll(filepath.Join(journalRoot, "entry-in-flight", SnapshotsDirName), 0o755); err != nil {
		t.Fatalf("mkdir in-flight: %v", err)
	}

	entries, warnings, err := ListEntries(journalRoot)
	if err != nil {
		t.Fatalf("ListEntries() error = %v, want the listing to survive", err)
	}

	wantEntries := []string{"entry-c-newest", "entry-b-middle", "entry-a-oldest"}
	if got := entryIDs(entries); !equalStrings(got, wantEntries) {
		t.Fatalf("entries = %v, want %v (valid entries, newest-first)", got, wantEntries)
	}

	wantWarnings := []string{"entry-invalid", "entry-malformed", "entry-mismatched"}
	if got := warningIDs(warnings); !equalStrings(got, wantWarnings) {
		t.Fatalf("warnings = %v, want %v (the in-flight entry is not corruption)", got, wantWarnings)
	}
	for _, warning := range warnings {
		if warning.Dir != filepath.Join(journalRoot, warning.ID) {
			t.Fatalf("warning %q Dir = %q, want the entry directory", warning.ID, warning.Dir)
		}
		if warning.Err == nil {
			t.Fatalf("warning %q carries no cause", warning.ID)
		}
		if !strings.Contains(warning.Error(), warning.ID) {
			t.Fatalf("warning message %q does not name the entry", warning.Error())
		}
	}
}

// An unreadable journal root is a different claim from an unreadable entry:
// nothing at all is known, so reporting "no history" would be a lie.
func TestListEntries_UnreadableRootIsFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	makeTestEntry(t, vaultRoot, journalRoot, "entry-a", time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))

	if err := os.Chmod(journalRoot, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(journalRoot, 0o755) })

	if _, _, err := ListEntries(journalRoot); err == nil {
		t.Fatal("ListEntries() = nil error, want the unreadable root to be fatal")
	}
}

// An entry whose manifest cannot be opened at all — as opposed to one holding
// bad bytes — is still only that entry's problem.
func TestListEntries_UnreadableManifestBecomesWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	makeTestEntry(t, vaultRoot, journalRoot, "entry-good", base)
	makeTestEntry(t, vaultRoot, journalRoot, "entry-locked", base.Add(-time.Hour))

	locked := filepath.Join(journalRoot, "entry-locked", ManifestFileName)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	entries, warnings, err := ListEntries(journalRoot)
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if got := entryIDs(entries); !equalStrings(got, []string{"entry-good"}) {
		t.Fatalf("entries = %v, want [entry-good]", got)
	}
	if got := warningIDs(warnings); !equalStrings(got, []string{"entry-locked"}) {
		t.Fatalf("warnings = %v, want [entry-locked]", got)
	}
}

// A missing journal is not damage — there is simply no history yet.
func TestListEntries_MissingRootIsEmpty(t *testing.T) {
	entries, warnings, err := ListEntries(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 0 || len(warnings) != 0 {
		t.Fatalf("entries = %v, warnings = %v, want both empty", entries, warnings)
	}
}

// Enumeration degrades to a warning; a specifically named entry does not. The
// caller asked for this one, so "it is corrupt" has to be the answer rather than
// a silently different entry.
func TestLoadEntry_CorruptEntryStaysADirectError(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	makeTestEntry(t, vaultRoot, journalRoot, "entry-good", base)
	makeTestEntry(t, vaultRoot, journalRoot, "entry-bad", base.Add(-time.Hour))
	corruptManifest(t, journalRoot, "entry-bad", []byte("{not json"))

	if _, err := LoadEntry(journalRoot, "entry-bad"); err == nil {
		t.Fatal("LoadEntry(corrupt) = nil error, want a direct error")
	}
	if _, err := LoadEntry(journalRoot, "entry-good"); err != nil {
		t.Fatalf("LoadEntry(valid) = %v, want nil", err)
	}
}

// Undoing an unrelated entry must keep working while another one is corrupt.
// This is the whole point: one bad manifest costs one undo record.
func TestRestore_UnaffectedByAnUnrelatedCorruptEntry(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	notePath := filepath.Join(vaultRoot, "note.md")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	rec, err := NewRecorder(vaultRoot, journalRoot, "bn set",
		WithClock(func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-real", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(notePath); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}
	if _, err := rec.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	makeTestEntry(t, vaultRoot, journalRoot, "entry-broken",
		time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC))
	corruptManifest(t, journalRoot, "entry-broken", []byte("{not json"))

	if _, err := Restore(vaultRoot, journalRoot, "entry-real", RestoreOptions{}); err != nil {
		t.Fatalf("Restore(entry-real) = %v, want the unrelated corruption to be irrelevant", err)
	}
	restored, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if string(restored) != "before\n" {
		t.Fatalf("note = %q, want %q", restored, "before\n")
	}
}

// Pruning selects by manifest timestamp, so an entry it cannot read is an entry
// it cannot date. It is left alone rather than deleted on a guess.
func TestPrune_LeavesUnreadableEntriesAlone(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	makeTestEntry(t, vaultRoot, journalRoot, "entry-old", base.Add(-96*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "entry-new", base.Add(-1*time.Hour))
	makeTestEntry(t, vaultRoot, journalRoot, "entry-broken", base.Add(-95*time.Hour))
	corruptManifest(t, journalRoot, "entry-broken", []byte("{not json"))

	removed, err := Prune(journalRoot, RetentionPolicy{
		MaxAge: 24 * time.Hour,
		Now:    func() time.Time { return base },
	})
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if !equalStrings(removed, []string{"entry-old"}) {
		t.Fatalf("removed = %v, want [entry-old]", removed)
	}
	if _, err := os.Stat(filepath.Join(journalRoot, "entry-broken")); err != nil {
		t.Fatalf("stat entry-broken: %v, want it left in place for inspection", err)
	}
}

func TestWarning_UnwrapsToItsCause(t *testing.T) {
	cause := errors.New("bad manifest")
	warning := Warning{ID: "entry-x", Dir: "/journal/entry-x", Err: cause}
	if !errors.Is(warning, cause) {
		t.Fatal("errors.Is(warning, cause) = false, want true")
	}
	if !strings.Contains(warning.Error(), "entry-x") {
		t.Fatalf("Error() = %q, want it to name the entry", warning.Error())
	}
}
