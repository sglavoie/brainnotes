package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// This file is the contract that makes the index safe to switch on: whatever a
// query says about the vault, it must say identically whether the notes came
// from a direct load or from a warm index. It imports queryexpr and query,
// which production code in this package must never do — see layering_test.go
// for why that test-only edge is allowed.

// parityFixture is deliberately awkward: a link graph with a hub, an orphan and
// a dead end, notes in and out of a subfolder, mixed frontmatter shapes, a
// non-markdown file, a hidden file, a hidden directory, and a note whose
// frontmatter does not parse. Every one of those is a place the two load paths
// could diverge.
var parityFixture = map[string]string{
	"Hub.md":              "---\nstatus: wip\ntags: [core, ops]\nrating: 5\n---\nSee [[Alpha]] and [[Beta]] and [[Notes/Delta]].\n",
	"Alpha.md":            "---\nstatus: wip\ntags: [core]\nrating: 3\nparent: \"[[Hub]]\"\n---\nLinks to [[Beta]].\n",
	"Beta.md":             "---\nstatus: done\ntags: [ops]\nrating: 1\n---\nA dead end with no links.\n",
	"Notes/Delta.md":      "---\nstatus: wip\ntags: [core, deep]\n---\nBack to [[Hub]] and out to [[Nowhere At All]].\n",
	"Notes/Epsilon.md":    "---\ntags: []\n---\nAn orphan that links nowhere.\n",
	"Archive/Zeta.md":     "---\nstatus: done\nreviewed: true\n---\nArchived, mentions [[Alpha]].\n",
	"Archive/Broken.md":   "---\nstatus: [unterminated\n---\nUnparseable frontmatter.\n",
	"Notes/reference.txt": "not a note at all",
	"Notes/.hidden.md":    "---\n---\nhidden file\n",
	".vscode/Notes.md":    "---\n---\ninside a hidden directory\n",
}

// parityQueries exercise every distinguishable shape the language offers over
// this fixture: frontmatter fields, presence and absence, folder scoping,
// typed comparison, boolean composition with not/or, and the graph fields,
// which are the ones that depend on the whole note set rather than on one note.
var parityQueries = []string{
	"",
	"status:wip",
	"status:done",
	"tag:core",
	"tag:core and status:wip",
	"tag:core or tag:ops",
	"not tag:core",
	"has:rating",
	"missing:rating",
	"prop:rating>=3",
	"prop:rating<3",
	"folder:Notes",
	"not folder:Archive",
	"reviewed:true",
	"orphan:true",
	"dead-end:true",
	"broken-links:true",
	"backlinks>=1",
	"links>0",
	"links-to:[[Hub]]",
	"linked-from:[[Hub]]",
	"near:[[Hub]]<=1",
	"near:[[Hub]]<=2",
	"path-to:[[Hub]]",
	"degree>=2",
	"cluster-size>=3",
	"(tag:core or status:done) and not folder:Archive",
	"near:[[Hub]]<=2 and status:wip and not folder:Archive",
}

// TestIndexLoadMatchesDirectLoadForEveryQuery runs the whole query table twice.
// The first pass is over a cold index that has just read every note; the second
// is over the same index with nothing changed, so every entry takes the clean
// stat-only path. Both passes must agree with a direct load, key for key and in
// the same order — a cache that returns the right set in the wrong order is
// still a behaviour change for a caller that pages results.
func TestIndexLoadMatchesDirectLoadForEveryQuery(t *testing.T) {
	v := testVault(t, parityFixture)
	// The fixture was written milliseconds ago, so without this every entry sits
	// inside the stale window and the second pass would re-read too — leaving the
	// all-clean path untested, which is the pass this test exists to cover.
	backdate(t, v.Root, time.Minute)

	ix, logs := newTestIndex(t, v)
	direct := directLoadNotes(t, v)

	for pass := 1; pass <= 2; pass++ {
		result := mustLoad(t, ix)

		if pass == 2 && result.Changed {
			t.Error("pass 2: an untouched vault reported Changed, so the second pass did not exercise the all-clean path")
		}
		if got, want := orderedKeys(t, v.Root, result.Notes), orderedKeys(t, v.Root, direct); !equalStrings(got, want) {
			t.Fatalf("pass %d: index note set = %v, want %v", pass, got, want)
		}

		for _, src := range parityQueries {
			want := evalKeys(t, v, direct, src)
			got := evalKeys(t, v, result.Notes, src)
			if !equalStrings(got, want) {
				t.Errorf("pass %d: %q\n index  = %v\n direct = %v", pass, src, got, want)
			}
		}
	}

	if len(*logs) != 0 {
		t.Errorf("a healthy vault produced index warnings: %v", *logs)
	}
}

// evalKeys compiles src against the vault and returns the vault-relative paths
// it selects, in the order the notes were supplied. The graph is built from the
// same note slice being filtered, so a divergence in the note set shows up as a
// divergence in the graph fields too rather than being papered over by a shared
// graph.
func evalKeys(t *testing.T, v *vault.Vault, notes []*vault.Note, src string) []string {
	t.Helper()

	env := queryexpr.Env{
		Root:        v.Root,
		Frontmatter: v.Frontmatter(),
		Graph:       query.BuildGraphForRoot(v.Root, notes),
	}
	plan, err := queryexpr.Compile(src, env)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	predicate, err := plan.Predicate(env)
	if err != nil {
		t.Fatalf("lower %q: %v", src, err)
	}

	var keys []string
	for _, note := range notes {
		if predicate(note) {
			keys = append(keys, relKey(t, v.Root, note))
		}
	}
	return keys
}

func orderedKeys(t *testing.T, root string, notes []*vault.Note) []string {
	t.Helper()

	keys := make([]string, 0, len(notes))
	for _, note := range notes {
		keys = append(keys, relKey(t, root, note))
	}
	return keys
}

func relKey(t *testing.T, root string, note *vault.Note) string {
	t.Helper()

	rel, err := filepath.Rel(root, note.Path)
	if err != nil {
		t.Fatalf("rel %s: %v", note.Path, err)
	}
	return filepath.ToSlash(rel)
}

// directLoadNotes is the baseline the index must match: the same walk and the
// same classification internal/web's loadVaultNotes performs
// (internal/web/loader.go:60) — v.Walk(vault.WalkFilter{}, ...), (*Note).Load
// per file, notes that fail to load dropped from the healthy set rather than
// aborting the walk.
//
// It is reproduced here rather than called, because loadVaultNotes is
// unexported in internal/web and this package must not import web. That is
// precisely why the parity table exists: two copies of a walk are exactly the
// thing that drifts, so the assertion is on the observable result of both.
func directLoadNotes(t testing.TB, v *vault.Vault) []*vault.Note {
	t.Helper()

	var notes []*vault.Note
	err := v.Walk(vault.WalkFilter{}, func(note *vault.Note) error {
		if err := note.Load(); err != nil {
			return nil
		}
		notes = append(notes, note)
		return nil
	})
	if err != nil {
		t.Fatalf("direct walk: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("the direct load found no notes; the parity comparison would be vacuous")
	}
	return notes
}
