package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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

func TestNewRejectsEmptyTitle(t *testing.T) {
	_ = buildNewFixture(t)
	_, err := runNew(t, "new", "book", "")
	if err == nil {
		t.Fatal("expected error for empty title, got nil")
	}
}

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
