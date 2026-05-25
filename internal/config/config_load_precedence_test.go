package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_FlagBeatsEnvAndFile(t *testing.T) {
	resetEnv(t)
	vaultFlag := t.TempDir()
	vaultEnv := t.TempDir()
	vaultFile := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envVault, vaultEnv)

	cfg, err := Load(vaultFlag, cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFlag {
		t.Fatalf("vault precedence: got %q, want %q", cfg.Vault, vaultFlag)
	}
}

func TestLoad_EnvBeatsFile(t *testing.T) {
	resetEnv(t)
	vaultEnv := t.TempDir()
	vaultFile := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envVault, vaultEnv)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultEnv {
		t.Fatalf("env precedence: got %q, want %q", cfg.Vault, vaultEnv)
	}
}

func TestLoad_FileUsedWhenNoFlagOrEnv(t *testing.T) {
	resetEnv(t)
	vaultFile := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("file precedence: got %q, want %q", cfg.Vault, vaultFile)
	}
}

func TestLoad_MissingConfigOKWhenVaultFlagGiven(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	// No --config flag, no $BNOTES_CONFIG, default search paths empty (HOME and
	// XDG point at empty temp dirs).
	cfg, err := Load(vault, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_ExplicitConfigPathMissingIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	missing := filepath.Join(t.TempDir(), "nope.toml")
	_, err := Load(vault, missing)
	if err == nil {
		t.Fatalf("expected error for missing --config path")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error should mention missing path; got %v", err)
	}
}

func TestLoad_InvalidTOMLSurfacesError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "this is = = not toml\n")

	_, err := Load(vault, cfgPath)
	if err == nil {
		t.Fatalf("expected error for invalid TOML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error should mention parse failure; got %v", err)
	}
}

func TestLoad_BNOTES_CONFIGHonored(t *testing.T) {
	resetEnv(t)
	vaultFile := t.TempDir()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "from-env.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envConfig, cfgPath)
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("vault from $BNOTES_CONFIG: got %q, want %q", cfg.Vault, vaultFile)
	}
}

func TestLoad_BNOTES_CONFIGMissingIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope.toml")
	t.Setenv(envConfig, missing)

	if _, err := Load(vault, ""); err == nil {
		t.Fatalf("expected error for missing $BNOTES_CONFIG path")
	}
}

func TestLoad_XDGConfigPath(t *testing.T) {
	resetEnv(t)
	vaultFile := t.TempDir()

	xdg := t.TempDir()
	cfgPath := filepath.Join(xdg, "bnotes", "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envXDG, xdg)
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("vault from XDG path: got %q, want %q", cfg.Vault, vaultFile)
	}
}

func TestLoad_MissingXDGFallsThroughToDefaultConfig(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envXDG, t.TempDir())

	vaultFile := t.TempDir()
	defaultPath := filepath.Join(home, ".config", "bnotes", "config.toml")
	writeFile(t, defaultPath, "vault = \""+vaultFile+"\"\n")

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("Load should fall through to default config when XDG path is missing: got %q, want %q", cfg.Vault, vaultFile)
	}
	if cfg.SourcePath != defaultPath {
		t.Fatalf("SourcePath: got %q, want %q", cfg.SourcePath, defaultPath)
	}
}

func TestLoad_XDGDirectoryDoesNotFallThroughToDefaultConfig(t *testing.T) {
	// An existing XDG config path that is a directory is invalid and should
	// block resolution rather than silently falling through to ~/.config.
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	xdg := t.TempDir()
	xdgPath := filepath.Join(xdg, "bnotes", "config.toml")
	if err := os.MkdirAll(xdgPath, 0o755); err != nil {
		t.Fatalf("mkdir xdg config dir: %v", err)
	}
	t.Setenv(envXDG, xdg)

	vaultFile := t.TempDir()
	defaultPath := filepath.Join(home, ".config", "bnotes", "config.toml")
	writeFile(t, defaultPath, "vault = \""+vaultFile+"\"\n")

	if _, err := Load("", ""); err == nil {
		t.Fatal("expected XDG config directory to be rejected")
	} else if !strings.Contains(err.Error(), xdgPath) || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error should mention XDG directory path, got %v", err)
	}
}

func TestLoad_DanglingSymlinkXDGAncestorDoesNotFallThroughToDefaultConfig(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	xdgLink := filepath.Join(t.TempDir(), "xdg-link")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing-xdg-root"), xdgLink); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	t.Setenv(envXDG, xdgLink)

	vaultFile := t.TempDir()
	defaultPath := filepath.Join(home, ".config", "bnotes", "config.toml")
	writeFile(t, defaultPath, "vault = \""+vaultFile+"\"\n")

	wantPath := filepath.Join(xdgLink, "bnotes", "config.toml")
	if _, err := Load("", ""); err == nil {
		t.Fatal("expected dangling XDG ancestor symlink to be rejected")
	} else if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("error should mention dangling XDG candidate path %q, got %v", wantPath, err)
	}
}
