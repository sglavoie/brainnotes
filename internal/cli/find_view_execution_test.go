package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
)

// A saved view stores four things — query, sort, relative_to, limit — and every
// one of them is part of its definition. These tests pin that running a view
// runs all four, because the failure they guard is silent: a view that
// `bn views` reports as enabled while `--view` executes something wider than
// what was saved looks identical to a working view until you count the results.

// viewExecutionProperties declares the typed properties the fixture uses. `last`
// is what the `modified` ordering reads.
const viewExecutionProperties = `[frontmatter.properties.status]
type = "string"

[frontmatter.properties.last]
type = "date"
`

// buildViewExecutionVault writes four notes whose `last` dates are deliberately
// out of step with their path order in both directions, so that path order,
// `modified` ascending, and `modified` descending each produce a different first
// two notes. A result that merely kept the path order therefore cannot pass for
// a correctly sorted one, and a cap applied before the sort cannot pass for one
// applied after it.
func buildViewExecutionVault(t *testing.T, views string) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Notes/Alpha.md": "---\nstatus: wip\nlast: 2024-01-02\n---\nAlpha body.\n",
		"Notes/Bravo.md": "---\nstatus: wip\nlast: 2024-01-01\n---\nBravo body.\n",
		"Notes/Delta.md": "---\nstatus: wip\nlast: 2024-01-03\n---\nDelta body.\n",
		"Notes/Echo.md":  "---\nstatus: done\nlast: 2024-01-04\n---\nEcho body.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	markVaultRoot(t, root, viewExecutionProperties+views)
	return root
}

// TestFindViewHonorsSortAndLimit is the core regression: a view saving a
// non-default ordering and a non-zero cap must execute both, and the cap must be
// applied *after* the ordering so it keeps the top N rather than N arbitrary
// matches.
func TestFindViewHonorsSortAndLimit(t *testing.T) {
	root := buildViewExecutionVault(t, `
[[views]]
name = "Recent WIP"
query = "status:wip"
sort = "-modified"
limit = 2

[[views]]
name = "Sorted only"
query = "status:wip"
sort = "-modified"

[[views]]
name = "Capped only"
query = "status:wip"
limit = 2
`)

	// Newest first, then capped at two. Path order would have kept Alpha and
	// Bravo, so this fails if either half of the view is dropped.
	got := runFindOrdered(t, root, "find", "--view", "Recent WIP")
	if want := []string{"Notes/Delta.md", "Notes/Alpha.md"}; !equalStrings(got, want) {
		t.Fatalf("find --view 'Recent WIP':\n got=%v\nwant=%v", got, want)
	}

	// The ordering on its own is the whole match set, newest first.
	got = runFindOrdered(t, root, "find", "--view", "Sorted only")
	if want := []string{"Notes/Delta.md", "Notes/Alpha.md", "Notes/Bravo.md"}; !equalStrings(got, want) {
		t.Fatalf("find --view 'Sorted only':\n got=%v\nwant=%v", got, want)
	}

	// A cap with no ordering caps the default path order.
	got = runFindOrdered(t, root, "find", "--view", "Capped only")
	if want := []string{"Notes/Alpha.md", "Notes/Bravo.md"}; !equalStrings(got, want) {
		t.Fatalf("find --view 'Capped only':\n got=%v\nwant=%v", got, want)
	}
}

// buildRelevanceTieVault writes a vault in which two notes are exactly equally
// related to the anchor.
//
// Mike and November carry the same body and the same frontmatter and differ
// only by name, so nothing the ranker reads — text, metadata, graph, recency —
// separates them: they tie, and only the path tie-break orders them. Delta is
// linked from the anchor and so outranks both, which is what puts the tied pair
// at the *bottom* of a `relevance` ordering and therefore at the top of a
// `-relevance` one, where a limit can see them.
func buildRelevanceTieVault(t *testing.T, views string) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Notes/Alpha.md":    "---\nstatus: wip\n---\nAlpha body links to [[Delta]].\n",
		"Notes/Delta.md":    "---\nstatus: wip\n---\nDelta body.\n",
		"Notes/Mike.md":     "---\nstatus: wip\n---\nTie body.\n",
		"Notes/November.md": "---\nstatus: wip\n---\nTie body.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	markVaultRoot(t, root, viewExecutionProperties+views)
	return root
}

// TestFindViewHonorsDescendingRelevanceAndLimit pins the CLI half of the shared
// definition of descending relevance, and is the regression for a saved view
// selecting a different top N at the terminal than in the browser.
//
// `-relevance` reverses the score comparison and nothing else: equal-score notes
// stay in ascending path order in both directions, because the path tie-break is
// a stability device rather than part of the requested ordering. Reversing the
// whole ordered slice instead — which is what `bn find` used to do — flips the
// tied pair, so a view carrying `limit = 2` returned November before Mike here
// while `/api/search` returned Mike before November from the same stored view.
//
// internal/web's TestSearchAppliesSavedViewDescendingRelevanceAndLimit asserts
// the same paths for the same vault; the two together are the parity claim.
func TestFindViewHonorsDescendingRelevanceAndLimit(t *testing.T) {
	root := buildRelevanceTieVault(t, `
[[views]]
name = "Least related"
query = "status:wip"
sort = "-relevance"
relative_to = "Alpha"
limit = 2

[[views]]
name = "Least related uncapped"
query = "status:wip"
sort = "-relevance"
relative_to = "Alpha"

[[views]]
name = "Most related"
query = "status:wip"
sort = "relevance"
relative_to = "Alpha"
`)
	pinFindClock(t)

	// Most related first: the anchor leads the ordering it is the measure of,
	// its linked note follows, and the tied pair trails in path order.
	got := runFindOrdered(t, root, "find", "--view", "Most related")
	want := []string{"Notes/Alpha.md", "Notes/Delta.md", "Notes/Mike.md", "Notes/November.md"}
	if !equalStrings(got, want) {
		t.Fatalf("find --view 'Most related':\n got=%v\nwant=%v", got, want)
	}

	// Least related first: scores reverse, the tie does not. A whole-slice
	// reversal would answer November, Mike, Delta, Alpha.
	got = runFindOrdered(t, root, "find", "--view", "Least related uncapped")
	want = []string{"Notes/Mike.md", "Notes/November.md", "Notes/Delta.md", "Notes/Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("find --view 'Least related uncapped':\n got=%v\nwant=%v", got, want)
	}

	// The cap is what makes the tie orientation observable as *which* notes come
	// back rather than only their order.
	got = runFindOrdered(t, root, "find", "--view", "Least related")
	want = []string{"Notes/Mike.md", "Notes/November.md"}
	if !equalStrings(got, want) {
		t.Fatalf("find --view 'Least related':\n got=%v\nwant=%v", got, want)
	}
}

// TestFindViewSortLosesToExplicitFlags pins the precedence rule: --sort and
// --relative-to on the command line replace a view's ordering wholesale. The cap
// is not a flag, so it survives an ordering override — overriding how results
// are ordered is not a request for more of them.
func TestFindViewSortLosesToExplicitFlags(t *testing.T) {
	root := buildViewExecutionVault(t, `
[[views]]
name = "Recent WIP"
query = "status:wip"
sort = "-modified"
limit = 2
`)

	got := runFindOrdered(t, root, "find", "--view", "Recent WIP", "--sort", "modified")
	if want := []string{"Notes/Bravo.md", "Notes/Alpha.md"}; !equalStrings(got, want) {
		t.Fatalf("find --view 'Recent WIP' --sort modified:\n got=%v\nwant=%v", got, want)
	}

	// An explicit `path` sort is still explicit: it must win over the stored
	// `-modified` rather than reading as "no ordering requested".
	got = runFindOrdered(t, root, "find", "--view", "Recent WIP", "--sort", "path")
	if want := []string{"Notes/Alpha.md", "Notes/Bravo.md"}; !equalStrings(got, want) {
		t.Fatalf("find --view 'Recent WIP' --sort path:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindViewRejectsADisabledView pins that `--view` sees exactly what
// `bn views` sees. A view whose stored sort is unusable is rendered disabled by
// the resolver, so running it has to fail rather than quietly degrade to a
// query-only filter — which is what made a view report as enabled while
// executing as something else.
func TestFindViewRejectsADisabledView(t *testing.T) {
	cases := []struct {
		name  string
		views string
		want  string
	}{
		{
			name: "unknown sort key",
			views: `
[[views]]
name = "Broken sort"
query = "status:wip"
sort = "recency"
`,
			want: "unknown sort key",
		},
		{
			name: "relevance with no anchor",
			views: `
[[views]]
name = "Anchorless"
query = "status:wip"
sort = "relevance"
`,
			want: "requires relative_to",
		},
		{
			name: "anchor without relevance",
			views: `
[[views]]
name = "Stray anchor"
query = "status:wip"
sort = "-modified"
relative_to = "Notes/Alpha.md"
`,
			want: `relative_to requires sort "relevance"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := buildViewExecutionVault(t, tc.views)

			// The vault still loads: one unusable view never breaks an
			// unrelated query.
			if got := runFind(t, root, "find", "--status", "wip"); len(got) != 3 {
				t.Fatalf("an unusable saved view must not break an unrelated query: got %v", got)
			}

			name := strings.TrimSpace(strings.Split(strings.Split(tc.views, "name = \"")[1], "\"")[0])
			got := runFindError(t, root, "find", "--view", name)
			if !strings.Contains(got, "is disabled") || !strings.Contains(got, tc.want) {
				t.Fatalf("find --view %q: got %q, want a disabled-view error mentioning %q", name, got, tc.want)
			}
		})
	}
}

// TestViewOrderingIsRejectedByCommandsThatCannotApplyIt pins the one documented
// policy for the rest of the CLI: `bn find` is the only flag-scoped command that
// orders and caps its output, so it is the only one that runs a view carrying
// those fields. Every other command refuses rather than running a partial view.
//
// The mutators are the reason this is a refusal rather than a silent drop:
// `bn replace --view top-10` dropping the cap would have rewritten every match.
func TestViewOrderingIsRejectedByCommandsThatCannotApplyIt(t *testing.T) {
	views := `
[[views]]
name = "Recent WIP"
query = "status:wip"
sort = "-modified"
limit = 2

[[views]]
name = "Capped"
query = "status:wip"
limit = 2

[[views]]
name = "Plain"
query = "status:wip"
`

	commands := [][]string{
		{"search", "body"},
		{"lint"},
		{"graph"},
		{"replace", "status", "wip", "done", "--dry-run"},
		{"repair", "--dry-run"},
		{"relink", "--dry-run"},
		{"rename-key", "status", "state", "--dry-run"},
	}

	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			root := buildViewExecutionVault(t, views)

			got := runFindError(t, root, append(append([]string{}, args...), "--view", "Recent WIP")...)
			if !strings.Contains(got, `stores sort "-modified"`) || !strings.Contains(got, "bn find --view") {
				t.Fatalf("%s --view 'Recent WIP': got %q, want a refusal naming the stored sort", args[0], got)
			}
			if !strings.Contains(got, args[0]) {
				t.Fatalf("%s --view 'Recent WIP': got %q, want the offending command named", args[0], got)
			}

			got = runFindError(t, root, append(append([]string{}, args...), "--view", "Capped")...)
			if !strings.Contains(got, "stores limit 2") {
				t.Fatalf("%s --view Capped: got %q, want a refusal naming the stored limit", args[0], got)
			}

			// A view with neither field is unaffected: the policy refuses what
			// cannot be executed, not saved views in general.
			if got := runCLIOutcome(t, root, append(append([]string{}, args...), "--view", "Plain")...); strings.Contains(got, "bn find --view") {
				t.Fatalf("%s --view Plain must not be refused: got %q", args[0], got)
			}
		})
	}
}

// runCLIOutcome executes args against vaultRoot and returns whatever the command
// produced — its rendered error, or its output when it succeeded. Unlike
// runFindError it asserts nothing about success, which is what lets one
// assertion cover "this must not be refused" without also pinning the command's
// own output shape.
func runCLIOutcome(t *testing.T, vaultRoot string, args ...string) string {
	t.Helper()
	isolateBnEnv(t)

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	if err := root.Execute(); err != nil {
		var rendered bytes.Buffer
		RenderError(&rendered, err)
		return rendered.String()
	}
	return out.String()
}

// TestOrderKeyVocabularyMatchesQueryexpr pins the two halves of the sort
// vocabulary together. query.OrderNotes cannot import queryexpr (queryexpr
// imports query), so the key literals are spelled twice; this is what stops a
// key added to one from being silently unorderable through the other.
//
// `relevance` is the one deliberate difference: it reads the recall sidecar, so
// each caller produces it with the ranker it already holds.
func TestOrderKeyVocabularyMatchesQueryexpr(t *testing.T) {
	want := queryexpr.SortKeys()
	got := append(query.OrderKeys(), queryexpr.SortRelevance)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order key vocabulary drift:\n query+relevance=%v\n      queryexpr=%v", got, want)
	}

	// Every key queryexpr accepts must be executable by `bn find`, which is the
	// property the two lists exist to guarantee.
	for _, key := range want {
		if key == queryexpr.SortRelevance {
			continue
		}
		if _, err := query.OrderNotes(nil, key, false, query.OrderInput{}); err != nil {
			t.Fatalf("query.OrderNotes cannot produce %q, which queryexpr accepts: %v", key, err)
		}
	}
}

func TestFindSavedTextMatchOrdering(t *testing.T) {
	root := buildViewExecutionVault(t, `
[[views]]
name = "Text matches"
query = "status:wip"
search = "Bravo"
sort = "match"
limit = 1
`)
	// Alpha matches only in the body and sorts earlier alphabetically. A title
	// hit must win before the cap, exactly as the browser's best-match search.
	path := filepath.Join(root, "Notes", "Alpha.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("Bravo mention.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runFindOrdered(t, root, "find", "--view", "Text matches")
	if !equalStrings(got, []string{"Notes/Bravo.md"}) {
		t.Fatalf("match order: %v", got)
	}
}
