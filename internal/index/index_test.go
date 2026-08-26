package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// testVault writes files (vault-relative slash path -> contents) into a fresh
// temp root and opens a vault on it.
func testVault(t *testing.T, files map[string]string) *vault.Vault {
	t.Helper()

	root := t.TempDir()
	for rel, contents := range files {
		writeNote(t, root, rel, contents)
	}
	cfg := &config.Config{Vault: root}
	cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
		"tags":     {Type: "list", Items: "string"},
		"status":   {Type: "string"},
		"rating":   {Type: "int"},
		"parent":   {Type: "wikilink"},
		"reviewed": {Type: "bool"},
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}

func writeNote(t *testing.T, root, rel, contents string) string {
	t.Helper()

	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

// backdate pushes every note's mtime into the past so the stale window stops
// forcing a re-read. Freshly written fixtures are by definition inside the
// window, so without this no test could ever reach the all-clean path.
func backdate(t testing.TB, root string, age time.Duration) {
	t.Helper()

	when := time.Now().Add(-age)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return err
		}
		return os.Chtimes(p, when, when)
	})
	if err != nil {
		t.Fatalf("backdate %s: %v", root, err)
	}
}

// newTestIndex builds an index whose log output is captured rather than
// printed, and returns the captured lines. Self-healing is required to be
// silent to the caller but not invisible, so tests assert on both.
func newTestIndex(t testing.TB, v *vault.Vault) (*Index, *[]string) {
	t.Helper()

	var logs []string
	ix, err := New(v, WithLogger(func(format string, args ...any) {
		logs = append(logs, format)
	}))
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	return ix, &logs
}

// relPaths reduces a result to the vault-relative slash paths of its healthy
// notes, sorted, which is the identity every parity and correctness assertion
// compares on.
func relPaths(t *testing.T, root string, notes []*vault.Note) []string {
	t.Helper()

	out := make([]string, 0, len(notes))
	for _, note := range notes {
		rel, err := filepath.Rel(root, note.Path)
		if err != nil {
			t.Fatalf("rel %s: %v", note.Path, err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func mustLoad(t testing.TB, ix *Index) Result {
	t.Helper()

	result, err := ix.Load()
	if err != nil {
		t.Fatalf("index load: %v", err)
	}
	return result
}

func TestLoadReturnsEveryWalkedNoteAndReportsColdChange(t *testing.T) {
	v := testVault(t, map[string]string{
		"Notes/Alpha.md":     "---\nstatus: wip\n---\nalpha\n",
		"Notes/Beta.md":      "---\nstatus: done\n---\nbeta\n",
		"Archive/Gamma.md":   "---\n---\ngamma\n",
		"Notes/notes.txt":    "not a note",
		"Notes/.Hidden.md":   "---\n---\nhidden\n",
		".secret/Buried.md":  "---\n---\nburied\n",
		"Notes/Sub/Delta.md": "---\ntags: [x]\n---\ndelta\n",
	})
	ix, _ := newTestIndex(t, v)

	result := mustLoad(t, ix)
	if !result.Changed {
		t.Error("a cold index must report Changed: there is no earlier observation to claim equality with")
	}
	want := []string{"Archive/Gamma.md", "Notes/Alpha.md", "Notes/Beta.md", "Notes/Sub/Delta.md"}
	if got := relPaths(t, v.Root, result.Notes); !equalStrings(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %v, want none", result.Failed)
	}
}

func TestSecondLoadIsCleanAndUnchanged(t *testing.T) {
	v := testVault(t, map[string]string{
		"Alpha.md": "---\nstatus: wip\n---\nalpha\n",
		"Beta.md":  "---\nstatus: done\n---\nbeta\n",
	})
	backdate(t, v.Root, time.Minute)
	ix, _ := newTestIndex(t, v)

	mustLoad(t, ix)
	second := mustLoad(t, ix)
	if second.Changed {
		t.Error("an unmodified vault must not report Changed on the second load")
	}
	if got, want := len(second.Notes), 2; got != want {
		t.Fatalf("notes = %d, want %d", got, want)
	}
}

// TestCleanEntryIsServedFromCacheWithoutReading proves the second pass really
// is served from the cache rather than merely producing the same answer: the
// file's bytes are swapped for different bytes of the same length while its
// mtime is restored, which is precisely the edit stat cannot see. A cached
// answer returns the old body; a re-reading one returns the new.
func TestCleanEntryIsServedFromCacheWithoutReading(t *testing.T) {
	v := testVault(t, map[string]string{"Alpha.md": "---\nstatus: wip\n---\nalpha\n"})
	backdate(t, v.Root, time.Minute)
	ix, _ := newTestIndex(t, v)

	mustLoad(t, ix)

	abs := filepath.Join(v.Root, "Alpha.md")
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	writeNote(t, v.Root, "Alpha.md", "---\nstatus: wip\n---\nbravo\n")
	if err := os.Chtimes(abs, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	second := mustLoad(t, ix)
	body, err := second.Notes[0].Body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !strings.Contains(body, "alpha") {
		t.Errorf("body = %q, want the cached revision; the entry was re-read despite matching size and mtime", body)
	}
}

// TestRecentModificationForcesReread is the other half of the same edit: with
// the mtime left inside the stale window, the identical size-preserving change
// must be picked up. This is the case the window exists for — a one-second
// filesystem granularity plus `sed -i` on a note is the real-world shape.
func TestRecentModificationForcesReread(t *testing.T) {
	v := testVault(t, map[string]string{"Alpha.md": "---\nstatus: wip\n---\nalpha\n"})
	backdate(t, v.Root, time.Minute)
	ix, _ := newTestIndex(t, v)

	mustLoad(t, ix)

	abs := filepath.Join(v.Root, "Alpha.md")
	writeNote(t, v.Root, "Alpha.md", "---\nstatus: wip\n---\nbravo\n")
	// Same size as the cached entry, and an mtime inside the window.
	recent := time.Now().Add(-staleWindow / 2)
	if err := os.Chtimes(abs, recent, recent); err != nil {
		t.Fatalf("set mtime: %v", err)
	}

	second := mustLoad(t, ix)
	if !second.Changed {
		t.Error("an edit inside the stale window must be reported as Changed")
	}
	body, err := second.Notes[0].Body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !strings.Contains(body, "bravo") {
		t.Errorf("body = %q, want the re-read revision", body)
	}
}

// TestStaleWindowBoundaryUsesTheClock pins the comparison itself rather than
// wall-clock timing: an mtime exactly staleWindow old is clean, one nanosecond
// younger is not.
func TestStaleWindowBoundaryUsesTheClock(t *testing.T) {
	base := time.Now()
	info := stubInfo{size: 10, mod: base.Add(-staleWindow)}
	cached := entry{Size: 10, ModTime: info.mod.UnixNano()}

	if !clean(cached, info, base) {
		t.Error("an mtime exactly staleWindow old must be clean")
	}
	if clean(cached, stubInfo{size: 10, mod: base.Add(-staleWindow + time.Nanosecond)}, base) {
		t.Error("an mtime one nanosecond inside the window must force a re-read")
	}
	if clean(entry{Size: 11, ModTime: info.mod.UnixNano()}, info, base) {
		t.Error("a size mismatch must force a re-read")
	}
	if clean(entry{Size: 10, ModTime: info.mod.UnixNano() + 1}, info, base) {
		t.Error("an mtime mismatch must force a re-read")
	}
	// A clock that has jumped backwards makes the age negative; that must fail
	// closed into a re-read rather than reading as "very old".
	if clean(cached, stubInfo{size: 10, mod: base.Add(time.Hour)}, base) {
		t.Error("a future mtime must force a re-read")
	}
}

func TestDeletedNoteIsDroppedFromTheIndex(t *testing.T) {
	v := testVault(t, map[string]string{
		"Alpha.md": "---\n---\nalpha\n",
		"Beta.md":  "---\n---\nbeta\n",
	})
	backdate(t, v.Root, time.Minute)
	ix, _ := newTestIndex(t, v)

	mustLoad(t, ix)
	if err := os.Remove(filepath.Join(v.Root, "Beta.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	second := mustLoad(t, ix)
	if !second.Changed {
		t.Error("dropping an entry the walk did not visit must report Changed")
	}
	if got, want := relPaths(t, v.Root, second.Notes), []string{"Alpha.md"}; !equalStrings(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
	// A second index over the same state file must not resurrect the dropped
	// note, which is what proves the drop was persisted rather than only
	// applied in memory.
	fresh, _ := newTestIndex(t, v)
	if got, want := relPaths(t, v.Root, mustLoad(t, fresh).Notes), []string{"Alpha.md"}; !equalStrings(got, want) {
		t.Errorf("reloaded notes = %v, want %v", got, want)
	}
}

func TestUnparseableNoteIsClassifiedNotFatal(t *testing.T) {
	v := testVault(t, map[string]string{
		"Good.md": "---\nstatus: wip\n---\ngood\n",
		"Bad.md":  "---\nstatus: [unterminated\n---\nbad\n",
	})
	ix, _ := newTestIndex(t, v)

	result := mustLoad(t, ix)
	if got, want := relPaths(t, v.Root, result.Notes), []string{"Good.md"}; !equalStrings(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
	if len(result.Failed) != 1 || result.Failed[0].RelPath != "Bad.md" {
		t.Fatalf("Failed = %v, want one entry for Bad.md", result.Failed)
	}
	if result.Failed[0].Err == nil {
		t.Error("a classified failure must carry its cause")
	}
}

// TestPersistedIndexIsReusedByANewIndex is the whole point of the on-disk
// state: a second process over an untouched vault starts warm.
func TestPersistedIndexIsReusedByANewIndex(t *testing.T) {
	v := testVault(t, map[string]string{
		"Alpha.md": "---\nstatus: wip\n---\nalpha\n",
		"Beta.md":  "---\nstatus: done\n---\nbeta\n",
	})
	backdate(t, v.Root, time.Minute)

	first, _ := newTestIndex(t, v)
	mustLoad(t, first)

	if _, err := os.Stat(filepath.Join(Dir(v.Root), stateFileName)); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	second, logs := newTestIndex(t, v)
	result := mustLoad(t, second)
	if result.Changed {
		t.Error("a warm start over an untouched vault must not report Changed")
	}
	if got, want := len(result.Notes), 2; got != want {
		t.Errorf("notes = %d, want %d", got, want)
	}
	if len(*logs) != 0 {
		t.Errorf("a healthy warm start logged %v", *logs)
	}
}

// TestStateDirectoryIsASubdirectory pins the layout choice: index.lock must not
// land in .bnotes/locks/, whose filenames are sha256(relpath) and belong to the
// note namespace, and `rm -rf .bnotes/index` must be the whole repair.
func TestStateDirectoryIsASubdirectory(t *testing.T) {
	v := testVault(t, map[string]string{"Alpha.md": "---\n---\nalpha\n"})
	ix, _ := newTestIndex(t, v)
	mustLoad(t, ix)

	if got, want := Dir(v.Root), filepath.Join(v.Root, ".bnotes", "index"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
	for _, name := range []string{stateFileName, lockFileName} {
		if _, err := os.Stat(filepath.Join(Dir(v.Root), name)); err != nil {
			t.Errorf("%s missing from the index directory: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(v.Root, ".bnotes", "locks", name)); err == nil {
			t.Errorf("%s leaked into the note lock namespace", name)
		}
	}

	if err := os.RemoveAll(Dir(v.Root)); err != nil {
		t.Fatalf("remove index dir: %v", err)
	}
	rebuilt, logs := newTestIndex(t, v)
	if got, want := len(mustLoad(t, rebuilt).Notes), 1; got != want {
		t.Errorf("notes after the documented repair = %d, want %d", got, want)
	}
	if len(*logs) != 0 {
		t.Errorf("a deleted index directory is an ordinary cold start, but it logged %v", *logs)
	}
}

// TestDiskWriteFailureIsIgnored covers the read-only-vault case: the process
// must still be correct, just cold next time.
func TestDiskWriteFailureIsIgnored(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	v := testVault(t, map[string]string{"Alpha.md": "---\n---\nalpha\n"})

	bnotes := filepath.Join(v.Root, ".bnotes")
	if err := os.MkdirAll(bnotes, 0o755); err != nil {
		t.Fatalf("mkdir .bnotes: %v", err)
	}
	if err := os.Chmod(bnotes, 0o500); err != nil {
		t.Fatalf("chmod .bnotes: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bnotes, 0o755) })

	ix, _ := newTestIndex(t, v)
	result, err := ix.Load()
	if err != nil {
		t.Fatalf("a disk-write failure must not surface as a load error: %v", err)
	}
	if got, want := len(result.Notes), 1; got != want {
		t.Errorf("notes = %d, want %d", got, want)
	}
}

func TestResetForcesARebuild(t *testing.T) {
	v := testVault(t, map[string]string{"Alpha.md": "---\n---\nalpha\n"})
	backdate(t, v.Root, time.Minute)
	ix, _ := newTestIndex(t, v)

	mustLoad(t, ix)
	if mustLoad(t, ix).Changed {
		t.Fatal("precondition: the second load should be unchanged")
	}
	ix.Reset()
	if _, err := os.Stat(filepath.Join(Dir(v.Root), stateFileName)); !os.IsNotExist(err) {
		t.Errorf("Reset left a state file behind: %v", err)
	}
	if !mustLoad(t, ix).Changed {
		t.Error("a reset index is cold and must report Changed")
	}
}

// stubInfo is the minimum os.FileInfo clean reads, so the window boundary can
// be tested against an exact clock instead of a sleep.
type stubInfo struct {
	size int64
	mod  time.Time
}

func (s stubInfo) Name() string       { return "stub" }
func (s stubInfo) Size() int64        { return s.size }
func (s stubInfo) Mode() os.FileMode  { return 0o644 }
func (s stubInfo) ModTime() time.Time { return s.mod }
func (s stubInfo) IsDir() bool        { return false }
func (s stubInfo) Sys() any           { return nil }

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
