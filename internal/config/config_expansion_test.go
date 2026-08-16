package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_TildeExpansionInVaultFlag(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "vault")
	writeFile(t, filepath.Join(vault, FileName), "")

	// expandUser reads HOME via os.UserHomeDir, not the injected Getenv, so a
	// Getenv that reports everything unset only suppresses $BNOTES_VAULT.
	cfg, err := Load(LoadOptions{VaultFlag: "~/vault", Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_TildeExpansionInVaultEnv(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "myvault")
	writeFile(t, filepath.Join(vault, FileName), "")
	t.Setenv(envVault, "~/myvault")

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault env should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_IgnoreNormalizesTrailingSlashAndDotSlash(t *testing.T) {
	// User provides variants that should all collapse to "Archive" (default).
	cfg, err := loadTOML(t, "ignore = [\"Archive/\", \"./Archive\", \"  Archive  \", \"Drafts/\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	archiveCount := 0
	for _, p := range cfg.Ignore {
		if p == "Archive" {
			archiveCount++
		}
	}
	if archiveCount != 1 {
		t.Fatalf("expected Archive to appear exactly once after normalization, got %d (%v)", archiveCount, cfg.Ignore)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Fatalf("expected Drafts (trailing slash stripped) in ignore: %v", cfg.Ignore)
	}
	for _, p := range cfg.Ignore {
		if strings.HasSuffix(p, "/") {
			t.Errorf("ignore entry %q should have trailing slash trimmed", p)
		}
		if strings.HasPrefix(p, "./") {
			t.Errorf("ignore entry %q should have leading ./ stripped", p)
		}
		if strings.TrimSpace(p) != p {
			t.Errorf("ignore entry %q should have surrounding whitespace stripped", p)
		}
	}
}

func TestLoad_IgnoreDropsEmptyAndDotEntries(t *testing.T) {
	cfg, err := loadTOML(t, "ignore = [\"\", \"   \", \".\", \"./\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Defaults still present.
	for _, want := range DefaultIgnore {
		if !contains(cfg.Ignore, want) {
			t.Errorf("default %q missing: %v", want, cfg.Ignore)
		}
	}
	// No empty or "." entries.
	for _, p := range cfg.Ignore {
		if p == "" || p == "." {
			t.Errorf("ignore should not contain %q: %v", p, cfg.Ignore)
		}
	}
}

// TestLoad_LegacyFrontmatterKeysRejected verifies that config files written
// against the removed split model get a migration error naming the offending
// key rather than a generic unknown-key message.
func TestLoad_LegacyFrontmatterKeysRejected(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{"wikilink_properties", "[frontmatter]\nwikilink_properties = [\"parent\"]\n", "frontmatter.wikilink_properties"},
		{"list_properties", "[frontmatter]\nlist_properties = [\"tags\"]\n", "frontmatter.list_properties"},
		{"schema", "[frontmatter.schema.status]\ntype = \"string\"\n", "frontmatter.schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadTOML(t, tt.toml)
			if err == nil {
				t.Fatalf("expected error for legacy key %s", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error should name %q, got: %v", tt.want, err)
			}
			if !strings.Contains(err.Error(), "[frontmatter.properties.<name>]") {
				t.Fatalf("error should point at the replacement model, got: %v", err)
			}
		})
	}
}

func TestLoad_TemplatesDefaultExtensionWithoutDot(t *testing.T) {
	cfg, err := loadTOML(t, "[templates]\ndefault_extension = \"markdown\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".markdown" {
		t.Errorf("default_extension should be normalized with leading dot: got %q", cfg.Templates.DefaultExtension)
	}
}

func TestLoad_TemplatesDefaultExtensionTrimsWhitespace(t *testing.T) {
	// A value of "  md  " in the TOML file used to flow through as
	// ".  md  ", producing bogus filenames such as `Note.  md  `.
	cfg, err := loadTOML(t, "[templates]\ndefault_extension = \"  md  \"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("default_extension should be trimmed and dotted: got %q, want %q",
			cfg.Templates.DefaultExtension, ".md")
	}
}

func TestLoad_TemplatesDefaultExtensionWhitespaceOnlyFallsBackToDefault(t *testing.T) {
	cfg, err := loadTOML(t, "[templates]\ndefault_extension = \"   \"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("whitespace-only default_extension should fall back to .md, got %q",
			cfg.Templates.DefaultExtension)
	}
}

func TestLoad_TemplatesDirNormalized(t *testing.T) {
	// A user writing `dir = "Templates/"` or stray whitespace must still land
	// on a normalized value so Walk's pruning matches the relative path.
	for _, raw := range []string{"Templates/", "  Templates  ", "Templates"} {
		cfg, err := loadTOML(t, "[templates]\ndir = \""+raw+"\"\n")
		if err != nil {
			t.Fatalf("Load (%q): %v", raw, err)
		}
		if cfg.Templates.Dir != "Templates" {
			t.Errorf("raw %q: got %q, want %q", raw, cfg.Templates.Dir, "Templates")
		}
	}
}
