package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildOpenVault writes a tiny vault and returns its root. Reused across
// `open` tests.
func buildOpenVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Apple.md":           "---\ntitle: Apple\n---\nA.\n",
		"References/Book.md": "---\ntitle: Book\n---\nB.\n",
		"People/Charlie.md":  "---\ntitle: Charlie\n---\nC.\n",
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
	markVaultRoot(t, root, "")
	return root
}

// isolateBnEnv clears the vault environment so a vault on the developer's
// machine cannot leak into a test.
func isolateBnEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
}

// runOpen executes the root command with the given args. It returns stdout
// and stderr separately.
func runOpen(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errBuf)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestOpen_PrintReturnsSelection(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	want := filepath.Join(vaultRoot, "Apple.md")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{want}, nil
	}
	editorCalled := false
	openExec = func(editor, file string) error {
		editorCalled = true
		return nil
	}

	stdout, _, err := runOpen(t, vaultRoot, "open", "--print")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if editorCalled {
		t.Fatalf("editor seam should not be called with --print")
	}
	got := strings.TrimRight(stdout, "\n")
	if got != want {
		t.Fatalf("stdout: got %q, want %q", got, want)
	}
}

func TestOpen_QueryForwardedToPicker(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	var capturedArgs []string
	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		capturedArgs = append([]string(nil), extraArgs...)
		return []string{filepath.Join(vaultRoot, "Apple.md")}, nil
	}
	openExec = func(editor, file string) error { return nil }
	t.Setenv("EDITOR", "true")

	if _, _, err := runOpen(t, vaultRoot, "open", "foo"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var sawQuery bool
	for i := 0; i+1 < len(capturedArgs); i++ {
		if capturedArgs[i] == "--query" && capturedArgs[i+1] == "foo" {
			sawQuery = true
			break
		}
	}
	if !sawQuery {
		t.Fatalf("expected --query foo in picker args; got %v", capturedArgs)
	}
}

func TestOpen_PickerAbort(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}
	editorCalled := false
	openExec = func(editor, file string) error {
		editorCalled = true
		return nil
	}
	t.Setenv("EDITOR", "true")

	stdout, stderr, err := runOpen(t, vaultRoot, "open")
	if !errors.Is(err, ErrNothingSelected) {
		t.Fatalf("err = %v, want ErrNothingSelected", err)
	}
	if editorCalled {
		t.Fatalf("editor should not run on abort")
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output on abort; stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestOpen_EditorSeamCalled(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	want := filepath.Join(vaultRoot, "References", "Book.md")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{want}, nil
	}
	var gotEditor, gotFile string
	openExec = func(editor, file string) error {
		gotEditor = editor
		gotFile = file
		return nil
	}
	t.Setenv("EDITOR", "nvim")

	if _, _, err := runOpen(t, vaultRoot, "open"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotEditor != "nvim" {
		t.Fatalf("editor: got %q, want %q", gotEditor, "nvim")
	}
	if gotFile != want {
		t.Fatalf("file: got %q, want %q", gotFile, want)
	}
}

func TestOpen_EmptyVault(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := t.TempDir()
	markVaultRoot(t, vaultRoot, "")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	pickerCalled := false
	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		pickerCalled = true
		return nil, nil
	}
	openExec = func(editor, file string) error { return nil }

	stdout, stderr, err := runOpen(t, vaultRoot, "open")
	if !errors.Is(err, ErrNothingSelected) {
		t.Fatalf("err = %v, want ErrNothingSelected", err)
	}
	if pickerCalled {
		t.Fatalf("picker should not be called on empty vault")
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if stderr != "" || !strings.Contains(err.Error(), "no notes in vault") {
		t.Fatalf("the error, not stderr, should say 'no notes in vault'; err=%v stderr=%q", err, stderr)
	}
}

func TestOpen_MissingEditor(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(vaultRoot, "Apple.md")}, nil
	}
	openExec = func(editor, file string) error { return errors.New("seam should not be called") }
	t.Setenv("EDITOR", "")

	_, _, err := runOpen(t, vaultRoot, "open")
	if err == nil {
		t.Fatalf("expected error when neither config editor nor $EDITOR is set")
	}
	if !strings.Contains(err.Error(), "[open].editor") || !strings.Contains(err.Error(), "$EDITOR") {
		t.Fatalf("error should mention both [open].editor and $EDITOR; got %v", err)
	}
}

func TestOpen_ConfigEditorTakesPriority(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	want := filepath.Join(vaultRoot, "Apple.md")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{want}, nil
	}
	var gotEditor string
	openExec = func(editor, file string) error {
		gotEditor = editor
		return nil
	}
	t.Setenv("EDITOR", "vim") // env editor should be ignored

	markVaultRoot(t, vaultRoot, "[open]\neditor = \"nvim\"\n")

	if _, _, err := runOpen(t, vaultRoot, "open"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotEditor != "nvim" {
		t.Fatalf("editor: got %q, want %q (config should take priority over $EDITOR)", gotEditor, "nvim")
	}
}

// TestOpen_NullWithoutPrintIsRejected verifies that `bn open --null` without
// `--print` produces an error rather than silently dropping the flag. The
// editor handoff is meaningless to NUL-terminate; pairing the flags is almost
// certainly a mistake.
func TestOpen_NullWithoutPrintIsRejected(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	pickerCalled := false
	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		pickerCalled = true
		return []string{filepath.Join(vaultRoot, "Apple.md")}, nil
	}
	openExec = func(editor, file string) error { return nil }

	_, _, err := runOpen(t, vaultRoot, "open", "--null")
	if err == nil {
		t.Fatalf("expected error for --null without --print")
	}
	if !strings.Contains(err.Error(), "--null requires --print") {
		t.Fatalf("expected error to mention '--null requires --print'; got %v", err)
	}
	if pickerCalled {
		t.Fatalf("picker should not run when flag validation fails")
	}
}

// TestOpen_PrintNullSeparator verifies that --print --null emits the selected
// path terminated with a NUL byte instead of a newline. This composes with
// `xargs -0` and other NUL-aware tools.
func TestOpen_PrintNullSeparator(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	want := filepath.Join(vaultRoot, "Apple.md")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{want}, nil
	}
	openExec = func(editor, file string) error {
		t.Fatalf("editor should not run with --print")
		return nil
	}

	stdout, _, err := runOpen(t, vaultRoot, "open", "--print", "--null")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout != want+"\x00" {
		t.Fatalf("stdout: got %q, want %q", stdout, want+"\x00")
	}
}

func TestOpen_EnvEditorUsedWhenConfigEmpty(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	want := filepath.Join(vaultRoot, "Apple.md")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{want}, nil
	}
	var gotEditor string
	openExec = func(editor, file string) error {
		gotEditor = editor
		return nil
	}
	t.Setenv("EDITOR", "emacs")

	// Config has no [open].editor set.
	if _, _, err := runOpen(t, vaultRoot, "open"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotEditor != "emacs" {
		t.Fatalf("editor: got %q, want %q ($EDITOR should be used when config editor is empty)", gotEditor, "emacs")
	}
}

func TestOpen_EnvEditorWithArgs(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	want := filepath.Join(vaultRoot, "Apple.md")

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		return []string{want}, nil
	}
	openExec = func(editor, file string) error { return runEditor(editor, file) }
	t.Setenv("EDITOR", "go env")
	captured := stubExecReplace(t)

	if _, _, err := runOpen(t, vaultRoot, "open"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, wantArgv := *captured, []string{"go", "env", want}; len(got) != len(wantArgv) || got[0] != wantArgv[0] || got[1] != wantArgv[1] || got[2] != wantArgv[2] {
		t.Fatalf("execReplace argv = %v, want %v", got, wantArgv)
	}
}
