package template

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindExact(t *testing.T) {
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "book"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "book" {
		t.Fatalf("got %s", got)
	}
}

func TestFindWithExtension(t *testing.T) {
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindTemplateSuffix(t *testing.T) {
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "Book Template.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book Template.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindAmbiguousMatch(t *testing.T) {
	cfg := newDiscoverConfig(t)
	// Two filenames differing only in case match the same case-insensitive
	// candidate "book.md". On case-sensitive filesystems both can coexist; on
	// case-insensitive ones (macOS default) the second write replaces the
	// first and the test degenerates to an exact match — skip it in that
	// case rather than fail.
	writeFile(t, vaultPath(cfg, "Templates", "book.md"), "x")
	writeFile(t, vaultPath(cfg, "Templates", "BOOK.md"), "y")
	entries, err := os.ReadDir(vaultPath(cfg, "Templates"))
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

func TestFindStripsRedundantExtension(t *testing.T) {
	// A user passing "Book.md" must still hit "Book Template.md" — the suffix
	// candidate is built from the stem, not from "Book.md" + " Template.md".
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "Book Template.md"), "x")
	got, err := Find(cfg, "Book.md")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book Template.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindDefaultExtensionFallback(t *testing.T) {
	// An empty DefaultExtension must fall back to ".md".
	cfg := newDiscoverConfig(t)
	cfg.Templates.DefaultExtension = ""
	writeFile(t, vaultPath(cfg, "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s", got)
	}
}

func TestFindPriorityOrder(t *testing.T) {
	// When both the exact name and the stem+ext form exist, the exact name
	// (candidate 1) must win because Find tries candidates in order.
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "book"), "exact")
	writeFile(t, vaultPath(cfg, "Templates", "book.md"), "with-ext")
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
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "JOURNAL.md"), "x")
	got, err := Find(cfg, "journal")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "JOURNAL.md" {
		t.Fatalf("got %s, want JOURNAL.md", filepath.Base(got))
	}
}

func TestFindExtensionWithoutDot(t *testing.T) {
	// A direct caller bypassing config.Load may pass an extension without a
	// leading dot. Find must normalize so candidate construction still
	// produces "book.md" rather than "bookmd".
	cfg := newDiscoverConfig(t)
	cfg.Templates.DefaultExtension = "md"
	writeFile(t, vaultPath(cfg, "Templates", "Book.md"), "x")
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
			cfg := newDiscoverConfig(t)
			cfg.Templates.DefaultExtension = tc.ext
			writeFile(t, vaultPath(cfg, "Templates", "Book.md"), "x")
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

func TestFindNameWithMixedCaseExtension(t *testing.T) {
	// Passing "Book.MD" when DefaultExtension is ".md" should still strip the
	// extension and match "Book Template.md".
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "Book Template.md"), "x")
	got, err := Find(cfg, "Book.MD")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book Template.md" {
		t.Fatalf("got %s, want Book Template.md", filepath.Base(got))
	}
}

func TestFindUnicodeCaseFoldMatch(t *testing.T) {
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "Σ.md"), "x")
	got, err := Find(cfg, "ς")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Σ.md" {
		t.Fatalf("got %s, want Σ.md", filepath.Base(got))
	}
}

func TestFindUnicodeExactNameWithoutLowercasing(t *testing.T) {
	// Preserve the user's original candidate text: lower-casing before matching
	// breaks exact lookups for some Unicode names like Turkish dotted İ, where
	// strings.ToLower("İ") != a string EqualFold considers equivalent to "İ".
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates", "İdea.md"), "x")
	got, err := Find(cfg, "İdea")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "İdea.md" {
		t.Fatalf("got %s, want İdea.md", filepath.Base(got))
	}
}
