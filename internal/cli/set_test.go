package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildSetFixture writes a small vault and a config that the `set`/`unset`
// commands can pick up via BNOTES_CONFIG.
func buildSetFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"note.md":  "---\ntitle: Existing\n---\nBody.\n",
		"a.md":     "---\ntitle: A\n---\nA body.\n",
		"b.md":     "---\ntitle: B\n---\nB body.\n",
		"empty.md": "Body only.\n",
		"tags.md":  "---\ntags:\n  - foo\n  - bar\ntitle: Tags\n---\nBody.\n",
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

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
	return root
}

// runCLI executes the root command with args and returns (stdout, err). It
// optionally writes stdin from the supplied string.
func runCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	err := cmd.Execute()
	return out.String(), err
}

func readNote(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestSetSimpleScalar(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "status=reading")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "status: reading") {
		t.Fatalf("expected status: reading, got:\n%s", got)
	}
}

func TestSetCategoriesWikilinkList(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "categories=Books,Personal")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "- '[[Books]]'") && !strings.Contains(got, "- \"[[Books]]\"") && !strings.Contains(got, "- [[Books]]") {
		t.Fatalf("expected wikilink-wrapped Books, got:\n%s", got)
	}
	if !strings.Contains(got, "Personal") {
		t.Fatalf("expected Personal entry, got:\n%s", got)
	}
}

func TestSetForcedDate(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "created=2026-01-01", "--type=date")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "created: 2026-01-01") {
		t.Fatalf("expected created: 2026-01-01, got:\n%s", got)
	}
}

func TestSetAutoDateAndInt(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "created=2026-01-01", "rating=7"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "created: 2026-01-01") {
		t.Fatalf("expected auto-date, got:\n%s", got)
	}
	if !strings.Contains(got, "rating: 7") {
		t.Fatalf("expected auto-int, got:\n%s", got)
	}
}

func TestSetSortedKeyOutput(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "empty.md")

	if _, err := runCLI(t, "", "set", notePath, "zeta=1", "alpha=2", "mu=3"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	idxA := strings.Index(got, "alpha:")
	idxM := strings.Index(got, "mu:")
	idxZ := strings.Index(got, "zeta:")
	if idxA < 0 || idxM < 0 || idxZ < 0 {
		t.Fatalf("missing keys, got:\n%s", got)
	}
	if !(idxA < idxM && idxM < idxZ) {
		t.Fatalf("keys not alphabetical: alpha=%d mu=%d zeta=%d\n%s", idxA, idxM, idxZ, got)
	}
}

func TestSetMultipleNotesViaStdin(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")
	stdin := aPath + "\n" + bPath + "\n"

	out, err := runCLI(t, stdin, "set", "-", "status=done")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, aPath+": updated") || !strings.Contains(out, bPath+": updated") {
		t.Fatalf("expected both paths updated, got %q", out)
	}
	for _, p := range []string{aPath, bPath} {
		if got := readNote(t, p); !strings.Contains(got, "status: done") {
			t.Fatalf("%s missing status: done\n%s", p, got)
		}
	}
}

func TestSetForcedRawSkipsCoercion(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "flag=true", "--type=raw"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "flag: true\n") {
		t.Fatalf("expected raw string, got bool:\n%s", got)
	}
	// YAML should quote or otherwise mark a string `true`.
	if !strings.Contains(got, "flag: \"true\"") && !strings.Contains(got, "flag: 'true'") {
		t.Fatalf("expected quoted string flag, got:\n%s", got)
	}
}

func TestSetRejectsOutOfVault(t *testing.T) {
	root := buildSetFixture(t)
	outside := filepath.Join(t.TempDir(), "outsider.md")
	if err := os.WriteFile(outside, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = root

	_, err := runCLI(t, "", "set", outside, "status=x")
	if err == nil {
		t.Fatal("expected error for out-of-vault path")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected out-of-vault error, got %v", err)
	}
}

func TestUnsetRemovesProperty(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "unset", notePath, "tags")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "tags:") {
		t.Fatalf("tags should be gone, got:\n%s", got)
	}
}

func TestUnsetIdempotent(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	if _, err := runCLI(t, "", "unset", notePath, "tags"); err != nil {
		t.Fatalf("first unset: %v", err)
	}
	out, err := runCLI(t, "", "unset", notePath, "tags")
	if err != nil {
		t.Fatalf("second unset: %v\n%s", err, out)
	}
	if out != "" {
		t.Fatalf("second unset should be silent, got %q", out)
	}
}

func TestUnsetMultipleNotesViaDash(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")

	out, err := runCLI(t, "", "unset", aPath, bPath, "--", "title")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, aPath+": updated") || !strings.Contains(out, bPath+": updated") {
		t.Fatalf("expected both updated, got %q", out)
	}
	for _, p := range []string{aPath, bPath} {
		if got := readNote(t, p); strings.Contains(got, "title:") {
			t.Fatalf("%s still has title:\n%s", p, got)
		}
	}
}

func TestSetSplitArgs(t *testing.T) {
	notes, pairs, err := splitSetArgs([]string{"a.md", "b.md", "k1=v1", "k2=v2"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(notes) != 2 || notes[0] != "a.md" || notes[1] != "b.md" {
		t.Fatalf("notes mismatch: %v", notes)
	}
	if len(pairs) != 2 || pairs[0] != "k1=v1" || pairs[1] != "k2=v2" {
		t.Fatalf("pairs mismatch: %v", pairs)
	}

	if _, _, err := splitSetArgs([]string{"a.md", "k=v", "b.md"}); err == nil {
		t.Fatal("expected error for note after pair")
	}
}

func TestCoerceValueAuto(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"FALSE", false},
		{"42", 42},
		{"-3", -3},          // negative integers should coerce to int
		{"-0", 0},           // canonical zero, still int-typed
		{"2026-01-01", nil}, // sentinel: time.Time checked below
		{"hello", "hello"},
		{"-hello", "-hello"}, // not numeric → string, no false positive
	}
	for _, c := range cases {
		got, err := coerceValue(c.in, "")
		if err != nil {
			t.Fatalf("coerceValue(%q) err=%v", c.in, err)
		}
		if c.in == "2026-01-01" {
			if _, ok := got.(interface{ Year() int }); !ok {
				t.Fatalf("expected time.Time for date, got %T", got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("coerceValue(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

// TestSetNegativeInt verifies that an integer literal with a leading `-` is
// stored as an int, not a string. Before the fix, intValueRe rejected the
// minus sign so `rating=-3` was written as `rating: "-3"`.
func TestSetNegativeInt(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "rating=-3"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "rating: -3\n") {
		t.Fatalf("expected `rating: -3` (int), got:\n%s", got)
	}
	if strings.Contains(got, `rating: "-3"`) || strings.Contains(got, "rating: '-3'") {
		t.Fatalf("negative int should not be quoted as a string, got:\n%s", got)
	}
}
