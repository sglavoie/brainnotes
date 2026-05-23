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

func TestFindNormalizesDefaultExtensionDirectConfig(t *testing.T) {
	cases := []struct {
		name string
		ext  string
	}{
		{"trims whitespace", "  md  "},
		{"dot only falls back", "."},
		{"separator falls back", "md/file"},
		{"nul falls back", "md\x00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newCfg(t)
			cfg.Templates.DefaultExtension = tc.ext
			writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book.md"), "x")
			got, err := Find(cfg, "book")
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if filepath.Base(got) != "Book.md" {
				t.Fatalf("got %s, want Book.md", filepath.Base(got))
			}
		})
	}
}

func TestFindEmptyStemAfterExtensionStrip(t *testing.T) {
	// A name that consists only of the configured extension reduces to an
	// empty stem; the third candidate (" Template.md") would have a leading
	// space and could accidentally match a poorly-named file. Reject early.
	cfg := newCfg(t)
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".md", ".MD"} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error for empty stem", name)
		}
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

func TestFindUnicodeCaseFoldMatch(t *testing.T) {
	cfg := newCfg(t)
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Σ.md"), "x")
	got, err := Find(cfg, "ς")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Σ.md" {
		t.Fatalf("got %s, want Σ.md", filepath.Base(got))
	}
}

func TestFindTemplatesDirCaseInsensitive(t *testing.T) {
	// The configured templates dir may differ in case from the directory on
	// disk. That should work on case-sensitive filesystems just as it does on
	// macOS/Windows case-insensitive filesystems.
	cfg := newCfg(t)
	cfg.Templates.Dir = "templates"
	writeFile(t, filepath.Join(cfg.Vault, "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s, want Book.md", filepath.Base(got))
	}
}

func TestFindNormalizesTemplatesDirDirectConfig(t *testing.T) {
	cases := []struct {
		name    string
		rawDir  string
		diskDir string
	}{
		{"empty falls back", "", "Templates"},
		{"dot falls back", ".", "Templates"},
		{"trims whitespace", "  Templates/  ", "Templates"},
		{"normalizes backslashes", `Meta\Templates\`, filepath.Join("Meta", "Templates")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newCfg(t)
			cfg.Templates.Dir = tc.rawDir
			writeFile(t, filepath.Join(cfg.Vault, tc.diskDir, "Book.md"), "x")
			got, err := Find(cfg, "book")
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if filepath.Base(got) != "Book.md" {
				t.Fatalf("got %s, want Book.md", filepath.Base(got))
			}
		})
	}
}

func TestFindTemplatesDirNestedCaseInsensitive(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = "meta/templates"
	writeFile(t, filepath.Join(cfg.Vault, "Meta", "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s, want Book.md", filepath.Base(got))
	}
}

func TestFindTemplatesDirAmbiguousCaseInsensitive(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = "TEMPLATES"
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cfg.Vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Skip("case-insensitive filesystem; cannot create ambiguous template dirs")
	}
	_, err = Find(cfg, "book")
	if !errors.Is(err, ErrTemplateAmbiguous) {
		t.Fatalf("expected ErrTemplateAmbiguous, got %v", err)
	}
}

func TestFindTemplatesDirAmbiguousEvenWhenExactExists(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = "Templates"
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "Templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Vault, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cfg.Vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Skip("case-insensitive filesystem; cannot create ambiguous template dirs")
	}
	_, err = Find(cfg, "book")
	if !errors.Is(err, ErrTemplateAmbiguous) {
		t.Fatalf("expected ErrTemplateAmbiguous, got %v", err)
	}
}

func TestFindRejectsTemplatesDirTraversal(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = "../Templates"
	writeFile(t, filepath.Join(cfg.Vault, "..", "Templates", "Book.md"), "x")
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for templates dir escaping vault")
	}
	if !strings.Contains(err.Error(), "escape the vault") {
		t.Fatalf("expected escape error, got %v", err)
	}
}

func TestFindRejectsTemplatesDirAbsolutePath(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = string(filepath.Separator)
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for absolute templates dir")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Fatalf("expected relative-path error, got %v", err)
	}
}

func TestFindRejectsTemplatesDirWindowsAbsolutePath(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = `C:\Templates`
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for Windows absolute templates dir")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Fatalf("expected relative-path error, got %v", err)
	}
}

func TestFindRejectsTemplatesDirNULByte(t *testing.T) {
	cfg := newCfg(t)
	cfg.Templates.Dir = "Templates\x00"
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for NUL byte in templates dir")
	}
}
