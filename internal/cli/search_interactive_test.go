package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

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
	nth, ok := fzfArgValue(capturedArgs, "--nth")
	if !ok {
		t.Fatalf("expected --nth in fzf args, got %v", capturedArgs)
	}
	if nth != "2.." {
		t.Fatalf("interactive search should match only the body field, got --nth %q", nth)
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
	want0 := fmt.Sprintf("%s\t4\t%s\tfirst body line", notePath, relPath)
	want1 := fmt.Sprintf("%s\t5\t%s\tsecond body line", notePath, relPath)
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
	// SplitN with limit 4 must yield absPath, lineNum, display path, and searchable body content.
	parts := strings.SplitN(got[0], "\t", 4)
	if len(parts) != 4 {
		t.Fatalf("expected 4 tab-separated fields, got %d: %q", len(parts), got[0])
	}
	if parts[0] != notePath {
		t.Fatalf("field[0]: expected %q, got %q", notePath, parts[0])
	}
	if parts[1] != "4" {
		t.Fatalf("field[1]: expected line 4, got %q", parts[1])
	}
	if parts[2] != "Notes/Test.md" {
		t.Fatalf("field[2]: expected display path, got %q", parts[2])
	}
	if parts[3] != "before\tafter" {
		t.Fatalf("searchable body field: expected %q, got %q", "before\tafter", parts[3])
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
