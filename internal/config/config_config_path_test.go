package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
