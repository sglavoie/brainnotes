package vaultexport

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// TestBaselineChunksAcrossPasses covers a first export too large for one pass.
// Every slice is durable before the next begins, so the work resumes where it
// stopped and no path is described twice.
func TestBaselineChunksAcrossPasses(t *testing.T) {
	f := newFixture(t)
	pinned := f.vault.Head(t)
	bounded := Options{MaxBaselineBytes: 1, MaxPackages: 2}

	first := f.mustExport(bounded)
	if !first.Baseline || len(first.Packages) != 2 {
		t.Fatalf("first pass: baseline=%v packages=%d", first.Baseline, len(first.Packages))
	}
	if first.PendingPaths != len(sortedSeed())-2 {
		t.Fatalf("pending paths: got %d, want %d", first.PendingPaths, len(sortedSeed())-2)
	}
	// The cursor stays empty while the baseline is unfinished: the commit it
	// describes is only fully sent once every remaining path has been.
	if first.Cursor != "" {
		t.Fatalf("cursor advanced mid-baseline: %q", first.Cursor)
	}
	f.requireBaselinePin(pinned)

	f.mustExport(bounded)
	last := f.mustExport(bounded)
	if last.Cursor != pinned {
		t.Fatalf("cursor after the last slice: got %q, want %q", last.Cursor, pinned)
	}
	if last.PendingPaths != 0 {
		t.Fatalf("pending paths: got %d, want 0", last.PendingPaths)
	}
	if got := f.state().Baseline; got != "" {
		t.Fatalf("the finished baseline is still pinned to %q", got)
	}

	// Every seeded path exactly once, across every pass.
	requirePaths(t, f.channelPaths(), sortedSeed())
	if done := f.mustExport(bounded); len(done.Packages) != 0 {
		t.Fatalf("a finished baseline published %d more packages", len(done.Packages))
	}
}

// TestBaselinePinsItsCommit is the reason State.Baseline exists.
//
// A chunked baseline resumed against a *newer* tip would skip every path an
// earlier slice already carried, because the path ledger doubles as its progress
// record — so an edit made to one of those paths in between would never travel
// at all. Pinned, the baseline finishes describing the vault as it was, and the
// edit travels afterwards as the commit it actually was.
func TestBaselinePinsItsCommit(t *testing.T) {
	f := newFixture(t)
	pinned := f.vault.Head(t)
	bounded := Options{MaxBaselineBytes: 1, MaxPackages: 2}

	first := f.mustExport(bounded)
	requirePaths(t, f.paths(first), sortedSeed()[:2])
	carried := f.operationFor(first, "Alpha.md")

	const edited = "edited while the baseline was still going\n"
	f.write("Alpha.md", edited)
	f.commit("edit a path the baseline already carried")

	rest := f.mustExport(Options{MaxBaselineBytes: 1})
	if rest.Cursor != pinned {
		t.Fatalf("the baseline finished at %q, want the commit it was pinned to %q", rest.Cursor, pinned)
	}
	requirePaths(t, f.channelPaths(), sortedSeed())

	// The edit is now an ordinary incremental export, causally after the baseline
	// slice that carried the path.
	result := f.mustExport(Options{})
	requirePaths(t, f.paths(result), []string{"Alpha.md"})
	if got := f.payload(result, "Alpha.md"); got != edited {
		t.Fatalf("Alpha.md payload: got %q, want %q", got, edited)
	}
	requirePredecessors(t, f.changeFor(result, "Alpha.md"), carried)
}

// TestBaselineHaltsWhenItsCommitLeavesTheBranch refuses to finish a baseline
// against a history that was rewritten under it: the slices already sent
// describe content the vault no longer claims.
func TestBaselineHaltsWhenItsCommitLeavesTheBranch(t *testing.T) {
	f := newFixture(t)
	base := f.vault.Head(t)

	f.write("Alpha.md", "the version the baseline was pinned to\n")
	f.commit("edit Alpha")
	if first := f.mustExport(Options{MaxBaselineBytes: 1, MaxPackages: 1}); first.Cursor != "" {
		t.Fatalf("the bounded baseline finished in one pass: %+v", first)
	}

	f.vault.Git(t, "reset", "--hard", "-q", base)

	_, err := f.export(Options{MaxBaselineBytes: 1, MaxPackages: 1})
	if !errors.Is(err, ErrHistoryRewritten) {
		t.Fatalf("error: got %v, want ErrHistoryRewritten", err)
	}
	if !strings.Contains(err.Error(), "Reset re-baselines") {
		t.Fatalf("the refusal must name its remedy: %v", err)
	}
}

// TestBaselineSendsAFileLargerThanTheBudget pins the "always at least one"
// rule: the budget bounds how much this producer holds at once, not what it is
// willing to send, so a vault does not become unexportable for holding one large
// attachment.
func TestBaselineSendsAFileLargerThanTheBudget(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("x", 4096)
	f.write("Big.md", big)
	f.commit("add one large file")

	result := f.mustExport(Options{MaxBaselineBytes: 8})
	requirePaths(t, f.paths(result), []string{
		"Alpha.md", "Beta.md", "Big.md", "Notes/Gamma.md", vaultfixture.UnrelatedNote, "brainnotes.toml",
	})
	if got := f.payload(result, "Big.md"); got != big {
		t.Fatalf("Big.md payload is %d bytes, want %d", len(got), len(big))
	}
	for _, published := range result.Packages {
		manifest := f.manifest(published.Dir)
		paths := manifest.Paths()
		for _, p := range paths {
			if p != "Big.md" {
				continue
			}
			if len(paths) != 1 {
				t.Fatalf("the oversized file travelled with %v, want alone", paths)
			}
		}
	}
}

// TestBaselineSliceIsOneOperation pins the shape of a slice: one operation over
// paths no other operation claimed, every change expecting the path absent.
func TestBaselineSliceIsOneOperation(t *testing.T) {
	f := newFixture(t)
	result := f.mustExport(Options{MaxBaselineBytes: 1})

	seen := map[string]struct{}{}
	for _, published := range result.Packages {
		manifest := f.manifest(published.Dir)
		if len(manifest.Operations) != 1 {
			t.Fatalf("%s carries %d operations, want 1", published.PackageID, len(manifest.Operations))
		}
		identity := manifest.Operations[0].OperationID
		if _, repeated := seen[identity]; repeated {
			t.Fatalf("two baseline slices share the operation identity %q", identity)
		}
		seen[identity] = struct{}{}
		if published.Commit != result.Cursor {
			t.Fatalf("%s describes %q, want the pinned commit %q", published.PackageID, published.Commit, result.Cursor)
		}
	}
	if len(seen) != len(sortedSeed()) {
		t.Fatalf("slices: got %d, want one per seeded path (%d)", len(seen), len(sortedSeed()))
	}
}

// TestNextSliceTakesAtLeastOneAndStopsAtTheBudget pins the budget rule directly,
// at the boundary the end-to-end tests cannot reach.
//
// The "always at least one" clause is the load-bearing half: a vault holding a
// single attachment larger than the budget must stay exportable, because the
// budget bounds what this producer chooses to hold at once and not what it is
// willing to send. Without it, that vault's baseline would cut an empty slice
// forever.
func TestNextSliceTakesAtLeastOneAndStopsAtTheBudget(t *testing.T) {
	files := []vaultgit.TreeFile{
		{Path: "a.md", Size: 4},
		{Path: "b.md", Size: 4},
		{Path: "c.md", Size: 4},
	}

	for _, tc := range []struct {
		name   string
		budget int64
		want   int
	}{
		{name: "the whole tree fits", budget: 12, want: 3},
		{name: "one byte short of the whole tree", budget: 11, want: 2},
		{name: "exactly two", budget: 8, want: 2},
		{name: "one byte short of two", budget: 7, want: 1},
		{name: "smaller than any single file", budget: 3, want: 1},
		{name: "no budget at all", budget: 0, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exp := &exporter{maxBytes: tc.budget}
			if got := len(exp.nextSlice(files)); got != tc.want {
				t.Fatalf("slice: got %d files, want %d", got, tc.want)
			}
		})
	}

	exp := &exporter{maxBytes: 1}
	if got := exp.nextSlice(nil); len(got) != 0 {
		t.Fatalf("an empty tree cut %d files", len(got))
	}
}
