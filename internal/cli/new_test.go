package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
)

// buildNewFixture writes a minimal vault with a Templates/ dir and the book
// template required by most `new` tests, then writes a config that routes
// `book` to References/Books.
func buildNewFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tmplDir := filepath.Join(root, "Templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bookTmpl := "---\ntitle: {{ .Title }}\ncreated: {{today}}\nauthor: {{ .Vars.author }}\n---\nslug: {{ .Slug }}\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "Book Template.md"), []byte(bookTmpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n\n[new.routes]\nbook = \"References/Books\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
	return root
}

// runNew executes the root command with the given args and returns
// (stdoutBytes, error). It restores the package-level clock on return.
func runNew(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := clockForNew
	clockForNew = tmpl.FixedClock{T: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)}
	t.Cleanup(func() { clockForNew = old })

	cmd := NewRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestNewBookRouting(t *testing.T) {
	root := buildNewFixture(t)

	out, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	if !strings.Contains(out, created) {
		t.Fatalf("stdout %q does not mention %q", out, created)
	}
	data, err := os.ReadFile(created)
	if err != nil {
		t.Fatalf("read created: %v", err)
	}
	got := string(data)
	want := "---\ntitle: Dune\ncreated: 2026-05-18\nauthor: Herbert\n---\nslug: dune\n"
	if got != want {
		t.Fatalf("content mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestNewFolderOverride(t *testing.T) {
	root := buildNewFixture(t)

	out, err := runNew(t, "new", "book", "Beloved", "--folder", "References", "--var", "author=Morrison")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	created := filepath.Join(root, "References", "Beloved.md")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected %s to exist: %v", created, err)
	}
	// Should not have been placed under the routed default.
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Beloved.md")); err == nil {
		t.Fatal("file leaked into routed folder despite --folder override")
	}
}

func TestNewFolderOverrideNormalizesLikeRoute(t *testing.T) {
	root := buildNewFixture(t)

	out, err := runNew(t, "new", "book", "Beloved", "--folder", ` ./References\Books/ `, "--var", "author=Morrison")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	created := filepath.Join(root, "References", "Books", "Beloved.md")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected normalized destination %s to exist: %v", created, err)
	}
	if !strings.Contains(out, created) {
		t.Fatalf("stdout %q does not mention normalized path %q", out, created)
	}
}

func TestNewRejectsWhitespaceFolderOverride(t *testing.T) {
	_ = buildNewFixture(t)

	_, err := runNew(t, "new", "book", "Blank", "--folder", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only --folder, got nil")
	}
	if !strings.Contains(err.Error(), "whitespace-only") {
		t.Fatalf("expected whitespace-only error, got %v", err)
	}
}

func TestNewRejectsAbsoluteFolderOverride(t *testing.T) {
	_ = buildNewFixture(t)

	_, err := runNew(t, "new", "book", "Abs", "--folder", "/tmp/books")
	if err == nil {
		t.Fatal("expected error for absolute --folder, got nil")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func TestNewRejectsWindowsAbsoluteFolderOverride(t *testing.T) {
	_ = buildNewFixture(t)

	_, err := runNew(t, "new", "book", "Abs", "--folder", `C:\Books`)
	if err == nil {
		t.Fatal("expected error for Windows absolute --folder, got nil")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func TestNewRefusesOverwrite(t *testing.T) {
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runNew(t, "new", "book", "Dune")
	if err == nil {
		t.Fatal("expected error on existing file without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "existing" {
		t.Fatalf("file was overwritten: %q", data)
	}
}

func TestNewForceOverwrite(t *testing.T) {
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=H", "--force"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "title: Dune") {
		t.Fatalf("expected rendered content, got %q", data)
	}
}

func TestNewEdit_EditorArgsSupported(t *testing.T) {
	root := buildNewFixture(t)
	t.Setenv("EDITOR", "go env")
	captured := stubExecReplace(t)

	out, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert", "--edit")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected created note at %s: %v", created, err)
	}
	if got, want := *captured, []string{"go", "env", created}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("execReplace argv = %v, want %v", got, want)
	}
}

// TestNewCreatesNoteWithReadableMode verifies that the atomic temp-file
// write used by `bn new` widens a brand-new note from os.CreateTemp's 0600 to
// the normal 0644 note mode.
func TestNewCreatesNoteWithReadableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	root := buildNewFixture(t)

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	info, err := os.Stat(created)
	if err != nil {
		t.Fatalf("stat created note: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("created note mode = %o, want 0644", got)
	}
}

// TestNewForcePreservesExistingMode verifies forced overwrites keep the
// destination's existing permission bits instead of inheriting the 0600 mode
// from the temporary file.
func TestNewForcePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=H", "--force"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat overwritten note: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("overwritten note mode = %o, want 0640", got)
	}
}

// TestNewRejectsFolderOutsideVault verifies that `bn new --folder ../escape`
// is rejected rather than silently writing past the vault root. Before the
// fix, resolveDest produced an absolute path outside the vault and the
// command happily created the file.
func TestNewRejectsFolderOutsideVault(t *testing.T) {
	root := buildNewFixture(t)
	_, err := runNew(t, "new", "book", "Pwn", "--folder", "../escape", "--var", "author=x")
	if err == nil {
		t.Fatal("expected error for --folder ../escape, got nil")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected 'outside the vault' error, got: %v", err)
	}
	// Ensure nothing leaked outside the vault.
	parent := filepath.Dir(root)
	if _, statErr := os.Stat(filepath.Join(parent, "escape", "Pwn.md")); statErr == nil {
		t.Fatalf("file leaked outside vault at %s", filepath.Join(parent, "escape", "Pwn.md"))
	}
}

// TestNewRejectsTitleWithSlash verifies that a title containing `/` is
// rejected with a clear error rather than producing a confusing temp-file
// failure deep in atomicWriteFile.
func TestNewRejectsTitleWithSlash(t *testing.T) {
	_ = buildNewFixture(t)
	_, err := runNew(t, "new", "book", "Foo/Bar")
	if err == nil {
		t.Fatal("expected error for title containing '/', got nil")
	}
	if !strings.Contains(err.Error(), "/") && !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected error mentioning title/slash, got: %v", err)
	}
}

// TestNewRejectsEmptyTitle keeps the empty-title contract (preserves existing
// behaviour through the new validateNewTitle path).
func TestNewRejectsEmptyTitle(t *testing.T) {
	_ = buildNewFixture(t)
	_, err := runNew(t, "new", "book", "")
	if err == nil {
		t.Fatal("expected error for empty title, got nil")
	}
}

// TestNewNormalizesExtensionWithoutLeadingDot verifies that a configured
// default extension lacking a leading `.` (e.g. `default_extension = "md"`)
// produces `Title.md`, not `Title md`. The rename command already normalized
// the prefix; the new command did not, yielding files named like "Dune md".
func TestNewNormalizesExtensionWithoutLeadingDot(t *testing.T) {
	root := t.TempDir()
	tmplDir := filepath.Join(root, "Templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "Book Template.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n\n[templates]\ndefault_extension = \"md\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	if _, err := runNew(t, "new", "book", "Dune"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := filepath.Join(root, "Dune.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Dune md")); err == nil {
		t.Fatalf("file with non-normalized extension exists at %s", filepath.Join(root, "Dune md"))
	}
}

func TestNewNormalizesExtensionFromDirectConfig(t *testing.T) {
	root := t.TempDir()
	tmplDir := filepath.Join(root, "Templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "Book Template.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	cfg := &config.Config{
		Vault: root,
		Templates: config.TemplatesConfig{
			DefaultExtension: " md ",
		},
	}
	cmd := NewNewCmd()
	cmd.SetContext(context.WithValue(context.Background(), configKey, cfg))
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"book", "Dune"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}
	want := filepath.Join(root, "Dune.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected normalized output %s to exist: %v", want, err)
	}
}

func TestNewParseVars(t *testing.T) {
	tests := []struct {
		in      []string
		wantErr bool
	}{
		{[]string{"a=1", "b=2"}, false},
		{[]string{"a=with=equals"}, false},
		{[]string{"noequals"}, true},
		{[]string{"=novalue"}, true},
	}
	for _, c := range tests {
		_, err := parseVars(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseVars(%v) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
	}
	m, err := parseVars([]string{"author=Ada", "year=1970"})
	if err != nil {
		t.Fatal(err)
	}
	if m["author"] != "Ada" || m["year"] != "1970" {
		t.Fatalf("parseVars unexpected: %v", m)
	}
}
