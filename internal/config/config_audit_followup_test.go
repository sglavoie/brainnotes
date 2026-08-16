package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_NewRoutesValueTrimsWhitespace verifies that surrounding whitespace
// in a route value is stripped so `bn new` does not write to a folder named
// "  Books  ".
func TestLoad_NewRoutesValueTrimsWhitespace(t *testing.T) {
	cfg, err := loadTOML(t, "[new.routes]\nbook = \"  Books  \"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.New.Routes["book"] != "Books" {
		t.Errorf("New.Routes[book]: got %q, want %q", cfg.New.Routes["book"], "Books")
	}
}

// TestLoad_NewRoutesWhitespaceOnlyValueDropped verifies that an
// all-whitespace route value is treated as unset (delete from the map) so
// the consumer's `Routes[name] != ""` gate falls through to the default
// folder rather than joining a whitespace-only segment.
func TestLoad_NewRoutesWhitespaceOnlyValueDropped(t *testing.T) {
	cfg, err := loadTOML(t, "[new.routes]\nbook = \"   \"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.New.Routes["book"]; ok {
		t.Errorf("whitespace-only route should be dropped from map, got %q", cfg.New.Routes["book"])
	}
}

// TestLoad_NewRoutesEmptyRouteNameRejected verifies that an empty route
// name is rejected — it cannot match any real template name and silently
// swallows the user's intent.
func TestLoad_NewRoutesEmptyRouteNameRejected(t *testing.T) {
	_, err := loadTOML(t, "[new.routes]\n\"\" = \"Books\"\n")
	if err == nil {
		t.Fatal("expected error for empty route name")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should mention empty route name, got %v", err)
	}
}

// TestLoad_PropertyAllowedEmptyEntryRejected verifies that an empty string
// inside the `allowed` list is rejected — it would let an empty
// frontmatter value silently pass the constraint at lint time.
func TestLoad_PropertyAllowedEmptyEntryRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.status]
type = "string"
allowed = ["WIP", "", "Done"]
`)
	if err == nil {
		t.Fatal("expected error for empty entry in allowed")
	}
	if !strings.Contains(err.Error(), "allowed") {
		t.Fatalf("error should mention allowed, got %v", err)
	}
}

// TestLoad_PropertyAllowedWhitespaceEntryRejected verifies that a
// whitespace-only entry in `allowed` is rejected for the same reason.
func TestLoad_PropertyAllowedWhitespaceEntryRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.status]
type = "string"
allowed = ["WIP", "   ", "Done"]
`)
	if err == nil {
		t.Fatal("expected error for whitespace entry in allowed")
	}
}

// TestLoad_DuplicateTOMLKeysIsError documents the parser-level rejection
// of duplicate keys. BurntSushi/toml refuses duplicates by spec; this
// guards against a future loader swap silently overwriting the user's
// intent.
func TestLoad_DuplicateTOMLKeysIsError(t *testing.T) {
	if _, err := loadTOML(t, "ignore = [\"Drafts\"]\nignore = [\"Other\"]\n"); err == nil {
		t.Fatalf("expected error for duplicate ignore key")
	}
}

// TestLoad_TildeOnlyVaultExpands verifies that a bare "~" is expanded to
// the user's home directory rather than left as the literal character.
func TestLoad_TildeOnlyVaultExpands(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, FileName), "")

	cfg, err := Load(LoadOptions{VaultFlag: "~", Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != home {
		t.Errorf("vault '~' should expand to HOME: got %q, want %q", cfg.Vault, home)
	}
}

// TestLoad_RelativeVaultPathFlagAbsolutized verifies that a relative
// vault path provided via --vault is converted to an absolute path
// (relative to the working directory). The Vault field is consumed by
// downstream packages that assume an absolute path.
func TestLoad_RelativeVaultPathFlagAbsolutized(t *testing.T) {
	// Create a real vault root and change into its parent so a relative
	// "child" reference works.
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	writeFile(t, filepath.Join(child, FileName), "")
	t.Chdir(parent)

	cfg, err := Load(LoadOptions{VaultFlag: "child", Getenv: isolatedEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(cfg.Vault) {
		t.Errorf("vault should be absolutized, got %q", cfg.Vault)
	}
	// Resolve EvalSymlinks-style equality: macOS prepends /private to /tmp,
	// so compare via filepath.Clean on both ends.
	wantAbs, _ := filepath.Abs(child)
	if cfg.Vault != wantAbs {
		t.Errorf("vault path: got %q, want %q", cfg.Vault, wantAbs)
	}
}
