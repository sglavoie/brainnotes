package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_NewRoutesAbsolutePathRejected verifies that absolute paths in
// [new.routes] are rejected. An absolute path would cause `bn new` to write
// outside the vault, escaping the configured root.
func TestLoad_NewRoutesAbsolutePathRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = \"/tmp/Books\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatalf("expected error for absolute path in [new.routes]")
	}
}

// TestLoad_NewRoutesWindowsAbsolutePathRejected verifies that Windows-style
// absolute paths are rejected even when tests run on a Unix host. Without this,
// a route such as "C:\Books" can pass validation despite not being
// vault-relative on Windows.
func TestLoad_NewRoutesWindowsAbsolutePathRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{`C:\Books`, "D:/Notes"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = '"+raw+"'\n")

			_, err := Load("", cfgPath)
			if err == nil {
				t.Fatalf("expected error for Windows absolute path %q in [new.routes]", raw)
			}
		})
	}
}

// TestLoad_NewRoutesParentTraversalRejected verifies that ".." traversal in
// [new.routes] is rejected; it would let `bn new` escape the vault.
func TestLoad_NewRoutesParentTraversalRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\nbook = \"../Escape\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatalf("expected error for parent traversal in [new.routes]")
	}
}

// TestLoad_NewRoutesWhitespaceTemplateNameRejected verifies route keys with
// surrounding whitespace are rejected. Route keys are matched exactly against
// the template name passed to `bn new`, so a key like " book " silently misses
// the intended "book" template.
func TestLoad_NewRoutesWhitespaceTemplateNameRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[new.routes]\n\" book \" = \"References/Books\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatalf("expected error for whitespace-padded template name in [new.routes]")
	}
	if !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("error should mention surrounding whitespace: %v", err)
	}
}

// TestLoad_PropertyListsTrimWhitespace verifies that whitespace around
// property names in list_properties / wikilink_properties is stripped, so
// `list_properties = [" tags "]` actually matches frontmatter key "tags"
// (the comparison in internal/cli/set.go is exact string match).
func TestLoad_PropertyListsTrimWhitespace(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter]\nlist_properties = [\" tags \"]\nwikilink_properties = [\" parent \"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.ListProperties) != 1 || cfg.Frontmatter.ListProperties[0] != "tags" {
		t.Errorf("ListProperties should be trimmed: got %q", cfg.Frontmatter.ListProperties)
	}
	if len(cfg.Frontmatter.WikilinkProperties) != 1 || cfg.Frontmatter.WikilinkProperties[0] != "parent" {
		t.Errorf("WikilinkProperties should be trimmed: got %q", cfg.Frontmatter.WikilinkProperties)
	}
}

// TestLoad_PropertyListsDropEmpty verifies empty / whitespace-only entries
// in property lists are discarded so they don't match every empty key.
func TestLoad_PropertyListsDropEmpty(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter]\nlist_properties = [\"\", \"   \", \"tags\"]\nwikilink_properties = [\"\", \"parent\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range cfg.Frontmatter.ListProperties {
		if strings.TrimSpace(p) == "" {
			t.Errorf("ListProperties should not contain empty entries: %v", cfg.Frontmatter.ListProperties)
		}
	}
	for _, p := range cfg.Frontmatter.WikilinkProperties {
		if strings.TrimSpace(p) == "" {
			t.Errorf("WikilinkProperties should not contain empty entries: %v", cfg.Frontmatter.WikilinkProperties)
		}
	}
}

// TestLoad_PropertyListsDedup verifies duplicate property names are
// collapsed so callers iterating the list don't double-process keys.
func TestLoad_PropertyListsDedup(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter]\nlist_properties = [\"tags\", \"tags\", \"topics\"]\nwikilink_properties = [\"parent\", \"parent\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	count := 0
	for _, p := range cfg.Frontmatter.ListProperties {
		if p == "tags" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ListProperties should dedup 'tags': got %v", cfg.Frontmatter.ListProperties)
	}
	count = 0
	for _, p := range cfg.Frontmatter.WikilinkProperties {
		if p == "parent" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("WikilinkProperties should dedup 'parent': got %v", cfg.Frontmatter.WikilinkProperties)
	}
}

// TestLoad_SchemaEmptyPropertyNameRejected verifies an empty key in
// [frontmatter.schema.""] is rejected; an empty property name cannot match
// any real frontmatter key and silently swallows the user's intent.
func TestLoad_SchemaEmptyPropertyNameRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter.schema.\"\"]\ntype = \"string\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for empty schema property name")
	}
}

// TestLoad_SchemaWhitespacePropertyNameRejected verifies quoted schema keys
// with surrounding whitespace are rejected. Schema keys are matched exactly
// against frontmatter keys, so accepting " status " would silently fail to
// validate the user's intended "status" property.
func TestLoad_SchemaWhitespacePropertyNameRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter.schema.\" status \"]\ntype = \"string\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for whitespace-padded schema property name")
	}
	if !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("error should mention surrounding whitespace: %v", err)
	}
}

// TestLoad_TemplatesDirWithNUL verifies a NUL byte in templates.dir is
// rejected — it would otherwise pass through to filepath.Walk and either
// panic on some platforms or silently fail to match paths.
func TestLoad_TemplatesDirWithNUL(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// Use literal string ('...') so the NUL byte is preserved verbatim.
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \"Tem\x00plates\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for NUL byte in templates.dir")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Fatalf("expected control-character error, got: %v", err)
	}
}

func TestLoad_TemplatesDirWithControlCharacter(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \"Tem\\tplates\"\n")

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for control character in templates.dir")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Fatalf("expected control-character error, got: %v", err)
	}
}

// TestLoad_VaultSymlinkResolvesToDirectory verifies that a symlink to a
// directory is accepted as the vault path (os.Stat follows symlinks).
func TestLoad_VaultSymlinkResolvesToDirectory(t *testing.T) {
	resetEnv(t)
	realVault := t.TempDir()

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "vault-link")
	if err := os.Symlink(realVault, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	cfg, err := Load(linkPath, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != linkPath {
		t.Errorf("vault should keep symlink path: got %q, want %q", cfg.Vault, linkPath)
	}
}

// TestLoad_OpenEditorTrimsWhitespace verifies surrounding whitespace in
// [open].editor is stripped. A value like "  " would otherwise be treated
// as set, shadowing the $EDITOR fallback in `bn open` and producing an
// opaque exec error when run.
func TestLoad_OpenEditorTrimsWhitespace(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[open]\neditor = \"  nvim  \"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Open.Editor != "nvim" {
		t.Errorf("Open.Editor should be trimmed: got %q, want %q", cfg.Open.Editor, "nvim")
	}
}

// TestLoad_OpenEditorWhitespaceOnlyBecomesEmpty verifies a whitespace-only
// editor value collapses to empty, so the `bn open` fallback to $EDITOR
// continues to fire rather than dispatching a blank command.
func TestLoad_OpenEditorWhitespaceOnlyBecomesEmpty(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[open]\neditor = \"   \"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Open.Editor != "" {
		t.Errorf("whitespace-only Open.Editor should be empty, got %q", cfg.Open.Editor)
	}
}

// TestLoad_OpenEditorUnsetStaysEmpty confirms an omitted [open].editor key
// remains empty after defaults are applied (the $EDITOR fallback path).
func TestLoad_OpenEditorUnsetStaysEmpty(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	cfg, err := Load(vault, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Open.Editor != "" {
		t.Errorf("unset Open.Editor should be empty, got %q", cfg.Open.Editor)
	}
}

// TestDefaultTemplate_MentionsOpenSection ensures the starter template
// documents the [open].editor key that downstream commands rely on.
func TestDefaultTemplate_MentionsOpenSection(t *testing.T) {
	tmpl := DefaultTemplate()
	for _, want := range []string{"[open]", "editor"} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing %q", want)
		}
	}
}
