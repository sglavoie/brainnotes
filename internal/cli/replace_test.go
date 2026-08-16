package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildReplaceFixture writes a small vault tailored to the `bn replace`
// scenarios: scalar status fields, tag lists with duplicates, wikilink-typed
// category lists, and notes in different folders for scoping tests.
func buildReplaceFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Areas/Alpha.md": "---\n" +
			"status: wip\n" +
			"tags:\n  - alpha\n  - shared\n" +
			"---\n" +
			"Alpha body.\n",
		"Areas/Beta.md": "---\n" +
			"status: wip\n" +
			"tags:\n  - beta\n  - shared\n" +
			"---\n" +
			"Beta body.\n",
		"Projects/Gamma.md": "---\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"status: done\n" +
			"tags:\n  - shared\n  - dup\n  - dup\n" +
			"---\n" +
			"Gamma body.\n",
		"Projects/Delta.md": "---\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"  - \"[[Personal]]\"\n" +
			"status: wip\n" +
			"---\n" +
			"Delta body.\n",
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

	useVaultRoot(t, root, testPropertyDefinitions)
	return root
}

func TestReplace_ScalarSingleFile(t *testing.T) {
	root := buildReplaceFixture(t)
	note := filepath.Join(root, "Areas/Alpha.md")

	out, err := runCLI(t, "", "replace", "status", "wip", "shipped", note)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated: Areas/Alpha.md") {
		t.Fatalf("expected updated line, got %q", out)
	}
	if strings.Contains(out, root) {
		t.Fatalf("expected vault-relative output, got %q", out)
	}
	if !strings.Contains(out, "replaced 1 file(s)") {
		t.Fatalf("expected replaced summary, got %q", out)
	}
	got := readNote(t, note)
	if !strings.Contains(got, "status: shipped") {
		t.Fatalf("expected status: shipped, got:\n%s", got)
	}
	// Other files untouched.
	beta := readNote(t, filepath.Join(root, "Areas/Beta.md"))
	if !strings.Contains(beta, "status: wip") {
		t.Fatalf("Beta should still have status: wip, got:\n%s", beta)
	}
}

func TestReplace_ListAcrossMultipleFiles(t *testing.T) {
	root := buildReplaceFixture(t)

	out, err := runCLI(t, "", "replace", "tags", "shared", "common")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, name := range []string{"Areas/Alpha.md", "Areas/Beta.md", "Projects/Gamma.md"} {
		got := readNote(t, filepath.Join(root, name))
		if strings.Contains(got, "- shared") {
			t.Fatalf("%s still references shared:\n%s", name, got)
		}
		if !strings.Contains(got, "- common") {
			t.Fatalf("%s missing common:\n%s", name, got)
		}
	}
	if !strings.Contains(out, "replaced 3 file(s)") {
		t.Fatalf("expected replaced 3, got %q", out)
	}
}

func TestReplace_ListDuplicateMatches(t *testing.T) {
	root := buildReplaceFixture(t)
	note := filepath.Join(root, "Projects/Gamma.md")

	if _, err := runCLI(t, "", "replace", "tags", "dup", "merged", note); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, note)
	count := strings.Count(got, "- merged")
	if count != 2 {
		t.Fatalf("expected 2 merged entries, got %d:\n%s", count, got)
	}
	if strings.Contains(got, "- dup") {
		t.Fatalf("expected dup gone, got:\n%s", got)
	}
}

func TestReplace_NoMatchPrintsSummaryOnly(t *testing.T) {
	root := buildReplaceFixture(t)
	_ = root

	out, err := runCLI(t, "", "replace", "status", "nope", "still-nope")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated:") {
		t.Fatalf("expected no updated lines, got %q", out)
	}
	if !strings.Contains(out, "replaced 0 file(s)") {
		t.Fatalf("expected zero summary, got %q", out)
	}
}

func TestReplace_ScopedByTagFilter(t *testing.T) {
	root := buildReplaceFixture(t)

	// Only Alpha has tag "alpha"; replacing status there should leave Beta alone.
	if _, err := runCLI(t, "", "replace", "status", "wip", "in-flight", "--tag", "alpha"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	alpha := readNote(t, filepath.Join(root, "Areas/Alpha.md"))
	if !strings.Contains(alpha, "status: in-flight") {
		t.Fatalf("Alpha not updated:\n%s", alpha)
	}
	beta := readNote(t, filepath.Join(root, "Areas/Beta.md"))
	if !strings.Contains(beta, "status: wip") {
		t.Fatalf("Beta should be untouched:\n%s", beta)
	}
}

func TestReplace_ScopedByGraphFilter(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Alpha.md": "---\nstatus: wip\n---\nLinks to [[Beta]].\n",
		"Beta.md":  "---\nstatus: wip\n---\nNo outgoing links.\n",
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	useVaultRoot(t, root, "")

	out, err := runCLI(t, "", "replace", "status", "wip", "done", "--min-links", "1")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "replaced 1 file(s)") {
		t.Fatalf("expected one replacement, got %q", out)
	}

	alpha := readNote(t, filepath.Join(root, "Alpha.md"))
	if !strings.Contains(alpha, "status: done") {
		t.Fatalf("Alpha should be updated:\n%s", alpha)
	}
	beta := readNote(t, filepath.Join(root, "Beta.md"))
	if !strings.Contains(beta, "status: wip") {
		t.Fatalf("Beta should be untouched:\n%s", beta)
	}
}

func TestReplace_ScopedByExplicitNoteArgs(t *testing.T) {
	root := buildReplaceFixture(t)
	alpha := filepath.Join(root, "Areas/Alpha.md")

	if _, err := runCLI(t, "", "replace", "status", "wip", "moved", alpha); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := readNote(t, alpha); !strings.Contains(got, "status: moved") {
		t.Fatalf("Alpha not updated:\n%s", got)
	}
	// Beta also has status: wip but was not in the args.
	beta := readNote(t, filepath.Join(root, "Areas/Beta.md"))
	if !strings.Contains(beta, "status: wip") {
		t.Fatalf("Beta should be untouched:\n%s", beta)
	}
}

func TestReplace_MutualExclusionError(t *testing.T) {
	root := buildReplaceFixture(t)
	alpha := filepath.Join(root, "Areas/Alpha.md")

	_, err := runCLI(t, "", "replace", "status", "wip", "x", alpha, "--tag", "alpha")
	if err == nil {
		t.Fatal("expected error combining notes and filter flags")
	}
	if !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplace_DryRunGolden(t *testing.T) {
	root := buildReplaceFixture(t)
	note := filepath.Join(root, "Areas/Alpha.md")
	originalBody := readNote(t, note)

	var out string
	assertMtimeUnchanged(t, []string{note}, func() {
		o, err := runCLI(t, "", "replace", "status", "wip", "shipped", note, "--dry-run")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})
	assertGolden(t, "replace_dryrun_scalar.golden", out)
	if got := readNote(t, note); got != originalBody {
		t.Fatalf("file body changed during dry-run:\n--- before ---\n%s--- after ---\n%s", originalBody, got)
	}
}

func TestReplace_WikilinkListAutoWrap(t *testing.T) {
	root := buildReplaceFixture(t)

	if _, err := runCLI(t, "", "replace", "categories", "Books", "Films"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	gamma := readNote(t, filepath.Join(root, "Projects/Gamma.md"))
	if !strings.Contains(gamma, "[[Films]]") {
		t.Fatalf("Gamma missing [[Films]]:\n%s", gamma)
	}
	if strings.Contains(gamma, "[[Books]]") {
		t.Fatalf("Gamma still has [[Books]]:\n%s", gamma)
	}
	delta := readNote(t, filepath.Join(root, "Projects/Delta.md"))
	if !strings.Contains(delta, "[[Films]]") || !strings.Contains(delta, "[[Personal]]") {
		t.Fatalf("Delta missing expected categories:\n%s", delta)
	}
	if strings.Contains(delta, "[[Books]]") {
		t.Fatalf("Delta still has [[Books]]:\n%s", delta)
	}
}

func TestReplace_RejectsInvalidKey(t *testing.T) {
	root := buildReplaceFixture(t)
	_ = root

	_, err := runCLI(t, "", "replace", "bad key", "a", "b")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestReplace_WikilinkSkipsAutoCoerce asserts that for wikilink-typed
// properties, a literal token that would otherwise auto-coerce (e.g. "true",
// "42", "2026-01-01") is treated as a string and wrapped in [[ ]] before
// comparison. Before the fix, coerceValue("true", "") returned a bool and the
// replacement never matched the string entry `[[true]]`.
func TestReplace_WikilinkSkipsAutoCoerce(t *testing.T) {
	root := t.TempDir()
	notePath := filepath.Join(root, "x.md")
	body := "---\ncategories:\n  - \"[[true]]\"\n---\nbody\n"
	if err := os.WriteFile(notePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	useVaultRoot(t, root, testPropertyDefinitions)

	if _, err := runCLI(t, "", "replace", "categories", "true", "false", notePath); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "[[false]]") {
		t.Fatalf("expected [[false]], got:\n%s", got)
	}
	if strings.Contains(got, "[[true]]") {
		t.Fatalf("expected [[true]] gone, got:\n%s", got)
	}
}
