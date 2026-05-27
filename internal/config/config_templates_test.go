package config

import (
	"path/filepath"
	"testing"
)

func TestLoad_TemplatesDirNormalizesDotSlashPrefix(t *testing.T) {
	// A user writing `dir = "./Templates"` or `dir = "././Templates"`
	// should still land on "Templates" after path.Clean normalization.
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"./Templates", "././Templates", "./Templates/"} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \""+raw+"\"\n")
		cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \".\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.Dir != "Templates" {
		t.Errorf("dir = '.' should fall back to Templates, got %q", cfg.Templates.Dir)
	}
}

func TestLoad_TemplatesDirDotSlashOnlyFallsBackToDefault(t *testing.T) {
	// "./" normalizes to "." which should then fall back to "Templates".
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \"./\"\n")

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = 'Meta\\Templates\\'\n")

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"/abs/Templates", "/Templates"} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \""+raw+"\"\n")
		cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{`C:\Templates`, "D:/Templates"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = '"+raw+"'\n")
			cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"..", "../escape", "../../escape"} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \""+raw+"\"\n")
		cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"/", "md/file", ".md/x", `\bad`, `.md\x`} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			// Use TOML literal string (single quotes) so backslashes pass through verbatim.
			writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = '"+raw+"'\n")
			cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "tab", raw: `m\td`},
		{name: "del", raw: `m\u007fd`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \""+tc.raw+"\"\n")

			cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \".\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("default_extension = '.' should fall back to .md, got %q",
			cfg.Templates.DefaultExtension)
	}
}
