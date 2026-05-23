package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_VaultPathAbsolutized(t *testing.T) {
	// A relative vault path should be converted to an absolute path by Load.
	resetEnv(t)

	// Create a directory that we can reference relatively.
	base := t.TempDir()
	vault := filepath.Join(base, "rel", "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Use an absolute path in the config that has an un-cleaned trailing slash
	// and double separators to verify Clean is applied.
	trailingSlash := vault + "/"
	cfg, err := Load(trailingSlash, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Errorf("vault should be cleaned: got %q, want %q", cfg.Vault, vault)
	}
	if !filepath.IsAbs(cfg.Vault) {
		t.Errorf("vault should be absolute: got %q", cfg.Vault)
	}
}

func TestLoad_VaultPathTrimsWhitespace(t *testing.T) {
	// Surrounding whitespace in the vault path (from flag, env, or file)
	// should be stripped so that `vault = " /path "` does not produce
	// a stat error on a path containing stray spaces.
	resetEnv(t)
	vault := t.TempDir()

	// Whitespace in flag path.
	cfg, err := Load("  "+vault+"  ", "")
	if err != nil {
		t.Fatalf("Load with whitespace flag: %v", err)
	}
	if cfg.Vault != vault {
		t.Errorf("vault from flag should be trimmed: got %q, want %q", cfg.Vault, vault)
	}

	// Whitespace in env var.
	resetEnv(t)
	t.Setenv(envVault, "  "+vault+"  ")
	cfg, err = Load("", "")
	if err != nil {
		t.Fatalf("Load with whitespace env: %v", err)
	}
	if cfg.Vault != vault {
		t.Errorf("vault from env should be trimmed: got %q, want %q", cfg.Vault, vault)
	}

	// Whitespace in config file value.
	resetEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \"  "+vault+"  \"\n")
	cfg, err = Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load with whitespace in file: %v", err)
	}
	if cfg.Vault != vault {
		t.Errorf("vault from file should be trimmed: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_VaultWhitespaceOnlyIsError(t *testing.T) {
	// A vault path that is only whitespace should be treated as unset.
	resetEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \"   \"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for whitespace-only vault path")
	}
	if !strings.Contains(err.Error(), "vault path is not set") {
		t.Fatalf("error should say vault is not set; got %v", err)
	}
}

func TestLoad_EmptyConfigFileUsesDefaults(t *testing.T) {
	// An empty config file is valid TOML; all defaults should be applied.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "")

	cfg, err := Load(vault, cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourcePath != cfgPath {
		t.Errorf("SourcePath: got %q, want %q", cfg.SourcePath, cfgPath)
	}
	if cfg.Templates.Dir != "Templates" {
		t.Errorf("Templates.Dir: got %q, want %q", cfg.Templates.Dir, "Templates")
	}
	for _, want := range DefaultIgnore {
		if !contains(cfg.Ignore, want) {
			t.Errorf("default ignore %q missing: %v", want, cfg.Ignore)
		}
	}
}

func TestLoad_IgnoreNormalizesRepeatedDotSlashPrefix(t *testing.T) {
	// "././Archive" should collapse to "Archive" and dedup with the default.
	// Before the fix, normalizeIgnore only stripped a single "./" prefix,
	// leaving "././Archive" as "./Archive" which did not dedup with "Archive".
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"././Archive\", \"./././Drafts\"]\n")

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
		t.Fatalf("expected Archive once after ././ normalization, got %d (%v)", archiveCount, cfg.Ignore)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Fatalf("expected Drafts after ./././ normalization: %v", cfg.Ignore)
	}
}

func TestLoad_IgnoreNormalizesInteriorDotSegments(t *testing.T) {
	// "sub/./dir" should clean to "sub/dir" via path.Clean.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"sub/./dir\", \"sub/dir\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	count := 0
	for _, p := range cfg.Ignore {
		if p == "sub/dir" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected sub/dir once after path.Clean dedup, got %d (%v)", count, cfg.Ignore)
	}
}

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

func TestLoad_WhitespaceFlagVaultFallsThroughToEnv(t *testing.T) {
	// A whitespace-only --vault flag must not shadow $BNOTES_VAULT. Previously
	// the flag's whitespace was assigned to cfg.Vault, then trimmed to "", so
	// the env-provided vault was silently dropped.
	resetEnv(t)
	envVaultPath := t.TempDir()
	t.Setenv(envVault, envVaultPath)

	cfg, err := Load("   ", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != envVaultPath {
		t.Fatalf("whitespace flag should fall through to env: got %q, want %q",
			cfg.Vault, envVaultPath)
	}
}

func TestLoad_WhitespaceEnvVaultFallsThroughToFile(t *testing.T) {
	// A whitespace-only $BNOTES_VAULT must not shadow the vault key in the
	// config file. Previously the env's whitespace was assigned to cfg.Vault,
	// then trimmed to "", so the file's vault key was silently dropped.
	resetEnv(t)
	fileVault := t.TempDir()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+fileVault+"\"\n")
	t.Setenv(envVault, "   ")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != fileVault {
		t.Fatalf("whitespace env should fall through to file: got %q, want %q",
			cfg.Vault, fileVault)
	}
}

func TestLoad_SymlinkedConfigFileResolves(t *testing.T) {
	// A symlink pointing at a real config file should be followed by Load.
	resetEnv(t)
	vault := t.TempDir()

	realDir := t.TempDir()
	realPath := filepath.Join(realDir, "real.toml")
	writeFile(t, realPath, "vault = \""+vault+"\"\n")

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "link.toml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	cfg, err := Load("", linkPath)
	if err != nil {
		t.Fatalf("Load via symlink: %v", err)
	}
	if cfg.Vault != vault {
		t.Errorf("vault via symlinked config: got %q, want %q", cfg.Vault, vault)
	}
	if cfg.SourcePath != linkPath {
		t.Errorf("SourcePath should remain the link path: got %q, want %q",
			cfg.SourcePath, linkPath)
	}
}

func TestLoad_DanglingSymlinkConfigPathIsError(t *testing.T) {
	// A dangling symlink (target missing) provided explicitly via --config
	// should be a hard error rather than silently treated as missing.
	resetEnv(t)
	vault := t.TempDir()

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "dangling.toml")
	if err := os.Symlink(filepath.Join(linkDir, "no-such-target.toml"), linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := Load(vault, linkPath)
	if err == nil {
		t.Fatalf("expected error for dangling symlink config")
	}
}

func TestLoad_WhitespaceBNOTES_CONFIGFallsThrough(t *testing.T) {
	// A whitespace-only $BNOTES_CONFIG must not be treated as an explicit
	// path; it should fall through to the next precedence level. Previously
	// the whitespace value would be stat'd and produce a confusing
	// "config: file    does not exist" error.
	resetEnv(t)
	vaultFile := t.TempDir()
	xdg := t.TempDir()
	cfgPath := filepath.Join(xdg, "bnotes", "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envConfig, "   ")
	t.Setenv(envXDG, xdg)

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load with whitespace BNOTES_CONFIG: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("XDG should win when BNOTES_CONFIG is whitespace: got %q, want %q",
			cfg.Vault, vaultFile)
	}
}

func TestLoad_WhitespaceConfigFlagFallsThrough(t *testing.T) {
	// A whitespace-only --config flag should not be treated as explicit;
	// it should fall through so a working env or default still resolves.
	resetEnv(t)
	vaultFile := t.TempDir()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "from-env.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envConfig, cfgPath)

	cfg, err := Load("", "   ")
	if err != nil {
		t.Fatalf("Load with whitespace --config: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("env should win when --config is whitespace: got %q, want %q",
			cfg.Vault, vaultFile)
	}
}

func TestCandidates_WhitespaceXDGTreatedAsUnset(t *testing.T) {
	// Whitespace-only XDG_CONFIG_HOME used to produce a bogus path like
	// " /bnotes/config.toml"; instead it should be ignored.
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envXDG, "   ")

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if cands[2].Path != "" {
		t.Errorf("whitespace XDG should be empty, got %q", cands[2].Path)
	}
	if !cands[3].Active {
		t.Errorf("default path should be active when XDG is whitespace: %+v", cands)
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
