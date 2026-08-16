package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envMap builds a Getenv that only knows the supplied variables.
func envMap(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func mkdirAll(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

func TestLoad_VaultFlagBeatsEnvAndDiscovery(t *testing.T) {
	flagVault := newVault(t, "")
	envVaultRoot := newVault(t, "")
	discovered := newVault(t, "")

	cfg, err := Load(LoadOptions{
		VaultFlag:  flagVault,
		WorkingDir: discovered,
		Getenv:     envMap(map[string]string{envVault: envVaultRoot}),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != flagVault {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, flagVault)
	}
	if cfg.VaultOrigin != VaultOriginFlag {
		t.Fatalf("origin: got %q, want %q", cfg.VaultOrigin, VaultOriginFlag)
	}
}

func TestLoad_EnvBeatsDiscovery(t *testing.T) {
	envVaultRoot := newVault(t, "")
	discovered := newVault(t, "")

	cfg, err := Load(LoadOptions{
		WorkingDir: discovered,
		Getenv:     envMap(map[string]string{envVault: envVaultRoot}),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != envVaultRoot {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, envVaultRoot)
	}
	if cfg.VaultOrigin != VaultOriginEnv {
		t.Fatalf("origin: got %q, want %q", cfg.VaultOrigin, VaultOriginEnv)
	}
}

func TestLoad_DiscoveryWalksUpwardFromWorkingDir(t *testing.T) {
	root := newVault(t, "")
	deep := mkdirAll(t, filepath.Join(root, "Projects", "Active", "Notes"))

	cfg, err := Load(LoadOptions{WorkingDir: deep, Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != root {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, root)
	}
	if cfg.VaultOrigin != VaultOriginDiscovery {
		t.Fatalf("origin: got %q, want %q", cfg.VaultOrigin, VaultOriginDiscovery)
	}
	if cfg.SourcePath != filepath.Join(root, FileName) {
		t.Fatalf("SourcePath: got %q, want %q", cfg.SourcePath, filepath.Join(root, FileName))
	}
}

func TestLoad_DiscoveryStopsAtNearestMarker(t *testing.T) {
	// A nested vault shadows its parent: the walk must stop at the first
	// brainnotes.toml it meets rather than climbing to the outermost one.
	outer := newVault(t, "")
	inner := mkdirAll(t, filepath.Join(outer, "Inner"))
	writeFile(t, filepath.Join(inner, FileName), "")

	cfg, err := Load(LoadOptions{WorkingDir: inner, Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != inner {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, inner)
	}
}

func TestLoad_DiscoveryFailsWithoutMarker(t *testing.T) {
	start := mkdirAll(t, filepath.Join(t.TempDir(), "no", "vault", "here"))

	_, err := Load(LoadOptions{WorkingDir: start, Getenv: isolatedEnv()})
	if err == nil {
		t.Fatal("expected error when no brainnotes.toml exists in any parent")
	}
	if !strings.Contains(err.Error(), FileName) || !strings.Contains(err.Error(), start) {
		t.Fatalf("error should name the marker and the starting directory; got %v", err)
	}
}

func TestLoad_DiscoveryIgnoresMarkerDirectory(t *testing.T) {
	// A directory named brainnotes.toml is not a config file, so the walk must
	// keep climbing instead of adopting that directory as the vault root.
	root := newVault(t, "")
	child := mkdirAll(t, filepath.Join(root, "child"))
	mkdirAll(t, filepath.Join(child, FileName))

	cfg, err := Load(LoadOptions{WorkingDir: child, Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != root {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, root)
	}
}

func TestLoad_WhitespaceFlagFallsThroughToEnv(t *testing.T) {
	envVaultRoot := newVault(t, "")

	cfg, err := Load(LoadOptions{
		VaultFlag: "   ",
		Getenv:    envMap(map[string]string{envVault: envVaultRoot}),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != envVaultRoot {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, envVaultRoot)
	}
}

func TestLoad_WhitespaceEnvFallsThroughToDiscovery(t *testing.T) {
	root := newVault(t, "")

	cfg, err := Load(LoadOptions{
		WorkingDir: root,
		Getenv:     envMap(map[string]string{envVault: "   "}),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != root {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, root)
	}
	if cfg.VaultOrigin != VaultOriginDiscovery {
		t.Fatalf("origin: got %q, want %q", cfg.VaultOrigin, VaultOriginDiscovery)
	}
}

func TestLoad_InvalidTOMLSurfacesError(t *testing.T) {
	_, err := loadTOML(t, "this is = = not toml\n")
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error should mention parse failure; got %v", err)
	}
}
