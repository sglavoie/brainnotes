package template

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func newCfg(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	return &config.Config{
		Vault: root,
		Templates: config.TemplatesConfig{
			Dir:              "Templates",
			DefaultExtension: ".md",
		},
	}
}

func TestFindExact(t *testing.T) {
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "book"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "book" {
		t.Fatalf("got %s", got)
	}
}

func TestFindWithExtension(t *testing.T) {
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindTemplateSuffix(t *testing.T) {
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book Template.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book Template.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindMissing(t *testing.T) {
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Find(cfg, "nope")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestFindEmptyName(t *testing.T) {
	cfg := newCfg(t)
	if _, err := Find(cfg, ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}
