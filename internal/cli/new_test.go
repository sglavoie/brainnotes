package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
