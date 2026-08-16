package config

import (
	"strings"
	"testing"
)

// TestLoad_RejectsUnknownTopLevelKey verifies that a typo or unrecognised
// top-level TOML key is rejected with a descriptive error. Previously, the
// BurntSushi/toml decoder silently ignored unknown keys, making typos in
// config files invisible.
func TestLoad_RejectsUnknownTopLevelKey(t *testing.T) {
	_, err := loadTOML(t, `typo_key = "oops"
`)
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

// TestLoad_RejectsLeftoverVaultKey verifies that a config file carried over
// from the external-config era, which still sets the removed `vault` key, gets
// a dedicated migration error rather than a generic unknown-key message.
func TestLoad_RejectsLeftoverVaultKey(t *testing.T) {
	_, err := loadTOML(t, `vault = "/x"
`)
	if err == nil {
		t.Fatal("expected error for leftover vault key, got nil")
	}
	if !strings.Contains(err.Error(), "the `vault` key was removed") {
		t.Fatalf("error should explain that the vault key was removed, got: %v", err)
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Fatalf("error should point at %s, got: %v", FileName, err)
	}
}

// TestLoad_RejectsUnknownNestedKey verifies that an unknown key nested inside
// a known section (e.g. [fzf]) is also rejected.
func TestLoad_RejectsUnknownNestedKey(t *testing.T) {
	_, err := loadTOML(t, `[fzf]
args = ["--height=40%"]
bad_nested = true
`)
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

func TestLoad_RejectsUnknownServeNestedKey(t *testing.T) {
	_, err := loadTOML(t, `[serve]
addr = "127.0.0.1:8729"
bad_nested = true
`)
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

func TestLoad_RejectsUnknownRelatedNestedKey(t *testing.T) {
	_, err := loadTOML(t, `[related]
metadata_properties = ["tags"]
metadata_property = ["topic"]
`)
	if err == nil {
		t.Fatal("expected error for unknown related key, got nil")
	}
	if !strings.Contains(err.Error(), "related.metadata_property") {
		t.Fatalf("error should name related.metadata_property, got: %v", err)
	}
}

// TestLoad_AcceptsAllKnownKeys verifies that a config file using every valid
// known key still loads without error, so the unknown-key check does not
// false-positive on legitimate keys.
func TestLoad_AcceptsAllKnownKeys(t *testing.T) {
	root := newVault(t, `ignore = [".git"]

[templates]
dir = "Templates"
default_extension = ".md"

[frontmatter.properties.author]
type = "wikilink"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[frontmatter.properties.status]
type = "string"
required = true
allowed = ["WIP", "Done"]

[fzf]
args = ["--height=40%"]

[open]
editor = "vim"

[serve]
addr = "127.0.0.1:8729"
open_browser = true

[new]

[related]
metadata_properties = ["categories", "tags"]

[brief]
exclude_folders = ["Archive", "Templates/Drafts"]
exclude_types = ["journal", "scratch"]

[agent]
command = ["my-agent", "--stdin"]
timeout_seconds = 30
`)

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: unexpected error with all-known keys: %v", err)
	}
	if cfg.Vault != root {
		t.Fatalf("vault mismatch: got %q, want %q", cfg.Vault, root)
	}
}
