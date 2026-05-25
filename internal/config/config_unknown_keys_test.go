package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_RejectsUnknownTopLevelKey verifies that a typo or unrecognised
// top-level TOML key is rejected with a descriptive error. Previously, the
// BurntSushi/toml decoder silently ignored unknown keys, making typos in
// config files invisible.
func TestLoad_RejectsUnknownTopLevelKey(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
typo_key = "oops"
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("error should mention 'unknown keys', got: %v", err)
	}
	if !strings.Contains(err.Error(), "typo_key") {
		t.Fatalf("error should name the unknown key 'typo_key', got: %v", err)
	}
}

// TestLoad_RejectsUnknownNestedKey verifies that an unknown key nested inside
// a known section (e.g. [fzf]) is also rejected.
func TestLoad_RejectsUnknownNestedKey(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[fzf]
args = ["--height=40%"]
bad_nested = true
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown nested key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("error should mention 'unknown keys', got: %v", err)
	}
	if !strings.Contains(err.Error(), "bad_nested") {
		t.Fatalf("error should name the unknown key 'bad_nested', got: %v", err)
	}
}

// TestLoad_AcceptsAllKnownKeys verifies that a config file using every valid
// known key still loads without error, so the unknown-key check does not
// false-positive on legitimate keys.
func TestLoad_AcceptsAllKnownKeys(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
ignore = [".git"]

[templates]
dir = "Templates"
default_extension = ".md"

[frontmatter]
wikilink_properties = ["author"]
list_properties = ["tags"]

[frontmatter.schema.status]
type = "string"
required = true
allowed = ["WIP", "Done"]

[fzf]
args = ["--height=40%"]

[open]
editor = "vim"

[new]
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: unexpected error with all-known keys: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault mismatch: got %q, want %q", cfg.Vault, vault)
	}
}
