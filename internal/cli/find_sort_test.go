package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pinFindClock freezes the recall clock for the duration of a test. Relevance
// ordering reads recency and snooze expiry off it, so it is what makes a
// relevance-sorted query reproducible.
func pinFindClock(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.FixedZone("find-sort", -5*60*60))
	original := recallNow
	recallNow = func() time.Time { return now }
	t.Cleanup(func() { recallNow = original })
	return now
}

// runFindOrdered executes find and returns the printed paths in the order they
// were printed. runFind sorts its result lexically, which is exactly the
// information a sort test needs to keep.
func runFindOrdered(t *testing.T, vaultRoot string, args ...string) []string {
	t.Helper()
	var rels []string
	for _, line := range strings.Split(runFindRaw(t, vaultRoot, args...), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(vaultRoot, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	return rels
}

// runFindSortError executes find expecting a validation failure.
func runFindSortError(t *testing.T, vaultRoot string, args ...string) error {
	t.Helper()
	isolateBnEnv(t)

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	err := root.Execute()
	if err == nil {
		t.Fatalf("execute %v: expected an error, got none\noutput: %s", args, out.String())
	}
	return err
}

func recallSidecarPath(vaultRoot string) string {
	return filepath.Join(vaultRoot, ".bnotes", "recall.json")
}

// TestFindSort_RelevanceIsDeterministic pins the determinism contract: the same
// query, against the same vault, with the clock pinned, prints the same bytes.
func TestFindSort_RelevanceIsDeterministic(t *testing.T) {
	root := buildQueryFixtureVault(t)
	pinFindClock(t)

	first := runFindRaw(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")
	second := runFindRaw(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")
	if first != second {
		t.Fatalf("relevance ordering is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.TrimSpace(first) == "" {
		t.Fatal("relevance-sorted find printed nothing")
	}
}

// TestFindSort_RelevanceDoesNotWriteRecallSidecar pins the recall read/write
// boundary for `find`: ranking reads the sidecar as a signal source and never
// records anything, so a vault that had no recall.json still has none.
func TestFindSort_RelevanceDoesNotWriteRecallSidecar(t *testing.T) {
	root := buildQueryFixtureVault(t)
	pinFindClock(t)

	sidecar := recallSidecarPath(root)
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("fixture already has a recall sidecar: stat err = %v", err)
	}

	runFindRaw(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")

	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("find wrote the recall sidecar %s: stat err = %v", sidecar, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("find created the .bnotes directory: stat err = %v", err)
	}
}

// TestFindSort_RelevanceAnchorSortsFirst pins that the --relative-to note is
// pulled out of the candidate set before RankSelected (which rejects a
// candidate repeating the source) and re-inserted at position 0 when the query
// matched it.
func TestFindSort_RelevanceAnchorSortsFirst(t *testing.T) {
	root := buildQueryFixtureVault(t)
	pinFindClock(t)

	got := runFindOrdered(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")
	if len(got) != 4 {
		t.Fatalf("relevance-sorted find returned %v, want all four notes", got)
	}
	if got[0] != "Notes/Hub.md" {
		t.Fatalf("anchor is not first: got %v", got)
	}
}

// TestFindSort_RelevanceExcludesUnmatchedAnchor pins the other half of that
// rule: an anchor the filter did not select is not smuggled into the results by
// the ordering.
func TestFindSort_RelevanceExcludesUnmatchedAnchor(t *testing.T) {
	root := buildQueryFixtureVault(t)
	pinFindClock(t)

	got := runFind(t, root, "find", "-q", "status:done", "--sort", "relevance", "--relative-to", "Hub")
	for _, rel := range got {
		if rel == "Notes/Hub.md" {
			t.Fatalf("unmatched anchor appeared in results: %v", got)
		}
	}
	want := map[string]bool{"Notes/Spoke.md": true, "Notes/Away.md": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly the status:done notes", got)
	}
	for _, rel := range got {
		if !want[rel] {
			t.Fatalf("got %v, want exactly the status:done notes", got)
		}
	}
}

// TestFindSort_RelevanceKeepsDismissedMatches is the RankSelected-not-Rank
// contract. A dismissed pair is suppressed by Rank, so if `find` ranked with
// Rank the dismissed note would silently vanish from a query that matched it —
// the ordering would change *which* notes come back. Selection is
// authoritative, so it must not.
func TestFindSort_RelevanceKeepsDismissedMatches(t *testing.T) {
	root := buildQueryFixtureVault(t)
	pinFindClock(t)

	baseline := runFind(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")
	if len(baseline) != 4 {
		t.Fatalf("baseline = %v, want all four notes", baseline)
	}

	// Dismiss every candidate, so Rank would return an empty result set.
	for _, candidate := range []string{"Spoke", "Leaf", "Away"} {
		out := runFindRaw(t, root, "related", "feedback", "Hub", candidate, "--state", "dismissed")
		if strings.TrimSpace(out) != "" {
			t.Fatalf("related feedback %s printed %q", candidate, out)
		}
	}
	if _, err := os.Stat(recallSidecarPath(root)); err != nil {
		t.Fatalf("expected feedback to create the sidecar: %v", err)
	}

	got := runFind(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")
	if len(got) != len(baseline) {
		t.Fatalf("dismissal dropped query matches: got %v, want the same %v", got, baseline)
	}
	for _, rel := range baseline {
		found := false
		for _, candidate := range got {
			if candidate == rel {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("dismissal dropped %s: got %v", rel, got)
		}
	}
}

// TestFindSort_RelevanceIsReadOnlyAcrossExistingState pins that ranking against
// an existing sidecar leaves its bytes alone.
func TestFindSort_RelevanceIsReadOnlyAcrossExistingState(t *testing.T) {
	root := buildQueryFixtureVault(t)
	pinFindClock(t)

	runFindRaw(t, root, "related", "feedback", "Hub", "Spoke", "--state", "useful")
	sidecar := recallSidecarPath(root)
	before, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}

	runFindRaw(t, root, "find", "--sort", "relevance", "--relative-to", "Hub")

	after, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar after find: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("find rewrote the recall sidecar:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestFindSort_Keys covers the orderings that need no recall state.
func TestFindSort_Keys(t *testing.T) {
	root := buildSortFixtureVault(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "path ascending is the default ordering",
			args: nil,
			want: []string{"A/Delta.md", "B/Alpha.md", "C/Zeta.md", "D/Mu.md"},
		},
		{
			name: "path descending",
			args: []string{"--sort", "-path"},
			want: []string{"D/Mu.md", "C/Zeta.md", "B/Alpha.md", "A/Delta.md"},
		},
		{
			name: "title",
			args: []string{"--sort", "title"},
			want: []string{"B/Alpha.md", "A/Delta.md", "D/Mu.md", "C/Zeta.md"},
		},
		{
			name: "created descending",
			args: []string{"--sort", "created:desc"},
			want: []string{"B/Alpha.md", "C/Zeta.md", "A/Delta.md", "D/Mu.md"},
		},
		{
			name: "modified ascending",
			args: []string{"--sort", "modified"},
			want: []string{"A/Delta.md", "C/Zeta.md", "B/Alpha.md", "D/Mu.md"},
		},
		{
			name: "backlinks descending",
			args: []string{"--sort", "-backlinks"},
			want: []string{"C/Zeta.md", "D/Mu.md", "A/Delta.md", "B/Alpha.md"},
		},
		{
			name: "degree descending",
			args: []string{"--sort", "-degree"},
			want: []string{"B/Alpha.md", "C/Zeta.md", "A/Delta.md", "D/Mu.md"},
		},
		{
			name: "pinned",
			args: []string{"--sort", "pinned"},
			want: []string{"C/Zeta.md", "A/Delta.md", "B/Alpha.md", "D/Mu.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runFindOrdered(t, root, append([]string{"find"}, tt.args...)...)
			if !equalStrings(got, tt.want) {
				t.Fatalf("find %v:\n got=%v\nwant=%v", tt.args, got, tt.want)
			}
		})
	}
}

// TestFindSort_UndatedNotesSortLast pins that a note with no value for the key
// sorts last in *both* directions, so `--sort -modified` means "newest first"
// rather than "undated first".
func TestFindSort_UndatedNotesSortLast(t *testing.T) {
	root := buildSortFixtureVault(t)

	for _, spec := range []string{"modified", "-modified", "created", "-created"} {
		got := runFindOrdered(t, root, "find", "--sort", spec)
		if len(got) == 0 || got[len(got)-1] != "D/Mu.md" {
			t.Fatalf("find --sort %s: undated note is not last: %v", spec, got)
		}
	}
}

// TestFindSort_UnknownKeyIsRejected pins that the flag is validated before the
// vault is opened, so a typo is a flag error rather than an empty result.
func TestFindSort_UnknownKeyIsRejected(t *testing.T) {
	root := buildQueryFixtureVault(t)
	err := runFindSortError(t, root, "find", "--sort", "backlinkz")
	if !strings.Contains(err.Error(), `find: --sort must be`) || !strings.Contains(err.Error(), `(got "backlinkz")`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// buildSortFixtureVault writes a vault in which every sort key produces a
// different order, so no assertion can pass by accidentally agreeing with the
// path ordering the evaluator already returns.
//
// A note's title is its filename stem, so the folder names are what pull path
// order away from title order: A/Delta, B/Alpha, C/Zeta, D/Mu sorts by path in
// folder order and by title in stem order.
//
// Links are Delta -> Zeta and Alpha -> Zeta, Alpha -> Mu, which gives
// backlinks Zeta 2, Mu 1, Delta 0, Alpha 0 and undirected degrees Alpha 2,
// Zeta 2, Delta 1, Mu 1 — two different orderings over the same graph.
//
// Mu carries no dates, which is what the two date keys use to pin that a note
// with no value sorts last in either direction.
func buildSortFixtureVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"A/Delta.md": "---\ncreated: 2021-01-01\nlast: 2021-06-01\npinned: 2\n---\nDelta links to [[Zeta]].\n",
		"B/Alpha.md": "---\ncreated: 2023-01-01\nlast: 2023-06-01\n---\nAlpha links to [[Zeta]] and [[Mu]].\n",
		"C/Zeta.md":  "---\ncreated: 2022-01-01\nlast: 2022-06-01\npinned: 1\n---\nZeta body.\n",
		"D/Mu.md":    "---\nstatus: wip\n---\nMu body, with no dates.\n",
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
	markVaultRoot(t, root, "")
	return root
}
