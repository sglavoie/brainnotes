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

func TestLoad_VaultMustExist(t *testing.T) {
	resetEnv(t)
	missing := filepath.Join(t.TempDir(), "nonexistent")
	if _, err := Load(missing, ""); err == nil {
		t.Fatalf("expected error for missing vault")
	}
}

func TestLoad_VaultMustBeDirectory(t *testing.T) {
	resetEnv(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	writeFile(t, file, "")

	_, err := Load(file, "")
	if err == nil {
		t.Fatalf("expected error for vault path pointing at file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should mention not a directory; got %v", err)
	}
}

func TestLoad_NoVaultAnywhere(t *testing.T) {
	resetEnv(t)
	if _, err := Load("", ""); err == nil {
		t.Fatalf("expected error when no vault path is set")
	}
}
