package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func buildSearchVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Notes/Alpha.md": "---\ntitle: Alpha\n---\nthe keyword is here\n",
		"Notes/Beta.md":  "---\ntitle: Beta\n---\nanother keyword usage\n",
		"Notes/Gamma.md": "---\ntitle: Gamma\n---\nnothing relevant here\n",
		"Notes/Upper.md": "---\ntitle: Upper\n---\nUPPERCASE CONTENT only\n",
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
	return root
}

func runSearch(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)
	cmd.SetArgs(append([]string{"--vault", root, "search"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// searchRels converts absolute-path stdout lines to sorted vault-relative slash paths.
func searchRels(t *testing.T, root, stdout string) []string {
	t.Helper()
	var rels []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

func TestSearch_BasicMatch(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "relevant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Gamma.md"}
	if !equalStrings(got, want) {
		t.Fatalf("search relevant:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_CaseFold(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "uppercase")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Upper.md"}
	if !equalStrings(got, want) {
		t.Fatalf("case-insensitive search for 'uppercase':\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_CaseSensitive(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "--case-sensitive", "uppercase")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	if len(got) != 0 {
		t.Fatalf("case-sensitive search for lowercase 'uppercase' should match nothing, got=%v", got)
	}
}

func TestSearch_NoMatches(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "xyzzy_no_such_word")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty output, got: %q", stdout)
	}
}

func TestSearch_Regex(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "--regex", "key(word|phrase)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Alpha.md", "Notes/Beta.md"}
	if !equalStrings(got, want) {
		t.Fatalf("regex search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_InvalidRegex(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	_, err := runSearch(t, root, "--regex", "[invalid")
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "[invalid") {
		t.Fatalf("error should contain pattern, got: %v", err)
	}
}

func TestSearch_EmptyPattern(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	_, err := runSearch(t, root, "   ")
	if err == nil {
		t.Fatal("expected error for empty pattern, got nil")
	}
}

func TestSearch_NullSep(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "-0", "keyword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "\x00") {
		t.Fatalf("expected NUL separator in output, got: %q", stdout)
	}
	parts := strings.Split(stdout, "\x00")
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("expected 2 NUL-separated paths, got %d: %v", len(nonEmpty), nonEmpty)
	}
}

func TestSearch_MultipleMatchingNotes(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "keyword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := readGolden(t, "search-basic.golden")
	if !equalStrings(got, want) {
		t.Fatalf("search keyword:\n got=%v\nwant=%v", got, want)
	}
}

func runSearchStdin(t *testing.T, root string, stdin io.Reader, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(stdin)
	cmd.SetArgs(append([]string{"--vault", root, "search"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func buildTaggedVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Notes/Physics.md":   "---\ntitle: Physics\ntags:\n  - physics\n---\nactivation energy is key\n",
		"Notes/Chemistry.md": "---\ntitle: Chemistry\ntags:\n  - chemistry\n---\nactivation energy transfer here\n",
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
	return root
}

func buildFolderVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"References/Paper1.md": "---\ntitle: Paper1\n---\nfull-text unique reference marker\n",
		"Notes/Misc.md":        "---\ntitle: Misc\n---\nfull-text misc content here\n",
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
	return root
}

func buildOrphanVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Notes/Linker.md": "---\ntitle: Linker\n---\nSee [[Linked]] for details.\n",
		"Notes/Linked.md": "---\ntitle: Linked\n---\norphan-test-term appears here too\n",
		"Notes/Orphan.md": "---\ntitle: Orphan\n---\norphan-test-term appears here\n",
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
	return root
}

func TestSearch_TagScope(t *testing.T) {
	isolateBnEnv(t)
	root := buildTaggedVault(t)

	stdout, err := runSearch(t, root, "--tag", "physics", "activation energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Physics.md"}
	if !equalStrings(got, want) {
		t.Fatalf("tag-scoped search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_FolderScope(t *testing.T) {
	isolateBnEnv(t)
	root := buildFolderVault(t)

	stdout, err := runSearch(t, root, "--folder", "References", "full-text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"References/Paper1.md"}
	if !equalStrings(got, want) {
		t.Fatalf("folder-scoped search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_PositionalArgs(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	stdout, err := runSearch(t, root, "keyword", alphaPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("positional-arg search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_StdinPipe(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	gammaPath := filepath.Join(root, "Notes", "Gamma.md")
	stdin := strings.NewReader(alphaPath + "\n" + gammaPath + "\n")

	stdout, err := runSearchStdin(t, root, stdin, "keyword", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("stdin-pipe search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_NoteArgsAndFindFlags_Error(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	_, err := runSearch(t, root, "--tag", "physics", "keyword", alphaPath)
	if err == nil {
		t.Fatal("expected error when combining note args and find flags, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error should mention mutually exclusive, got: %v", err)
	}
}

func TestSearch_OrphanScope(t *testing.T) {
	isolateBnEnv(t)
	root := buildOrphanVault(t)

	stdout, err := runSearch(t, root, "--orphan", "orphan-test-term")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Orphan.md"}
	if !equalStrings(got, want) {
		t.Fatalf("orphan-scoped search:\n got=%v\nwant=%v", got, want)
	}
}

// buildVaultFromFiles creates a temp vault with the given relative-path → content map.
func buildVaultFromFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

// contextLines splits stdout by newline, dropping only empty lines (preserving "--" separators).
func contextLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func TestSearch_NullStdin(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	betaPath := filepath.Join(root, "Notes", "Beta.md")
	stdin := strings.NewReader(alphaPath + "\x00" + betaPath + "\x00")

	stdout, err := runSearchStdin(t, root, stdin, "-0", "keyword", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := strings.Split(stdout, "\x00")
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("expected 2 NUL-separated paths, got %d: %v", len(nonEmpty), nonEmpty)
	}
	sort.Strings(nonEmpty)
	got := make([]string, len(nonEmpty))
	for i, p := range nonEmpty {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		got[i] = filepath.ToSlash(rel)
	}
	want := []string{"Notes/Alpha.md", "Notes/Beta.md"}
	if !equalStrings(got, want) {
		t.Fatalf("null-stdin search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_Context_Basic(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Physics.md": "---\ntitle: Physics\n---\ncontext line above\nactivation energy in physics\ncontext line below\n",
	})
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := contextLines(stdout)
	want := readGoldenLines(t, "search-context-basic.golden")
	if !equalStrings(got, want) {
		t.Fatalf("context basic:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_Context_Overlap(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nenergy line one\nsome middle line\nenergy line two\n",
	})
	// Two matches 2 lines apart with N=2: windows should merge into one block — no "--".
	stdout, err := runSearch(t, root, "--context", "2", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout, "--") {
		t.Fatalf("overlapping windows should merge into one block, got separator:\n%s", stdout)
	}
	lines := contextLines(stdout)
	if len(lines) == 0 {
		t.Fatal("expected output lines, got none")
	}
}

func TestSearch_Context_NoOverlap(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nenergy line one\nfar apart A\nfar apart B\nfar apart C\nenergy line two\n",
	})
	// Two matches far apart with N=1: windows should not overlap — "--" appears once.
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	var sepCount int
	for _, l := range lines {
		if l == "--" {
			sepCount++
		}
	}
	if sepCount != 1 {
		t.Fatalf("expected exactly 1 '--' separator for non-overlapping windows, got %d:\n%s", sepCount, stdout)
	}
}

func TestSearch_Context_MultiNote(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/A.md": "---\ntitle: A\n---\ncontext before\nenergy in A\ncontext after\n",
		"Notes/B.md": "---\ntitle: B\n---\ncontext before\nenergy in B\ncontext after\n",
	})
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := contextLines(stdout)
	want := readGoldenLines(t, "search-context-multinote.golden")
	if !equalStrings(got, want) {
		t.Fatalf("context multi-note:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_Context_FirstLine(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nenergy is the first body line\nsome context after\n",
	})
	// Match on first body line with N=2: no frontmatter lines should appear.
	stdout, err := runSearch(t, root, "--context", "2", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	for _, l := range lines {
		if strings.Contains(l, "title:") || strings.Contains(l, "---") {
			t.Fatalf("frontmatter line leaked into context output: %q", l)
		}
	}
	if len(lines) == 0 {
		t.Fatal("expected output lines, got none")
	}
}

func TestSearch_Context_LastLine(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nsome context before\nenergy is the last line\n",
	})
	// Match on last body line with N=2: output should not go past file end.
	stdout, err := runSearch(t, root, "--context", "2", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	// Exactly 2 lines: context line before + match line.
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d:\n%s", len(lines), stdout)
	}
	if !strings.Contains(lines[1], ":") || !strings.Contains(lines[1], "energy is the last line") {
		t.Fatalf("expected match line as last output line, got: %q", lines[1])
	}
}

func TestSearch_Context_NoFrontmatter(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "energy first\ncontext after\n",
	})
	// Note without frontmatter: bodyStartIdx=0, match at line 1.
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d:\n%s", len(lines), stdout)
	}
	// First line should be the match at line 1 (1-based).
	if !strings.Contains(lines[0], ":1:") {
		t.Fatalf("expected match at line 1, got: %q", lines[0])
	}
}
