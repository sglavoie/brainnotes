package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearch_OpenOpensFirstMatchAtRawLine(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/A.md": "---\ntitle: A\n---\nintro\ntarget line\n",
		"Notes/B.md": "---\ntitle: B\n---\ntarget elsewhere\n",
	})

	origOpenExec := searchOpenExec
	t.Cleanup(func() { searchOpenExec = origOpenExec })

	var gotEditor, gotFile string
	var gotLine int
	searchOpenExec = func(editor, file string, line int) error {
		gotEditor = editor
		gotFile = file
		gotLine = line
		return nil
	}
	t.Setenv("EDITOR", "nvim")

	stdout, err := runSearch(t, root, "--open", "target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout with --open, got %q", stdout)
	}
	if gotEditor != "nvim" {
		t.Fatalf("editor: got %q, want nvim", gotEditor)
	}
	wantFile := filepath.Join(root, "Notes", "A.md")
	if gotFile != wantFile {
		t.Fatalf("file: got %q, want %q", gotFile, wantFile)
	}
	if gotLine != 5 {
		t.Fatalf("line: got %d, want 5", gotLine)
	}
}

func TestSearch_OpenInteractiveOpensSelectedLine(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)
	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	fzfOut := fmt.Sprintf("%s\t4\tNotes/Alpha.md\tthe keyword is here\n", alphaPath)

	origLookPath := fzfLookPath
	origCommand := fzfCommand
	origOpenExec := searchOpenExec
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
		searchOpenExec = origOpenExec
	})

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_OUTPUT="+fzfOut)
		return cmd
	}

	var gotFile string
	var gotLine int
	searchOpenExec = func(editor, file string, line int) error {
		gotFile = file
		gotLine = line
		return nil
	}
	t.Setenv("EDITOR", "nvim")

	stdout, err := runSearch(t, root, "--open", "-i", "keyword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout with --open, got %q", stdout)
	}
	if gotFile != alphaPath {
		t.Fatalf("file: got %q, want %q", gotFile, alphaPath)
	}
	if gotLine != 4 {
		t.Fatalf("line: got %d, want 4", gotLine)
	}
}

func TestSearch_OpenRequiresEditor(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)
	t.Setenv("EDITOR", "")

	_, err := runSearch(t, root, "--open", "keyword")
	if err == nil {
		t.Fatal("expected error when neither config editor nor $EDITOR is set")
	}
	if !strings.Contains(err.Error(), "[open].editor") || !strings.Contains(err.Error(), "$EDITOR") {
		t.Fatalf("error should mention both [open].editor and $EDITOR; got %v", err)
	}
}

func TestSearch_OpenCannotBeCombinedWithCount(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	_, err := runSearch(t, root, "--open", "--count", "keyword")
	if err == nil {
		t.Fatal("expected --open/--count validation error, got nil")
	}
	if !strings.Contains(err.Error(), "--count cannot be combined with --open") {
		t.Fatalf("expected --count/--open error, got %v", err)
	}
}
