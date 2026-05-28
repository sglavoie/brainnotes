package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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

func TestSearch_InteractiveRequiresPatternOrFlag(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)
	_, err := runSearch(t, root)
	if err == nil {
		t.Fatal("expected error when no args and no -i, got nil")
	}
	if !strings.Contains(err.Error(), "pattern required") {
		t.Fatalf("expected 'pattern required' in error, got: %v", err)
	}
}

func TestSearch_NoPatterRequired_WithInteractive(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_EXIT_CODE=1")
		return cmd
	}

	_, err := runSearch(t, root, "-i")
	if err != nil {
		t.Fatalf("expected no error with -i and no pattern, got: %v", err)
	}
}

func TestSearch_InteractiveNoPatternPreviewUsesLiveQuery(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	var capturedArgs []string
	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string(nil), args...)
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_EXIT_CODE=1")
		return cmd
	}

	_, err := runSearch(t, root, "-i")
	if err != nil {
		t.Fatalf("expected no error with -i and no pattern, got: %v", err)
	}

	preview, ok := fzfArgValue(capturedArgs, "--preview")
	if !ok {
		t.Fatalf("expected --preview in fzf args, got %v", capturedArgs)
	}
	if !strings.Contains(preview, "{q}") {
		t.Fatalf("live preview should pass fzf query placeholder as an argument, got %q", preview)
	}
	if strings.Contains(preview, `"{q}`) || strings.Contains(preview, "{q}|") {
		t.Fatalf("live preview must not quote {q} inside the grep pattern, got %q", preview)
	}
	if strings.Contains(preview, "{}") {
		t.Fatalf("live preview must not contain bare fzf current-item placeholder, got %q", preview)
	}
}

func TestBuildLiveQueryHighlightPreviewEscapesQuery(t *testing.T) {
	for _, bin := range []string{"sh", "sed", "grep"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("test{ing} here\ntesting here\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	preview := buildLiveQueryHighlightPreview()
	preview = strings.ReplaceAll(preview, "{q}", shellQuoteForTest("test{ing}"))
	preview = strings.ReplaceAll(preview, "{1}", shellQuoteForTest(path))

	out, err := exec.Command("sh", "-c", preview).CombinedOutput()
	if err != nil {
		t.Fatalf("preview command: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "\x1b[01;31m\x1b[Ktest{ing}") && !strings.Contains(got, "\x1b[01;31mtest{ing}") {
		t.Fatalf("expected literal query to be highlighted, got %q", got)
	}
	if strings.Contains(got, "\x1b[01;31m\x1b[Ktesting") || strings.Contains(got, "\x1b[01;31mtesting") {
		t.Fatalf("query should be escaped as a literal string, got %q", got)
	}
}

func fzfArgValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"="), true
		}
	}
	return "", false
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestBuildInteractiveLines_AllLines(t *testing.T) {
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nfirst body line\nsecond body line\n",
	})
	notePath := filepath.Join(root, "Notes", "Test.md")
	notes := []*vault.Note{{Path: notePath}}

	got, err := buildInteractiveLines(notes, root, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d: %v", len(got), got)
	}
	// "first body line" is raw file line 4 (3 frontmatter lines + 1).
	relPath := "Notes/Test.md"
	want0 := fmt.Sprintf("%s\t4\t%s:4:first body line", notePath, relPath)
	want1 := fmt.Sprintf("%s\t5\t%s:5:second body line", notePath, relPath)
	if got[0] != want0 {
		t.Fatalf("record[0]:\n got %q\nwant %q", got[0], want0)
	}
	if got[1] != want1 {
		t.Fatalf("record[1]:\n got %q\nwant %q", got[1], want1)
	}
}

func TestBuildInteractiveLines_Filtered(t *testing.T) {
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nno match here\ntarget line content\nalso no match\n",
	})
	notePath := filepath.Join(root, "Notes", "Test.md")
	notes := []*vault.Note{{Path: notePath}}

	matcher := func(s string) bool { return strings.Contains(s, "target") }
	got, err := buildInteractiveLines(notes, root, matcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d: %v", len(got), got)
	}
	// "target line content" is raw file line 5 (3 frontmatter + 1 non-matching + 1 match).
	if !strings.Contains(got[0], "\t5\t") {
		t.Fatalf("expected line 5 in record, got: %q", got[0])
	}
	if !strings.Contains(got[0], "target line content") {
		t.Fatalf("expected content in record, got: %q", got[0])
	}
}

func TestBuildInteractiveLines_TabInContent(t *testing.T) {
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nbefore\tafter\n",
	})
	notePath := filepath.Join(root, "Notes", "Test.md")
	notes := []*vault.Note{{Path: notePath}}

	got, err := buildInteractiveLines(notes, root, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	// SplitN with limit 3 must yield absPath, lineNum, displayString.
	parts := strings.SplitN(got[0], "\t", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 tab-separated fields, got %d: %q", len(parts), got[0])
	}
	if parts[0] != notePath {
		t.Fatalf("field[0]: expected %q, got %q", notePath, parts[0])
	}
	if parts[1] != "4" {
		t.Fatalf("field[1]: expected line 4, got %q", parts[1])
	}
}

func TestBuildInteractiveLines_EmptyBody(t *testing.T) {
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\n",
	})
	notePath := filepath.Join(root, "Notes", "Test.md")
	notes := []*vault.Note{{Path: notePath}}

	got, err := buildInteractiveLines(notes, root, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no records for empty body, got %d: %v", len(got), got)
	}
}

func TestBuildInteractiveLines_NoFrontmatter(t *testing.T) {
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "first line\nsecond line\n",
	})
	notePath := filepath.Join(root, "Notes", "Test.md")
	notes := []*vault.Note{{Path: notePath}}

	got, err := buildInteractiveLines(notes, root, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d: %v", len(got), got)
	}
	// bodyStart=0, so first line is raw file line 1.
	if !strings.Contains(got[0], "\t1\t") {
		t.Fatalf("expected line 1 for first record, got: %q", got[0])
	}
	if !strings.Contains(got[0], "first line") {
		t.Fatalf("expected 'first line' in first record, got: %q", got[0])
	}
}

func TestSearch_InteractiveNullSep(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	betaPath := filepath.Join(root, "Notes", "Beta.md")
	fzfOut := fmt.Sprintf("%s\t4\tNotes/Alpha.md:4:the keyword is here\n%s\t4\tNotes/Beta.md:4:another keyword usage\n", alphaPath, betaPath)

	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_OUTPUT="+fzfOut)
		return cmd
	}

	stdout, err := runSearch(t, root, "-i", "-0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "\x00") {
		t.Fatalf("expected NUL separator in interactive -0 output, got: %q", stdout)
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

func TestSearch_InteractiveCountError(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)
	_, err := runSearch(t, root, "--count", "-i")
	if err == nil {
		t.Fatal("expected error for --count -i, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected 'cannot be combined' in error, got: %v", err)
	}
}

func TestSearch_InteractiveContextError(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)
	_, err := runSearch(t, root, "--context", "2", "-i")
	if err == nil {
		t.Fatal("expected error for --context 2 -i, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected 'cannot be combined' in error, got: %v", err)
	}
}
