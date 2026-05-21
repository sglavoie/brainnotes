package template

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestFindWhitespaceOnlyName(t *testing.T) {
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"   ", "\t", " \n "} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error for whitespace-only name", name)
		}
	}
}

func TestFindRejectsPathSeparators(t *testing.T) {
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../etc/passwd", "sub/book", `sub\book`} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error, got nil", name)
		}
	}
}

func TestFindAmbiguousMatch(t *testing.T) {
	cfg := newCfg(t)
	// Two filenames differing only in case match the same case-insensitive
	// candidate "book.md". On case-sensitive filesystems both can coexist; on
	// case-insensitive ones (macOS default) the second write replaces the
	// first and the test degenerates to an exact match — skip it in that
	// case rather than fail.
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "book.md"), "x")
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "BOOK.md"), "y")
	entries, err := os.ReadDir(filepath.Join(cfg.Vault, "Templates"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Skip("case-insensitive filesystem; cannot create ambiguous templates")
	}
	_, err = Find(cfg, "book")
	if !errors.Is(err, ErrTemplateAmbiguous) {
		t.Fatalf("expected ErrTemplateAmbiguous, got %v", err)
	}
}

func TestFindNilConfig(t *testing.T) {
	if _, err := Find(nil, "book"); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestFindDirectoriesIgnored(t *testing.T) {
	// A subdirectory named like a template must not be returned.
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates", "Book.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Find(cfg, "book")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestFindStripsRedundantExtension(t *testing.T) {
	// A user passing "Book.md" must still hit "Book Template.md" — the suffix
	// candidate is built from the stem, not from "Book.md" + " Template.md".
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book Template.md"), "x")
	got, err := Find(cfg, "Book.md")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book Template.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindNotFoundIncludesCandidates(t *testing.T) {
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Find(cfg, "ghost")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"ghost", "ghost.md", "ghost template.md"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing candidate %q", msg, want)
		}
	}
}

func TestFindDefaultExtensionFallback(t *testing.T) {
	// An empty DefaultExtension must fall back to ".md".
	cfg := newCfg(t)
	cfg.Templates.DefaultExtension = ""
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindMissingTemplateDir(t *testing.T) {
	// When the templates directory does not exist, Find should return an error
	// (not panic or return an empty path).
	cfg := newCfg(t)
	// Do not create the Templates directory.
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for missing template dir")
	}
	if strings.Contains(err.Error(), "nil pointer") {
		t.Fatalf("got nil pointer error; should be a clean read error: %v", err)
	}
}

func TestFindPriorityOrder(t *testing.T) {
	// When both the exact name and the stem+ext form exist, the exact name
	// (candidate 1) must win because Find tries candidates in order.
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "book"), "exact")
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "book.md"), "with-ext")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "book" {
		t.Fatalf("expected exact match %q, got %q", "book", filepath.Base(got))
	}
}

func TestFindCaseInsensitiveMatch(t *testing.T) {
	// Lookup must be case-insensitive.
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "JOURNAL.md"), "x")
	got, err := Find(cfg, "journal")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "JOURNAL.md" {
		t.Fatalf("got %s, want JOURNAL.md", filepath.Base(got))
	}
}

func TestFindRejectsNULByte(t *testing.T) {
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(cfg, "book\x00.md"); err == nil {
		t.Fatal("expected error for NUL byte in name")
	}
}

func TestFindRejectsDotNames(t *testing.T) {
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", ".."} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error", name)
		}
	}
}

func TestFindExtensionWithoutDot(t *testing.T) {
	// A direct caller bypassing config.Load may pass an extension without a
	// leading dot. Find must normalize so candidate construction still
	// produces "book.md" rather than "bookmd".
	cfg := newCfg(t)
	cfg.Templates.DefaultExtension = "md"
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s, want Book.md", filepath.Base(got))
	}
}

func TestFindNameWithMixedCaseExtension(t *testing.T) {
	// Passing "Book.MD" when DefaultExtension is ".md" should still strip the
	// extension and match "Book Template.md".
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book Template.md"), "x")
	got, err := Find(cfg, "Book.MD")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book Template.md" {
		t.Fatalf("got %s, want Book Template.md", filepath.Base(got))
	}
}
