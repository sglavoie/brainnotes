package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_TildeExpansionInVaultFlag(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}

	cfg, err := Load("~/vault", "")
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
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	t.Setenv(envVault, "~/myvault")

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault env should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_TildeExpansionInVaultFromConfigFile(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "vfile")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \"~/vfile\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault from config file should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_TildeExpansionInConfigFlag(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := t.TempDir()

	cfgDir := filepath.Join(home, ".myconfig")
	cfgPath := filepath.Join(cfgDir, "bn.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n")

	cfg, err := Load("", "~/.myconfig/bn.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, vault)
	}
}

func TestCandidates_TildeExpansionInEnv(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envConfig, "~/somecfg.toml")

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := filepath.Join(home, "somecfg.toml")
	if cands[1].Path != want {
		t.Fatalf("env path should expand ~: got %q, want %q", cands[1].Path, want)
	}
}

func TestCandidates_TildeExpansionInXDG(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envXDG, "~/customxdg")

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := filepath.Join(home, "customxdg", "bnotes", "config.toml")
	if cands[2].Path != want {
		t.Fatalf("XDG path should expand ~: got %q, want %q", cands[2].Path, want)
	}
}

func TestLoad_ConfigPathDirectoryIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()
	dir := t.TempDir() // exists but is a directory, not a TOML file

	_, err := Load(vault, dir)
	if err == nil {
		t.Fatalf("expected error when --config points at a directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error should mention directory; got %v", err)
	}
}

func TestLoad_IgnoreNormalizesTrailingSlashAndDotSlash(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// User provides variants that should all collapse to "Archive" (default).
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"Archive/\", \"./Archive\", \"  Archive  \", \"Drafts/\"]\n")

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"\", \"   \", \".\", \"./\"]\n")

	cfg, err := Load("", cfgPath)
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

func TestLoad_FrontmatterEmptyListsOptOutOfDefaults(t *testing.T) {
	// Users who write `wikilink_properties = []` should get an empty list,
	// not the defaults — explicit empty differs from omitted.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter]\nwikilink_properties = []\nlist_properties = []\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.WikilinkProperties) != 0 {
		t.Errorf("explicit empty wikilink_properties should stay empty, got %v", cfg.Frontmatter.WikilinkProperties)
	}
	if len(cfg.Frontmatter.ListProperties) != 0 {
		t.Errorf("explicit empty list_properties should stay empty, got %v", cfg.Frontmatter.ListProperties)
	}
}

func TestLoad_TemplatesDefaultExtensionWithoutDot(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \"markdown\"\n")

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \"  md  \"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("default_extension should be trimmed and dotted: got %q, want %q",
			cfg.Templates.DefaultExtension, ".md")
	}
}

func TestLoad_TemplatesDefaultExtensionWhitespaceOnlyFallsBackToDefault(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \"   \"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("whitespace-only default_extension should fall back to .md, got %q",
			cfg.Templates.DefaultExtension)
	}
}

func TestCandidates_BadXDGIgnoredWhenFlagSet(t *testing.T) {
	// Previously a bad ~-expansion in XDG_CONFIG_HOME (HOME unset)
	// would error out Candidates even when --config was provided. The
	// flag is highest priority, so it should still win cleanly.
	resetEnv(t)
	os.Unsetenv("HOME")
	t.Setenv(envXDG, "~/broken")

	flag := filepath.Join(t.TempDir(), "flag.toml")
	cands, err := Candidates(flag)
	if err != nil {
		t.Fatalf("Candidates with flag set should not error: %v", err)
	}
	if !cands[0].Active || cands[0].Path != flag {
		t.Errorf("flag candidate should be active with %q: %+v", flag, cands[0])
	}
	if cands[2].Path != "" {
		t.Errorf("broken XDG path should be empty, got %q", cands[2].Path)
	}
}

func TestCandidates_BadEnvIgnoredWhenFlagSet(t *testing.T) {
	// Bad ~-expansion in $BNOTES_CONFIG (HOME unset) should not break
	// resolution when --config is provided.
	resetEnv(t)
	os.Unsetenv("HOME")
	t.Setenv(envConfig, "~/broken.toml")

	flag := filepath.Join(t.TempDir(), "flag.toml")
	cands, err := Candidates(flag)
	if err != nil {
		t.Fatalf("Candidates with flag set should not error: %v", err)
	}
	if !cands[0].Active || cands[0].Path != flag {
		t.Errorf("flag candidate should be active with %q: %+v", flag, cands[0])
	}
	if cands[1].Path != "" {
		t.Errorf("broken env path should be empty, got %q", cands[1].Path)
	}
}

func TestLoad_TemplatesDirNormalized(t *testing.T) {
	// A user writing `dir = "Templates/"` or stray whitespace must still land
	// on a normalized value so Walk's pruning matches the relative path.
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"Templates/", "  Templates  ", "Templates"} {
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

func TestCandidates_BadEnvStillErrorsWhenNoHigherSource(t *testing.T) {
	resetEnv(t)
	os.Unsetenv("HOME")
	t.Setenv(envConfig, "~/broken.toml")

	if _, err := Candidates(""); err == nil {
		t.Fatalf("expected error when env expansion fails and no flag is set")
	}
}
