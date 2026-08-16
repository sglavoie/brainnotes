package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_ExplicitVaultPathAbsolutizedAndCleaned(t *testing.T) {
	root := newVault(t, "")

	cfg, err := Load(LoadOptions{VaultFlag: root + "/", Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != root {
		t.Errorf("vault should be cleaned: got %q, want %q", cfg.Vault, root)
	}
	if !filepath.IsAbs(cfg.Vault) {
		t.Errorf("vault should be absolute: got %q", cfg.Vault)
	}
}

func TestLoad_ExplicitVaultPathTrimsWhitespace(t *testing.T) {
	root := newVault(t, "")

	cfg, err := Load(LoadOptions{VaultFlag: "  " + root + "  ", Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load with whitespace flag: %v", err)
	}
	if cfg.Vault != root {
		t.Errorf("vault from flag should be trimmed: got %q, want %q", cfg.Vault, root)
	}

	cfg, err = Load(LoadOptions{Getenv: envMap(map[string]string{envVault: "  " + root + "  "})})
	if err != nil {
		t.Fatalf("Load with whitespace env: %v", err)
	}
	if cfg.Vault != root {
		t.Errorf("vault from env should be trimmed: got %q, want %q", cfg.Vault, root)
	}
}

func TestLoad_ExplicitVaultMustExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nonexistent")

	_, err := Load(LoadOptions{VaultFlag: missing, Getenv: isolatedEnv()})
	if err == nil {
		t.Fatal("expected error for missing vault")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error should name the missing path; got %v", err)
	}
}

func TestLoad_ExplicitVaultMustBeDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "")

	_, err := Load(LoadOptions{VaultFlag: file, Getenv: isolatedEnv()})
	if err == nil {
		t.Fatal("expected error for vault path pointing at a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should mention not a directory; got %v", err)
	}
}

func TestLoad_ExplicitVaultWithoutMarkerIsError(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(LoadOptions{VaultFlag: dir, Getenv: isolatedEnv()})
	if err == nil {
		t.Fatalf("expected error for vault without %s", FileName)
	}
	if !strings.Contains(err.Error(), FileName) || !strings.Contains(err.Error(), "bn init") {
		t.Fatalf("error should name the marker and point at `bn init`; got %v", err)
	}
}

func TestLoad_ExplicitEnvVaultWithoutMarkerIsError(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(LoadOptions{Getenv: envMap(map[string]string{envVault: dir})})
	if err == nil {
		t.Fatalf("expected error for $%s without %s", envVault, FileName)
	}
	if !strings.Contains(err.Error(), envVault) {
		t.Fatalf("error should name the environment variable; got %v", err)
	}
}

func TestLoad_ExplicitVaultExpandsTilde(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := mkdirAll(t, filepath.Join(home, "vault"))
	writeFile(t, filepath.Join(root, FileName), "")

	cfg, err := Load(LoadOptions{VaultFlag: "~/vault", Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != root {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, root)
	}
}

func TestLoad_RuntimeFieldsPopulatedWithoutVaultKey(t *testing.T) {
	root := newVault(t, "ignore = [\"Drafts\"]\n")

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != root {
		t.Errorf("Vault: got %q, want %q", cfg.Vault, root)
	}
	want := filepath.Join(root, FileName)
	if cfg.SourcePath != want {
		t.Errorf("SourcePath: got %q, want %q", cfg.SourcePath, want)
	}
	if cfg.VaultOrigin != VaultOriginFlag {
		t.Errorf("VaultOrigin: got %q, want %q", cfg.VaultOrigin, VaultOriginFlag)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Errorf("configured ignore entry was dropped: %v", cfg.Ignore)
	}
}

func TestLoad_VaultKeyInTOMLIsRejected(t *testing.T) {
	other := t.TempDir()

	_, err := loadTOML(t, "vault = \""+other+"\"\n")
	if err == nil {
		t.Fatal("expected error for a leftover vault key")
	}
	if !strings.Contains(err.Error(), "`vault` key was removed") {
		t.Fatalf("error should explain the removed key; got %v", err)
	}
}

func TestLoad_SymlinkedVaultRootIsAccepted(t *testing.T) {
	root := newVault(t, "")
	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	cfg, err := Load(LoadOptions{VaultFlag: link, Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != link {
		t.Fatalf("vault should stay the link path: got %q, want %q", cfg.Vault, link)
	}
}
