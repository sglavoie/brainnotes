package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// This file is where the cache's one safety property gets falsified rather
// than described. Every other test in the package pins a mechanism — the stale
// window, the state file, a corruption class. This one asserts the outcome all
// of those exist to produce: after any mutation the filesystem can express, a
// cached answer and an uncached answer are the same answer.
//
// The comparison is on (relpath, SourceHash) rather than on paths, because
// "the same set of notes" is the weak half of the claim. SourceHash is the
// SHA-256 of the bytes each note was parsed from, so comparing it says the
// index returned the same *revision* of every file — which is the only thing
// that makes serving a note from cache honest.

// observation maps a vault-relative slash path to the SHA-256 of the bytes
// that note was parsed from.
type observation map[string]string

// String renders an observation stably so a mismatch names the offending
// entries instead of dumping two unordered maps.
func (o observation) String() string {
	rels := make([]string, 0, len(o))
	for rel := range o {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	parts := make([]string, 0, len(rels))
	for _, rel := range rels {
		hash := o[rel]
		if len(hash) > 12 {
			hash = hash[:12]
		}
		parts = append(parts, rel+"="+hash)
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// diskObservation is the ground truth: a plain walk that reads and parses every
// note, with no cache anywhere in the path. Notes that fail to parse are
// skipped, mirroring the way Result.Notes excludes them.
func diskObservation(t *testing.T, v *vault.Vault) observation {
	t.Helper()

	out := observation{}
	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if err := n.Load(); err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(v.Root, n.Path)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = n.SourceHash()
		return nil
	})
	if err != nil {
		t.Fatalf("direct walk: %v", err)
	}
	return out
}

func resultObservation(t *testing.T, root string, result Result) observation {
	t.Helper()

	out := observation{}
	for _, n := range result.Notes {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			t.Fatalf("rel %s: %v", n.Path, err)
		}
		out[filepath.ToSlash(rel)] = n.SourceHash()
	}
	return out
}

func compareObservations(t *testing.T, step, loader string, want, got observation) {
	t.Helper()

	if want.String() == got.String() {
		return
	}
	t.Errorf("after %s the %s disagreed with a direct read\n got: %v\nwant: %v", step, loader, got, want)
}

// assertEveryLoaderAgrees runs the same vault through four loaders that differ
// only in how much they are allowed to remember, and requires one answer from
// all of them:
//
//   - a direct walk, which remembers nothing (the ground truth);
//   - the long-lived index, which has followed every step of the script;
//   - a fresh index over the state file that one just wrote, which is the next
//     `bn` process starting on this vault;
//   - an index with the state file removed, which is a cold start.
//
// Running all four after *every* mutation is the point: a cache bug that only
// shows up on the second mutation after a rename has nowhere to hide.
func assertEveryLoaderAgrees(t *testing.T, step string, v *vault.Vault, warm *Index) {
	t.Helper()

	want := diskObservation(t, v)

	warmResult := mustLoad(t, warm)
	compareObservations(t, step, "in-process warm index", want, resultObservation(t, v.Root, warmResult))
	if len(warmResult.Failed) != 0 {
		t.Errorf("after %s the warm index reported failures %v", step, warmResult.Failed)
	}

	diskWarm, diskLogs := newTestIndex(t, v)
	diskResult := mustLoad(t, diskWarm)
	compareObservations(t, step, "state-file warm index", want, resultObservation(t, v.Root, diskResult))
	if len(*diskLogs) != 0 {
		t.Errorf("after %s a healthy warm start logged %v", step, *diskLogs)
	}

	// Removing the state directory is the documented repair, so an index built
	// after it has nothing at all to go on. Whatever it returns is the answer
	// the two cached loaders above were only ever allowed to reproduce.
	if err := os.RemoveAll(Dir(v.Root)); err != nil {
		t.Fatalf("after %s: remove index dir: %v", step, err)
	}
	cold, _ := newTestIndex(t, v)
	compareObservations(t, step, "cold index", want, resultObservation(t, v.Root, mustLoad(t, cold)))
}

func chtimes(t *testing.T, abs string, when time.Time) {
	t.Helper()

	if err := os.Chtimes(abs, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", abs, err)
	}
}

// TestInvalidationSurvivesEveryMutationShape scripts the mutation table against
// one long-lived index, checking all four loaders after each step. The steps
// are ordered so that each one leaves the cache in a different state for the
// next — a rename follows a size-preserving edit, a backwards clock follows a
// delete — because the interesting failures are the ones that need two events.
func TestInvalidationSurvivesEveryMutationShape(t *testing.T) {
	v := testVault(t, map[string]string{
		"Notes/Alpha.md":   "---\nstatus: wip\n---\nalpha\n",
		"Notes/Beta.md":    "---\nstatus: done\n---\nbeta\n",
		"Archive/Gamma.md": "---\ntags: [x]\n---\ngamma\n",
	})
	root := v.Root
	backdate(t, root, time.Minute)

	warm, warmLogs := newTestIndex(t, v)
	assertEveryLoaderAgrees(t, "the initial load", v, warm)

	steps := []struct {
		name string
		do   func()
	}{
		{"a create", func() {
			writeNote(t, root, "Notes/Delta.md", "---\nstatus: wip\n---\ndelta\n")
		}},
		{"an edit in place preserving size", func() {
			// Byte-for-byte the same length as the cached revision: stat sees
			// nothing but the mtime, so this is the edit that decides whether
			// the cache is keyed on anything real.
			writeNote(t, root, "Notes/Alpha.md", "---\nstatus: wip\n---\nALPHA\n")
		}},
		{"an edit in place preserving size and dated into the past", func() {
			// The same edit again, but with an old mtime, so the stale window
			// has nothing to say about it. Size is equal and the file is far
			// outside the window: the mtime comparison is the only thing left
			// that can notice. A restored backup, `rsync -t`, or a checkout
			// that preserves timestamps all produce exactly this.
			abs := filepath.Join(root, "Notes", "Alpha.md")
			writeNote(t, root, "Notes/Alpha.md", "---\nstatus: wip\n---\nalpha\n")
			chtimes(t, abs, time.Date(2001, 9, 9, 1, 46, 40, 0, time.UTC))
		}},
		{"an edit changing size", func() {
			writeNote(t, root, "Notes/Beta.md", "---\nrating: 3\nstatus: done\n---\nbeta, at some length\n")
		}},
		{"a rename", func() {
			from := filepath.Join(root, "Notes", "Beta.md")
			to := filepath.Join(root, "Notes", "Beta Renamed.md")
			if err := os.Rename(from, to); err != nil {
				t.Fatalf("rename: %v", err)
			}
		}},
		{"a move across folders", func() {
			from := filepath.Join(root, "Notes", "Delta.md")
			to := filepath.Join(root, "Archive", "Delta.md")
			if err := os.Rename(from, to); err != nil {
				t.Fatalf("move: %v", err)
			}
		}},
		{"a delete", func() {
			if err := os.Remove(filepath.Join(root, "Archive", "Gamma.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}},
		{"an mtime pushed backwards", func() {
			// A restored backup, an rsync with -t, or a clock correction. The
			// content is unchanged, so the cached bytes stay right — but the
			// recorded mtime no longer matches, which must force a re-read
			// rather than be read as "even older, therefore even safer".
			chtimes(t, filepath.Join(root, "Notes", "Alpha.md"), time.Date(1990, 3, 1, 12, 0, 0, 0, time.UTC))
		}},
		{"a touch with no content change", func() {
			chtimes(t, filepath.Join(root, "Notes", "Alpha.md"), time.Now())
		}},
		{"an external writer bypassing bn", func() {
			// Obsidian, or `sed -i`, or a sync client: a bare os.WriteFile with
			// no lock taken, no compare-and-swap, and no notification to this
			// process that anything happened.
			//
			// The size changes here on purpose. An external write that also
			// preserved size *and* restored an old mtime is invisible to stat by
			// construction and the index serves the cached revision by design
			// (see TestCleanEntryIsServedFromCacheWithoutReading); staleWindow is
			// what buys back the realistic form of that, where the mtime is
			// recent, and TestSameSecondSameSizeEditIsNeverServedFromCache is
			// where that case is pinned.
			abs := filepath.Join(root, "Notes", "Alpha.md")
			if err := os.WriteFile(abs, []byte("---\nstatus: done\ntags: [edited]\n---\nrewritten outside bn\n"), 0o644); err != nil {
				t.Fatalf("external write: %v", err)
			}
		}},
		{"a delete of the last note in a folder", func() {
			if err := os.Remove(filepath.Join(root, "Archive", "Delta.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}},
	}

	for _, step := range steps {
		step.do()
		assertEveryLoaderAgrees(t, step.name, v, warm)
	}

	if len(*warmLogs) != 0 {
		t.Errorf("no step in this script is a failure class, but the index logged %v", *warmLogs)
	}
}

// TestSameSecondSameSizeEditIsNeverServedFromCache is the staleWindow
// regression test, and the one case the mutation script above deliberately
// leaves alone.
//
// Two revisions of the same note with the same byte length and the same whole
// second in their mtime are indistinguishable to stat on a filesystem with
// one-second mtime granularity — HFS+ and several network filesystems. Nothing
// but "that mtime is too recent to trust" can tell them apart, so removing the
// window would make this test, and only this test, go red.
//
// The clock is frozen rather than slept against, so the assertion holds no
// matter how long the test itself takes to run.
func TestSameSecondSameSizeEditIsNeverServedFromCache(t *testing.T) {
	v := testVault(t, map[string]string{"Alpha.md": "---\nstatus: wip\n---\nalpha\n"})
	abs := filepath.Join(v.Root, "Alpha.md")

	tick := time.Now().Truncate(time.Second)
	inWindow := func() time.Time { return tick.Add(staleWindow / 2) }
	pastWindow := func() time.Time { return tick.Add(time.Hour) }

	newAt := func(now func() time.Time) *Index {
		t.Helper()
		ix, err := New(v, WithClock(now), WithLogger(nil))
		if err != nil {
			t.Fatalf("new index: %v", err)
		}
		return ix
	}

	chtimes(t, abs, tick)
	guarded := newAt(inWindow)
	// The control proves the trap is real rather than hypothetical: an index
	// whose clock has moved past the window sees the identical stat and is
	// entitled to answer from cache.
	control := newAt(pastWindow)

	for _, ix := range []*Index{guarded, control} {
		if body := firstBody(t, mustLoad(t, ix)); !strings.Contains(body, "alpha") {
			t.Fatalf("precondition: body = %q, want the first revision", body)
		}
	}

	// The same-second, same-size edit. Neither size nor mtime moves.
	const edited = "---\nstatus: wip\n---\nbravo\n"
	if err := os.WriteFile(abs, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}
	chtimes(t, abs, tick)

	after := mustLoad(t, guarded)
	if !after.Changed {
		t.Error("a same-second same-size edit must be reported as Changed")
	}
	if body := firstBody(t, after); !strings.Contains(body, "bravo") {
		t.Errorf("body = %q, want the re-read revision: size and mtime are identical, so only staleWindow can catch this edit", body)
	}
	if got, want := after.Notes[0].SourceHash(), vault.HashBytes([]byte(edited)); got != want {
		t.Errorf("SourceHash = %q, want the hash of the bytes on disk %q", got, want)
	}

	if body := firstBody(t, mustLoad(t, control)); !strings.Contains(body, "alpha") {
		t.Errorf("control body = %q: an index past the window is supposed to trust the stat, so the assertion above is not testing what it claims", body)
	}

	// A second process starting on the state file the guarded index just wrote
	// must reach the same conclusion, since the recorded mtime is still inside
	// the window as far as its clock is concerned.
	fresh := newAt(inWindow)
	if body := firstBody(t, mustLoad(t, fresh)); !strings.Contains(body, "bravo") {
		t.Errorf("state-file warm body = %q, want the re-read revision", body)
	}
}

func firstBody(t *testing.T, result Result) string {
	t.Helper()

	if len(result.Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(result.Notes))
	}
	body, err := result.Notes[0].Body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	return body
}
