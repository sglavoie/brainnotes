package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRename_PreservesSourceExtension verifies that bn rename preserves the
// source file's extension rather than substituting the config's
// DefaultExtension. Before the fix, a .markdown note renamed by bn rename
// would silently become a .md file (changing the on-disk extension and
// breaking links built against the original suffix). Move already preserves
// the source extension via filepath.Ext(target.Path); rename should match.
func TestRename_PreservesSourceExtension(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "Alpha.markdown")
	if err := os.WriteFile(src, []byte("---\ntitle: Alpha\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	if _, err := runCLI(t, "", "rename", src, "Beta"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	wantNew := filepath.Join(root, "Beta.markdown")
	if _, err := os.Stat(wantNew); err != nil {
		t.Fatalf("expected %s to exist: %v", wantNew, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected old path %s gone, stat err=%v", src, err)
	}
	// And we should NOT have created a .md sibling.
	if _, err := os.Stat(filepath.Join(root, "Beta.md")); !os.IsNotExist(err) {
		t.Fatalf("rename should not have produced Beta.md; got stat err=%v", err)
	}
}

// TestSet_LeadingZeroIntStaysString verifies that strings with a leading zero
// auto-coerce as strings, preserving the leading zero. Before the fix,
// `bn set version=01` stored the integer 1, silently dropping the leading
// zero. Negative numbers like `-01` are likewise kept as strings.
func TestSet_LeadingZeroIntStaysString(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "version=01"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := readNote(t, notePath)
	// yaml.v3 surrounds purely-numeric-looking strings with quotes to
	// disambiguate from ints; we just need to make sure the literal "01"
	// is on disk (not "1") and the value is not a bare YAML int.
	if !strings.Contains(got, "version: \"01\"") && !strings.Contains(got, "version: '01'") {
		t.Fatalf("expected version: \"01\" (string), got:\n%s", got)
	}
	if strings.Contains(got, "version: 01\n") || strings.Contains(got, "version: 1\n") {
		t.Fatalf("expected leading zero preserved as string, got:\n%s", got)
	}
}

// TestSet_NormalIntStillCoerces verifies that the leading-zero fix did not
// regress regular integer coercion: "7", "0", "-3" still become ints.
func TestSet_NormalIntStillCoerces(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "rating=7", "count=0", "delta=-3"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := readNote(t, notePath)
	for _, sub := range []string{"rating: 7", "count: 0", "delta: -3"} {
		if !strings.Contains(got, sub) {
			t.Errorf("expected %q in:\n%s", sub, got)
		}
	}
}

// TestSet_ForceIntStripsLeadingZero documents that --type int still parses
// leading-zero strings as ints (the user explicitly opted in).
func TestSet_ForceIntStripsLeadingZero(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", "--type", "int", notePath, "version=01"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "version: 1\n") {
		t.Fatalf("expected version: 1 with --type int, got:\n%s", got)
	}
}

// TestUnset_StdinSentinelAsNoteNeedsProperty verifies that 'bn unset -' (with
// no '--' and no other args) is parsed as stdin note input and fails clearly
// because no property names were supplied.
func TestUnset_StdinSentinelAsNoteNeedsProperty(t *testing.T) {
	root := buildSetFixture(t)
	_ = root

	out, err := runCLI(t, "", "unset", "-")
	if err == nil {
		t.Fatalf("expected error for 'bn unset -', got nil; output=%q", out)
	}
	if !strings.Contains(err.Error(), "at least one property name is required") {
		t.Fatalf("expected missing-property error, got: %v", err)
	}
}

// TestUnset_StdinSentinelAfterDashAllowed verifies the documented invocation
// 'bn unset - -- <prop>' continues to work: '-' before '--' triggers stdin,
// the property follows after '--', and the sentinel check does not fire.
func TestUnset_StdinSentinelAfterDashAllowed(t *testing.T) {
	root := buildSetFixture(t)
	tagsPath := filepath.Join(root, "tags.md")

	stdin := tagsPath + "\n"
	if _, err := runCLI(t, stdin, "unset", "-", "--", "tags"); err != nil {
		t.Fatalf("unset - -- tags: %v", err)
	}
	got := readNote(t, tagsPath)
	if strings.Contains(got, "tags:") {
		t.Fatalf("expected tags removed, got:\n%s", got)
	}
}
