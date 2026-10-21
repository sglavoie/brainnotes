package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// noteSourceCase names one implementation of noteSource and how to build it for
// a vault. Every behaviour the snapshot depends on is asserted against both, so
// swapping the default source cannot change what a snapshot observes.
type noteSourceCase struct {
	name string
	make func(t *testing.T, v *vault.Vault) noteSource
}

func noteSourceCases() []noteSourceCase {
	return []noteSourceCase{
		{
			name: "direct",
			make: func(*testing.T, *vault.Vault) noteSource { return directNoteSource{} },
		},
		{
			name: "index",
			make: func(t *testing.T, v *vault.Vault) noteSource {
				t.Helper()
				return newTestIndexSource(t, v)
			},
		},
		{
			// A warm index is a distinct path, not a repeat of the cold one: it
			// serves entries without reading the file at all, so every rule the
			// direct path enforces at read time has to be enforced somewhere
			// else. Backdating the fixture's mtimes is what makes the entries
			// clean — inside staleWindow the index re-reads and the cache-served
			// branch never runs.
			name: "index-warm",
			make: func(t *testing.T, v *vault.Vault) noteSource {
				t.Helper()
				source := newTestIndexSource(t, v)
				if _, err := source.loadNotes(v, nil, noteLoadTimeout, nil); err != nil {
					t.Fatalf("warm the index: %v", err)
				}
				return source
			},
		},
	}
}

func newTestIndexSource(t *testing.T, v *vault.Vault) *indexNoteSource {
	t.Helper()
	source, err := newIndexNoteSource(v)
	if err != nil {
		t.Fatalf("newIndexNoteSource: %v", err)
	}
	return source
}

// writeSourceFixture lays down one healthy note, one note whose frontmatter
// cannot be parsed, and one note the caller will quarantine.
func writeSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Healthy.md":     "---\ntitle: Healthy\n---\nBody\n",
		"Broken.md":      "---\ntitle: [unterminated\n---\nBody\n",
		"Quarantined.md": "---\ntitle: Quarantined\n---\nBody\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		backdate(t, path)
	}
	return root
}

// backdate moves a file's mtime well outside the index's stale window, so a
// cached entry for it is trusted on stat alone. Without this every load in a
// freshly written fixture re-reads, and the cache-served branch — the one the
// direct path has no equivalent of — is never exercised.
func backdate(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// TestNoteSourcesClassifyIdentically is the parity that makes the swap safe:
// both sources must produce the same healthy notes, the same content hashes,
// the same diagnostics, and — because the classification is fingerprinted by
// the same function — the same fingerprint. An equal fingerprint is the strong
// form of the claim: a serial computed under one source is comparable with one
// computed under the other, so a load path change cannot silently invalidate
// every derived cache.
func TestNoteSourcesClassifyIdentically(t *testing.T) {
	t.Parallel()

	root := writeSourceFixture(t)
	_, v := testVaultOpen(t, root, "127.0.0.1:0")
	quarantined := map[string]struct{}{
		filepath.Join(root, "Quarantined.md"): {},
	}

	type observation struct {
		loaded      map[string]string
		parseErrors []diagnosticFileError
		skipped     []diagnosticFileError
		timedOut    []string
		fingerprint string
	}
	observe := func(t *testing.T, source noteSource) observation {
		t.Helper()
		result, err := source.loadNotes(v, nil, noteLoadTimeout, quarantined)
		if err != nil {
			t.Fatalf("loadNotes: %v", err)
		}
		loaded := make(map[string]string, len(result.loaded))
		for _, item := range result.loaded {
			loaded[item.rel] = item.note.SourceHash()
		}
		return observation{
			loaded:      loaded,
			parseErrors: result.parseErrors,
			skipped:     result.skipped,
			timedOut:    result.timedOut,
			fingerprint: result.fingerprint,
		}
	}

	observations := make(map[string]observation)
	for _, tc := range noteSourceCases() {
		observations[tc.name] = observe(t, tc.make(t, v))
	}

	direct := observations["direct"]
	if len(direct.loaded) != 1 || direct.loaded["Healthy.md"] == "" {
		t.Fatalf("direct loaded = %#v, want only a hashed Healthy.md", direct.loaded)
	}
	if len(direct.parseErrors) != 1 || direct.parseErrors[0].Path != "Broken.md" {
		t.Fatalf("direct parse errors = %#v, want Broken.md", direct.parseErrors)
	}
	if len(direct.skipped) != 1 || direct.skipped[0].Path != "Quarantined.md" {
		t.Fatalf("direct skipped = %#v, want Quarantined.md", direct.skipped)
	}
	if len(direct.timedOut) != 0 {
		t.Fatalf("an already-quarantined path must not be re-reported as newly timed out: %#v", direct.timedOut)
	}

	for name, observed := range observations {
		if name == "direct" {
			continue
		}
		if len(observed.loaded) != len(direct.loaded) {
			t.Fatalf("%s: loaded counts differ: %#v vs direct %#v", name, observed.loaded, direct.loaded)
		}
		for rel, hash := range direct.loaded {
			if observed.loaded[rel] != hash {
				t.Fatalf("%s: hash for %s = %q, want %q", name, rel, observed.loaded[rel], hash)
			}
		}
		if !sameDiagnostics(observed.parseErrors, direct.parseErrors) {
			t.Fatalf("%s: parse errors differ:\n%#v\ndirect %#v", name, observed.parseErrors, direct.parseErrors)
		}
		if !sameDiagnostics(observed.skipped, direct.skipped) {
			t.Fatalf("%s: skipped differ:\n%#v\ndirect %#v", name, observed.skipped, direct.skipped)
		}
		if len(observed.timedOut) != 0 {
			t.Fatalf("%s: reported a new timeout for a quarantined path: %#v", name, observed.timedOut)
		}
		if observed.fingerprint != direct.fingerprint {
			t.Fatalf("%s: fingerprint = %s, want %s", name, observed.fingerprint, direct.fingerprint)
		}
	}
}

func sameDiagnostics(a, b []diagnosticFileError) bool {
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

// TestVaultStateDefaultsToTheIndexSource pins the wiring itself: production
// snapshots come from the index, and nothing but a test may put the direct
// source back.
func TestVaultStateDefaultsToTheIndexSource(t *testing.T) {
	t.Parallel()

	root := writeSourceFixture(t)
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	if _, ok := state.source.(*indexNoteSource); !ok {
		t.Fatalf("default source = %T, want *indexNoteSource", state.source)
	}
}

// TestVaultStateSnapshotBehaviourHoldsForBothSources runs the snapshot
// behaviour every derived cache depends on — the serial moving exactly when the
// vault's contents move — against each source in turn.
func TestVaultStateSnapshotBehaviourHoldsForBothSources(t *testing.T) {
	t.Parallel()

	for _, tc := range noteSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			notePath := filepath.Join(root, "Note.md")
			otherPath := filepath.Join(root, "Other.md")
			if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v1\n"), 0o644); err != nil {
				t.Fatalf("write note: %v", err)
			}
			if err := os.WriteFile(otherPath, []byte("---\ntitle: Other\n---\nBody\n"), 0o644); err != nil {
				t.Fatalf("write other: %v", err)
			}
			_, v := testVaultOpen(t, root, "127.0.0.1:0")

			state, err := newVaultState(v, time.Second)
			if err != nil {
				t.Fatalf("newVaultState: %v", err)
			}
			state.source = tc.make(t, v)
			now := time.Unix(0, 0)
			state.now = func() time.Time { return now }

			advance := func(t *testing.T) vaultSnapshot {
				t.Helper()
				now = now.Add(2 * time.Second)
				snapshot, err := state.currentSnapshot()
				if err != nil {
					t.Fatalf("currentSnapshot: %v", err)
				}
				return snapshot
			}

			first := advance(t)
			if len(first.notes) != 2 {
				t.Fatalf("notes = %d, want 2", len(first.notes))
			}

			for i := 0; i < 3; i++ {
				if rebuilt := advance(t); rebuilt.serial != first.serial {
					t.Fatalf("rebuild %d serial = %d, want %d (unchanged vault must not bump serial)", i, rebuilt.serial, first.serial)
				}
			}

			if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v2\n"), 0o644); err != nil {
				t.Fatalf("rewrite note: %v", err)
			}
			edited := advance(t)
			if edited.serial != first.serial+1 {
				t.Fatalf("edited serial = %d, want %d", edited.serial, first.serial+1)
			}
			body, err := noteBodyByTitle(edited, "Note")
			if err != nil {
				t.Fatalf("edited body: %v", err)
			}
			if body != "Body v2\n" {
				t.Fatalf("edited body = %q, want %q", body, "Body v2\n")
			}
			if settled := advance(t); settled.serial != edited.serial {
				t.Fatalf("post-edit rebuild serial = %d, want %d (one edit bumps exactly once)", settled.serial, edited.serial)
			}

			if err := os.Remove(otherPath); err != nil {
				t.Fatalf("remove other: %v", err)
			}
			deleted := advance(t)
			if deleted.serial != edited.serial+1 {
				t.Fatalf("deleted serial = %d, want %d", deleted.serial, edited.serial+1)
			}
			if len(deleted.notes) != 1 {
				t.Fatalf("notes after delete = %d, want 1", len(deleted.notes))
			}
		})
	}
}

func noteBodyByTitle(snapshot vaultSnapshot, title string) (string, error) {
	for _, note := range snapshot.notes {
		if note.Title() == title {
			return note.Body()
		}
	}
	return "", os.ErrNotExist
}

// TestIndexSourceHonoursQuarantineForACachedNote covers the hazard the index
// introduces and the direct path cannot have: a cached entry is served without
// a read, so the reader's quarantine check alone would let a note quarantined
// after it was cached come back as healthy.
func TestIndexSourceHonoursQuarantineForACachedNote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	healthyPath := filepath.Join(root, "Healthy.md")
	laterPath := filepath.Join(root, "Later.md")
	if err := os.WriteFile(healthyPath, []byte("---\ntitle: Healthy\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write healthy: %v", err)
	}
	if err := os.WriteFile(laterPath, []byte("---\ntitle: Later\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write later: %v", err)
	}
	// Outside the stale window, so the second load is served from the cache
	// rather than re-read. Inside it the index re-reads and the reader's own
	// quarantine check would answer for this — which is not what is under test.
	backdate(t, healthyPath)
	backdate(t, laterPath)
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	source := newTestIndexSource(t, v)

	warm, err := source.loadNotes(v, nil, noteLoadTimeout, nil)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(warm.loaded) != 2 {
		t.Fatalf("first load = %d notes, want 2", len(warm.loaded))
	}

	// Quarantine after the note is cached: the second load must skip it even
	// though serving it would require no read at all.
	quarantined := map[string]struct{}{laterPath: {}}
	after, err := source.loadNotes(v, nil, noteLoadTimeout, quarantined)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(after.loaded) != 1 || after.loaded[0].rel != "Healthy.md" {
		t.Fatalf("second load loaded = %#v, want only Healthy.md", after.loaded)
	}
	if len(after.skipped) != 1 || after.skipped[0].Path != "Later.md" {
		t.Fatalf("second load skipped = %#v, want Later.md", after.skipped)
	}
	if after.skipped[0].Error != errNoteLoadTimeout.Error() {
		t.Fatalf("skip reason = %q, want %q", after.skipped[0].Error, errNoteLoadTimeout.Error())
	}
	if len(after.timedOut) != 0 {
		t.Fatalf("an already-quarantined path must not be re-reported as newly timed out: %#v", after.timedOut)
	}
}

// TestIndexSourceFallsBackToTheDirectPathForAnInjectedLoader states the rule
// the noteSource doc comment relies on: a test injecting a noteLoader is asking
// for the direct path, because the index reads bytes itself and would silently
// ignore the loader.
func TestIndexSourceFallsBackToTheDirectPathForAnInjectedLoader(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	source := newTestIndexSource(t, v)

	calls := 0
	load := func(n *vault.Note) error {
		calls++
		return n.Load()
	}
	if _, err := source.loadNotes(v, load, noteLoadTimeout, nil); err != nil {
		t.Fatalf("loadNotes: %v", err)
	}
	if calls != 1 {
		t.Fatalf("injected loader called %d times, want 1 (the index must not swallow it)", calls)
	}
}
