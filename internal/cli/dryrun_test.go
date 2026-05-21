package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assertMtimeUnchanged captures the mtime of every path before fn runs and
// fails the test if any of them differs afterward — used to verify that
// --dry-run never touches disk.
func assertMtimeUnchanged(t *testing.T, paths []string, fn func()) {
	t.Helper()
	before := make([]os.FileInfo, len(paths))
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		before[i] = fi
	}
	fn()
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !fi.ModTime().Equal(before[i].ModTime()) {
			t.Fatalf("mtime of %s changed: before=%v after=%v", p, before[i].ModTime(), fi.ModTime())
		}
	}
}

func assertGolden(t *testing.T, goldenName, got string) {
	t.Helper()
	path := filepath.Join("testdata", goldenName)
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if got != string(wantBytes) {
		t.Fatalf("output mismatch with %s:\n--- want ---\n%s--- got ---\n%s", path, string(wantBytes), got)
	}
}

func TestSetDryRun_NewKey(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	originalBody := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "status=reading")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "set_dryrun_newkey.golden", out)
	if got := readNote(t, notePath); got != originalBody {
		t.Fatalf("file body changed:\n--- before ---\n%s\n--- after ---\n%s", originalBody, got)
	}
}

func TestSetDryRun_ListAppend(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")
	originalBody := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "tags+=baz")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "set_dryrun_listappend.golden", out)
	if got := readNote(t, notePath); got != originalBody {
		t.Fatalf("file body changed")
	}
}

func TestSetDryRun_IdempotentAppendPrintsNothing(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "tags+=foo")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

func TestSetDryRun_MultiNoteStableOrder(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")
	stdin := bPath + "\n" + aPath + "\n"

	var out string
	assertMtimeUnchanged(t, []string{aPath, bPath}, func() {
		o, err := runCLI(t, stdin, "set", "--dry-run", "-", "status=done")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "set_dryrun_multinote.golden", out)
}

func TestUnsetDryRun_ExistingKey(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")
	originalBody := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unset", "--dry-run", notePath, "tags")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "unset_dryrun_existing.golden", out)
	if got := readNote(t, notePath); got != originalBody {
		t.Fatalf("file body changed")
	}
}

func TestUnsetDryRun_MissingKeyPrintsNothing(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unset", "--dry-run", notePath, "missing")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

// TestUnsetDryRun_DuplicatePropName asserts that passing the same property
// name twice on the command line produces exactly one `- key: value` line in
// the dry-run output. Before the fix, the second occurrence emitted a
// duplicate line that did not match what the non-dry path (idempotent) would
// have done.
func TestUnsetDryRun_DuplicatePropName(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "unset", "--dry-run", notePath, "tags", "tags")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if c := strings.Count(out, "- tags:"); c != 1 {
		t.Fatalf("expected exactly one `- tags:` line, got %d in:\n%s", c, out)
	}
}

// TestSetDryRun_WikilinkScalarShowsWrappedValue asserts that the dry-run
// preview for a wikilink-typed scalar shows the value the Marshal step will
// actually write (`[[Books]]`), not the raw user input (`Books`). Before the
// fix, `bn set --dry-run note.md parent=Books` printed `+ parent: Books`
// while the corresponding apply path would have written `parent: '[[Books]]'`.
func TestSetDryRun_WikilinkScalarShowsWrappedValue(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", "--dry-run", notePath, "parent=Books")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ parent: [[Books]]") {
		t.Fatalf("expected `+ parent: [[Books]]` (wrapped form) in dry-run output, got:\n%s", out)
	}
}

// TestSetDryRun_WikilinkListShowsWrappedValues asserts the same wikilink
// normalization for list-typed wikilink properties: each list element must
// appear wrapped in `[[ ]]` in the dry-run preview so it matches what the
// real save would produce.
func TestSetDryRun_WikilinkListShowsWrappedValues(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", "--dry-run", notePath, "categories=Books,Personal")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ categories: [[[Books]], [[Personal]]]") {
		t.Fatalf("expected wrapped categories in dry-run output, got:\n%s", out)
	}
}

// TestSetDryRun_WikilinkAlreadyStoredIsNoOp asserts that `bn set --dry-run`
// reports no change when the stored value is already the wrapped form of the
// user-supplied value (e.g. `parent: '[[Books]]'` on disk, `parent=Books` on
// the command line). The opSet branch was already comparing via
// wikilinkNormalized; this test pins that behavior down end-to-end so the
// dry-run path stays consistent with the apply path.
func TestSetDryRun_WikilinkAlreadyStoredIsNoOp(t *testing.T) {
	root := t.TempDir()
	notePath := filepath.Join(root, "wl.md")
	if err := os.WriteFile(notePath, []byte("---\nparent: '[[Books]]'\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "parent=Books")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})
	if out != "" {
		t.Fatalf("expected empty dry-run output for no-op wikilink set, got %q", out)
	}
}

// TestFormatDryRunValue_TimeZoneAtMidnight verifies that a time.Time at midnight
// in a non-UTC zone with offset 0 (e.g. time.FixedZone("", 0)) does NOT render
// as date-only. Only times explicitly in time.UTC should use the short form.
// Before the fix, formatDryRunValue checked the zone offset (== 0) rather than
// the location pointer (== time.UTC), so FixedZone("", 0) would incorrectly
// produce "2026-01-01" instead of the full RFC3339 representation.
func TestFormatDryRunValue_TimeZoneAtMidnight(t *testing.T) {
	// A time at midnight UTC should render as date-only.
	utcMidnight := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := formatDryRunValue(utcMidnight)
	if got != "2026-01-01" {
		t.Errorf("UTC midnight: got %q, want %q", got, "2026-01-01")
	}

	// A time at midnight in FixedZone("", 0) should render as RFC3339, not
	// as date-only. FixedZone has offset 0 but is not time.UTC.
	fakeUTC := time.FixedZone("", 0)
	fixedMidnight := time.Date(2026, 1, 1, 0, 0, 0, 0, fakeUTC)
	got = formatDryRunValue(fixedMidnight)
	if got == "2026-01-01" {
		t.Errorf("FixedZone(0) midnight should NOT render as date-only, got %q", got)
	}
	// It should be the full RFC3339 form.
	want := "2026-01-01T00:00:00Z"
	if got != want {
		t.Errorf("FixedZone(0) midnight: got %q, want %q", got, want)
	}

	// A time with a non-zero component should always be RFC3339.
	withTime := time.Date(2026, 1, 1, 14, 30, 0, 0, time.UTC)
	got = formatDryRunValue(withTime)
	want = "2026-01-01T14:30:00Z"
	if got != want {
		t.Errorf("with time: got %q, want %q", got, want)
	}
}
