package config

import (
	"testing"
)

func TestLoad_TemplatesDirNormalizesDotSlashPrefix(t *testing.T) {
	// A user writing `dir = "./Templates"` or `dir = "././Templates"`
	// should still land on "Templates" after path.Clean normalization.
	for _, raw := range []string{"./Templates", "././Templates", "./Templates/"} {
		cfg, err := loadTOML(t, "[templates]\ndir = \""+raw+"\"\n")
		if err != nil {
			t.Fatalf("Load (%q): %v", raw, err)
		}
		if cfg.Templates.Dir != "Templates" {
			t.Errorf("raw %q: got %q, want %q", raw, cfg.Templates.Dir, "Templates")
		}
	}
}

func TestLoad_TemplatesDirDotOnlyFallsBackToDefault(t *testing.T) {
	// A templates dir of "." should fall back to the default "Templates"
	// since "." is the vault root itself.
	cfg, err := loadTOML(t, "[templates]\ndir = \".\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.Dir != "Templates" {
		t.Errorf("dir = '.' should fall back to Templates, got %q", cfg.Templates.Dir)
	}
}

func TestLoad_TemplatesDirDotSlashOnlyFallsBackToDefault(t *testing.T) {
	// "./" normalizes to "." which should then fall back to "Templates".
	cfg, err := loadTOML(t, "[templates]\ndir = \"./\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.Dir != "Templates" {
		t.Errorf("dir = './' should fall back to Templates, got %q", cfg.Templates.Dir)
	}
}

func TestLoad_TemplatesDirNormalizesBackslashes(t *testing.T) {
	// Backslash-separated config should normalize to the slash-separated
	// vault-relative form Walk compares against, even on Unix hosts.
	cfg, err := loadTOML(t, "[templates]\ndir = 'Meta\\Templates\\'\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.Dir != "Meta/Templates" {
		t.Errorf("backslash templates dir should normalize to Meta/Templates, got %q",
			cfg.Templates.Dir)
	}
}

func TestLoad_TemplatesDirAbsolutePathFallsBack(t *testing.T) {
	// An absolute templates dir cannot match vault-relative Walk paths,
	// so it should fall back to the default rather than silently break
	// template-dir pruning.
	for _, raw := range []string{"/abs/Templates", "/Templates"} {
		cfg, err := loadTOML(t, "[templates]\ndir = \""+raw+"\"\n")
		if err != nil {
			t.Fatalf("Load (%q): %v", raw, err)
		}
		if cfg.Templates.Dir != "Templates" {
			t.Errorf("absolute %q should fall back to Templates, got %q", raw, cfg.Templates.Dir)
		}
	}
}

func TestLoad_TemplatesDirWindowsAbsolutePathFallsBack(t *testing.T) {
	// A Windows drive-rooted templates dir is also absolute, even when the
	// tests run on Unix. It cannot be a vault-relative templates directory.
	for _, raw := range []string{`C:\Templates`, "D:/Templates"} {
		t.Run(raw, func(t *testing.T) {
			cfg, err := loadTOML(t, "[templates]\ndir = '"+raw+"'\n")
			if err != nil {
				t.Fatalf("Load (%q): %v", raw, err)
			}
			if cfg.Templates.Dir != "Templates" {
				t.Errorf("Windows absolute %q should fall back to Templates, got %q", raw, cfg.Templates.Dir)
			}
		})
	}
}

func TestLoad_TemplatesDirParentTraversalFallsBack(t *testing.T) {
	// A templates dir starting with ".." escapes the vault and cannot be
	// pruned by Walk; it should fall back to the default.
	for _, raw := range []string{"..", "../escape", "../../escape"} {
		cfg, err := loadTOML(t, "[templates]\ndir = \""+raw+"\"\n")
		if err != nil {
			t.Fatalf("Load (%q): %v", raw, err)
		}
		if cfg.Templates.Dir != "Templates" {
			t.Errorf("traversal %q should fall back to Templates, got %q", raw, cfg.Templates.Dir)
		}
	}
}

func TestLoad_TemplatesDefaultExtensionRejectsPathSeparators(t *testing.T) {
	// `default_extension` is joined as a suffix to the rendered filename. A
	// value containing `/` or `\` would silently produce paths like
	// "Note.md/file" — the resulting filename is unusable. Fall back to ".md"
	// so the user gets predictable behavior instead of a corrupted path.
	for _, raw := range []string{"/", "md/file", ".md/x", `\bad`, `.md\x`} {
		t.Run(raw, func(t *testing.T) {
			// Use TOML literal string (single quotes) so backslashes pass through verbatim.
			cfg, err := loadTOML(t, "[templates]\ndefault_extension = '"+raw+"'\n")
			if err != nil {
				t.Fatalf("Load (%q): %v", raw, err)
			}
			if cfg.Templates.DefaultExtension != ".md" {
				t.Errorf("raw %q: got %q, want %q", raw, cfg.Templates.DefaultExtension, ".md")
			}
		})
	}
}

func TestLoad_TemplatesDefaultExtensionRejectsControlCharacters(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "tab", raw: `m\td`},
		{name: "del", raw: `m\u007fd`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadTOML(t, "[templates]\ndefault_extension = \""+tc.raw+"\"\n")
			if err != nil {
				t.Fatalf("Load (%q): %v", tc.raw, err)
			}
			if cfg.Templates.DefaultExtension != ".md" {
				t.Errorf("raw %q: got %q, want %q", tc.raw, cfg.Templates.DefaultExtension, ".md")
			}
		})
	}
}

func TestLoad_TemplatesDefaultExtensionDotOnlyFallsBackToDefault(t *testing.T) {
	// `default_extension = "."` would otherwise produce filenames like
	// "Note." — a stray period with no real extension. Fall back to ".md".
	cfg, err := loadTOML(t, "[templates]\ndefault_extension = \".\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("default_extension = '.' should fall back to .md, got %q",
			cfg.Templates.DefaultExtension)
	}
}
