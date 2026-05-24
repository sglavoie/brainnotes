package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_NewRoutesValueTrimsWhitespace verifies that surrounding whitespace
// in a route value is stripped so `bn new` does not write to a folder named
// "  Books  ".
func TestLoad_NewRoutesValueTrimsWhitespace(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = \"  Books  \"\n")

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = \"   \"\n")

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\n\"\" = \"Books\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for empty route name")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should mention empty route name, got %v", err)
	}
}

// TestLoad_SchemaAllowedEmptyEntryRejected verifies that an empty string
// inside the `allowed` list is rejected — it would let an empty
// frontmatter value silently pass the constraint at lint time.
func TestLoad_SchemaAllowedEmptyEntryRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
type = "string"
allowed = ["WIP", "", "Done"]
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for empty entry in allowed")
	}
	if !strings.Contains(err.Error(), "allowed") {
		t.Fatalf("error should mention allowed, got %v", err)
	}
}

// TestLoad_SchemaAllowedWhitespaceEntryRejected verifies that a
// whitespace-only entry in `allowed` is rejected for the same reason.
func TestLoad_SchemaAllowedWhitespaceEntryRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
type = "string"
allowed = ["WIP", "   ", "Done"]
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for whitespace entry in allowed")
	}
}

// TestLoad_DuplicateTOMLKeysIsError documents the parser-level rejection
// of duplicate keys. BurntSushi/toml refuses duplicates by spec; this
// guards against a future loader swap silently overwriting the user's
// intent.
func TestLoad_DuplicateTOMLKeysIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nvault = \"/other\"\n")

	if _, err := Load("", cfgPath); err == nil {
		t.Fatalf("expected error for duplicate vault key")
	}
}

// TestLoad_TildeOnlyVaultExpands verifies that a bare "~" is expanded to
// the user's home directory rather than left as the literal character.
func TestLoad_TildeOnlyVaultExpands(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load("~", "")
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
	resetEnv(t)
	// Create a real directory and change into its parent so a relative
	// "child" reference works.
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(parent)

	cfg, err := Load("child", "")
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

// TestCandidates_FlagInactiveWhenWhitespaceOnly verifies that a
// whitespace-only --config flag does not produce an active candidate;
// downstream sources must still be consulted.
func TestCandidates_FlagInactiveWhenWhitespaceOnly(t *testing.T) {
	resetEnv(t)
	envPath := filepath.Join(t.TempDir(), "env.toml")
	t.Setenv(envConfig, envPath)

	cands, err := Candidates("   ")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if cands[0].Active || cands[0].Path != "" {
		t.Errorf("whitespace flag should produce empty/inactive entry: %+v", cands[0])
	}
	if !cands[1].Active || cands[1].Path != envPath {
		t.Errorf("env should be active when flag is whitespace: %+v", cands[1])
	}
}
