package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPinFixture writes a vault whose brainnotes.toml declares no properties at
// all, so every pin test also proves that pinning works without a
// [frontmatter.properties.pinned] block. Ranks are deliberately inverted against
// path order so a pinned-order assertion cannot pass by accident.
func buildPinFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Alpha.md":   "---\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":    "---\ntitle: Beta\n---\nBeta body.\n",
		"Charlie.md": "---\npinned: 2.0\ntitle: Charlie\n---\nCharlie body.\n",
		"Delta.md":   "---\npinned: 1.0\ntitle: Delta\n---\nDelta body.\n",
		"Flagged.md": "---\npinned: true\ntitle: Flagged\n---\nFlagged body.\n",
		"Off.md":     "---\npinned: false\ntitle: Off\n---\nOff body.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	useVaultRoot(t, root, "")
	return root
}

func TestPin_AppendsRankAfterHighest(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Alpha.md")

	out, err := runCLI(t, "", "pin", notePath)
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pinned: Alpha.md") {
		t.Fatalf("expected `pinned: Alpha.md` in output, got:\n%s", out)
	}

	got := readNote(t, notePath)
	want := "---\npinned: 3.0\ntitle: Alpha\n---\nAlpha body.\n"
	if got != want {
		t.Fatalf("note =\n%q\nwant\n%q", got, want)
	}
}

// TestPin_ResolvesByTitle pins through the shared single-note resolver rather
// than a path, which is the form the command is documented with.
func TestPin_ResolvesByTitle(t *testing.T) {
	root := buildPinFixture(t)

	if out, err := runCLI(t, "", "pin", "Beta"); err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if got := readNote(t, filepath.Join(root, "Beta.md")); !strings.Contains(got, "pinned: 3.0\n") {
		t.Fatalf("expected rank 3.0 in:\n%s", got)
	}
}

// TestPin_HandWrittenFalseIsPinnable covers the Obsidian spelling: `pinned:
// false` classifies as not pinned, so pinning it writes a real rank.
func TestPin_HandWrittenFalseIsPinnable(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Off.md")

	if out, err := runCLI(t, "", "pin", notePath); err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if got := readNote(t, notePath); !strings.Contains(got, "pinned: 3.0\n") {
		t.Fatalf("expected rank 3.0 in:\n%s", got)
	}
}

func TestPin_AlreadyPinnedLeavesRankAlone(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Charlie.md")
	original := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "pin", notePath)
		if err != nil {
			t.Fatalf("pin: %v\n%s", err, o)
		}
		out = o
	})
	if !strings.Contains(out, "already pinned: Charlie.md") {
		t.Fatalf("expected `already pinned` report, got:\n%s", out)
	}
	if got := readNote(t, notePath); got != original {
		t.Fatalf("note changed:\n--- before ---\n%s--- after ---\n%s", original, got)
	}
}

// TestPin_UnrankedIsAlreadyPinned proves a hand-written `pinned: true` counts as
// pinned, so a re-pin does not silently overwrite the user's flag with a rank.
func TestPin_UnrankedIsAlreadyPinned(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Flagged.md")

	out, err := runCLI(t, "", "pin", notePath)
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already pinned: Flagged.md") {
		t.Fatalf("expected `already pinned` report, got:\n%s", out)
	}
	if got := readNote(t, notePath); !strings.Contains(got, "pinned: true\n") {
		t.Fatalf("expected `pinned: true` to survive, got:\n%s", got)
	}
}

func TestPinDryRun_AppendGolden(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Alpha.md")
	original := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "pin", "--dry-run", notePath)
		if err != nil {
			t.Fatalf("pin: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "pin_dryrun_append.golden", out)
	if got := readNote(t, notePath); got != original {
		t.Fatalf("note changed under --dry-run")
	}
}

// TestPinDryRun_TwiceReportsSameRank proves neither run wrote: a first run that
// had written would push the second run's appended rank one step higher.
func TestPinDryRun_TwiceReportsSameRank(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Alpha.md")

	first, err := runCLI(t, "", "pin", "--dry-run", notePath)
	if err != nil {
		t.Fatalf("first pin: %v\n%s", err, first)
	}
	second, err := runCLI(t, "", "pin", "--dry-run", notePath)
	if err != nil {
		t.Fatalf("second pin: %v\n%s", err, second)
	}
	if first != second {
		t.Fatalf("dry-run output differs between runs:\n--- first ---\n%s--- second ---\n%s", first, second)
	}
	if !strings.Contains(first, "frontmatter[pinned]:  -> 3.0") {
		t.Fatalf("expected rank 3.0 in dry-run output, got:\n%s", first)
	}
}

func TestPinDryRun_AlreadyPinnedPrintsNothing(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Charlie.md")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "pin", "--dry-run", notePath)
		if err != nil {
			t.Fatalf("pin: %v\n%s", err, o)
		}
		out = o
	})
	assertGolden(t, "pin_dryrun_already_pinned.golden", out)
}

func TestUnpin_RemovesKeyEntirely(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Charlie.md")

	out, err := runCLI(t, "", "unpin", notePath)
	if err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "unpinned: Charlie.md") {
		t.Fatalf("expected `unpinned` report, got:\n%s", out)
	}

	got := readNote(t, notePath)
	want := "---\ntitle: Charlie\n---\nCharlie body.\n"
	if got != want {
		t.Fatalf("note =\n%q\nwant\n%q", got, want)
	}
}

// TestUnpin_AgreesWithHasAndMissing pins down the reason unpin removes the key
// instead of writing a tombstone: --missing pinned must find the unpinned note
// and --has pinned must not.
func TestUnpin_AgreesWithHasAndMissing(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Charlie.md")

	if out, err := runCLI(t, "", "unpin", notePath); err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}

	missing, err := runCLI(t, "", "find", "--missing", "pinned")
	if err != nil {
		t.Fatalf("find --missing: %v\n%s", err, missing)
	}
	if !strings.Contains(missing, notePath+"\n") {
		t.Fatalf("expected %s in --missing pinned output:\n%s", notePath, missing)
	}

	has, err := runCLI(t, "", "find", "--has", "pinned")
	if err != nil {
		t.Fatalf("find --has: %v\n%s", err, has)
	}
	if strings.Contains(has, notePath+"\n") {
		t.Fatalf("did not expect %s in --has pinned output:\n%s", notePath, has)
	}
}

func TestUnpin_NotPinnedIsNoOp(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Alpha.md")
	original := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unpin", notePath)
		if err != nil {
			t.Fatalf("unpin: %v\n%s", err, o)
		}
		out = o
	})
	if !strings.Contains(out, "not pinned: Alpha.md") {
		t.Fatalf("expected `not pinned` report, got:\n%s", out)
	}
	if got := readNote(t, notePath); got != original {
		t.Fatalf("note changed")
	}
}

func TestUnpinDryRun_ExistingGolden(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Charlie.md")
	original := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unpin", "--dry-run", notePath)
		if err != nil {
			t.Fatalf("unpin: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "unpin_dryrun_existing.golden", out)
	if got := readNote(t, notePath); got != original {
		t.Fatalf("note changed under --dry-run")
	}
}

func TestUnpinDryRun_NotPinnedPrintsNothing(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Alpha.md")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unpin", "--dry-run", notePath)
		if err != nil {
			t.Fatalf("unpin: %v\n%s", err, o)
		}
		out = o
	})
	assertGolden(t, "unpin_dryrun_not_pinned.golden", out)
}

// TestPin_IsUndoable proves the write goes through the same journaled noteops
// path as `bn set`.
func TestPin_IsUndoable(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Alpha.md")
	original := readNote(t, notePath)

	if out, err := runCLI(t, "", "pin", notePath); err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "", "undo"); err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}
	if got := readNote(t, notePath); got != original {
		t.Fatalf("undo did not revert the pin:\n--- want ---\n%s--- got ---\n%s", original, got)
	}
}

// TestUnpin_IsUndoable covers the removal side of the same journal contract.
func TestUnpin_IsUndoable(t *testing.T) {
	root := buildPinFixture(t)
	notePath := filepath.Join(root, "Charlie.md")
	original := readNote(t, notePath)

	if out, err := runCLI(t, "", "unpin", notePath); err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "", "undo"); err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}
	if got := readNote(t, notePath); got != original {
		t.Fatalf("undo did not revert the unpin:\n--- want ---\n%s--- got ---\n%s", original, got)
	}
}

// TestPin_RankExhaustionRenormalizes covers the plan shape a hand-written
// astronomic rank produces: no float64 sits above it, so PlanAppend rewrites the
// whole list to 1.0..N and the writes go through one transaction.
func TestPin_RankExhaustionRenormalizes(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Alpha.md":   "---\ntitle: Alpha\n---\nAlpha body.\n",
		"Delta.md":   "---\npinned: 1.0\ntitle: Delta\n---\nDelta body.\n",
		"Huge.md":    "---\npinned: 10000000000000000.0\ntitle: Huge\n---\nHuge body.\n",
		"Flagged.md": "---\npinned: true\ntitle: Flagged\n---\nFlagged body.\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	useVaultRoot(t, root, "")

	if out, err := runCLI(t, "", "pin", filepath.Join(root, "Alpha.md")); err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}

	want := map[string]string{
		"Delta.md":   "pinned: 1.0\n",
		"Huge.md":    "pinned: 2.0\n",
		"Flagged.md": "pinned: 3.0\n",
		"Alpha.md":   "pinned: 4.0\n",
	}
	for rel, fragment := range want {
		if got := readNote(t, filepath.Join(root, rel)); !strings.Contains(got, fragment) {
			t.Fatalf("%s: expected %q, got:\n%s", rel, fragment, got)
		}
	}
}

func TestFindPinned_EmitsPinnedOrder(t *testing.T) {
	root := buildPinFixture(t)

	out, err := runCLI(t, "", "find", "--pinned")
	if err != nil {
		t.Fatalf("find --pinned: %v\n%s", err, out)
	}

	want := []string{
		filepath.Join(root, "Delta.md"),
		filepath.Join(root, "Charlie.md"),
		filepath.Join(root, "Flagged.md"),
	}
	got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("find --pinned = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("find --pinned = %v, want %v", got, want)
		}
	}
}
