package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_NewRoutesNormalizeValues(t *testing.T) {
	// Route values are consumed by bn new via exact map lookup and filepath.Join.
	// Normalize the stored value so whitespace, dot segments, trailing slashes,
	// and backslashes don't leak into destination construction.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = ' ./References\\Books/ '\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.New.Routes["book"]; got != "References/Books" {
		t.Fatalf("new.routes value should be normalized: got %q, want %q",
			got, "References/Books")
	}
}

func TestLoad_NewRoutesDropsWhitespaceOnlyValues(t *testing.T) {
	// A whitespace-only route value should behave as unset, not as a folder
	// literally named with spaces.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = '   '\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.New.Routes["book"]; ok {
		t.Fatalf("whitespace-only route should be dropped, got routes %v", cfg.New.Routes)
	}
}

func TestLoad_NewRoutesRejectCaseFoldCollisions(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = 'Books'\nBOOK = 'References/Books'\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for case-fold-colliding route keys")
	}
	if !strings.Contains(err.Error(), "collide case-insensitively") {
		t.Fatalf("expected case-fold collision error, got %v", err)
	}
}
