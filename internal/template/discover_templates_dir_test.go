package template

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindTemplatesDirCaseInsensitive(t *testing.T) {
	// The configured templates dir may differ in case from the directory on
	// disk. That should work on case-sensitive filesystems just as it does on
	// macOS/Windows case-insensitive filesystems.
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "templates"
	writeFile(t, vaultPath(cfg, "Templates", "Book.md"), "x")
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
			cfg := newDiscoverConfig(t)
			cfg.Templates.Dir = tc.rawDir
			writeFile(t, vaultPath(cfg, tc.diskDir, "Book.md"), "x")
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
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "meta/templates"
	writeFile(t, vaultPath(cfg, "Meta", "Templates", "Book.md"), "x")
	got, err := Find(cfg, "book")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if filepath.Base(got) != "Book.md" {
		t.Fatalf("got %s, want Book.md", filepath.Base(got))
	}
}

func TestFindTemplatesDirAmbiguousCaseInsensitive(t *testing.T) {
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "TEMPLATES"
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	mustMkdirAll(t, vaultPath(cfg, "templates"))
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
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "Templates"
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	mustMkdirAll(t, vaultPath(cfg, "templates"))
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
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "../Templates"
	writeFile(t, vaultPath(cfg, "..", "Templates", "Book.md"), "x")
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for templates dir escaping vault")
	}
	if !strings.Contains(err.Error(), "escape the vault") {
		t.Fatalf("expected escape error, got %v", err)
	}
}

func TestFindRejectsTemplatesDirAbsolutePath(t *testing.T) {
	cfg := newDiscoverConfig(t)
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
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = `C:\Templates`
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for Windows absolute templates dir")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Fatalf("expected relative-path error, got %v", err)
	}
}

func TestFindRejectsTemplatesDirWindowsDriveRelativePath(t *testing.T) {
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = `C:Templates`
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for Windows drive-relative templates dir")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Fatalf("expected relative-path error, got %v", err)
	}
}

func TestFindRejectsTemplatesDirNULByte(t *testing.T) {
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "Templates\x00"
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for NUL byte in templates dir")
	}
}

func TestFindTemplatesDirMustBeDirectory(t *testing.T) {
	cfg := newDiscoverConfig(t)
	writeFile(t, vaultPath(cfg, "Templates"), "not a directory")

	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for file-backed templates path")
	}
	msg := err.Error()
	for _, want := range []string{
		`templates path "`,
		`must be a directory, not a file`,
		`set templates.dir to a directory`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestFindTemplatesDirCaseInsensitiveFileMatch(t *testing.T) {
	cfg := newDiscoverConfig(t)
	cfg.Templates.Dir = "templates"
	writeFile(t, vaultPath(cfg, "Templates"), "not a directory")

	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for case-insensitive file-backed templates path")
	}
	msg := err.Error()
	for _, want := range []string{
		`templates path "`,
		`must be a directory, not a file`,
		`set templates.dir to a directory`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}
