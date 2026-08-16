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
	_, err := loadTOML(t, "[new.routes]\nbook = \"/tmp/Books\"\n")
	if err == nil {
		t.Fatalf("expected error for absolute path in [new.routes]")
	}
}

// TestLoad_NewRoutesWindowsAbsolutePathRejected verifies that Windows-style
// absolute paths are rejected even when tests run on a Unix host. Without this,
// a route such as "C:\Books" can pass validation despite not being
// vault-relative on Windows.
func TestLoad_NewRoutesWindowsAbsolutePathRejected(t *testing.T) {
	for _, raw := range []string{`C:\Books`, "D:/Notes"} {
		t.Run(raw, func(t *testing.T) {
			_, err := loadTOML(t, "[new.routes]\nbook = '"+raw+"'\n")
			if err == nil {
				t.Fatalf("expected error for Windows absolute path %q in [new.routes]", raw)
			}
		})
	}
}

// TestLoad_NewRoutesParentTraversalRejected verifies that ".." traversal in
// [new.routes] is rejected; it would let `bn new` escape the vault.
func TestLoad_NewRoutesParentTraversalRejected(t *testing.T) {
	_, err := loadTOML(t, "[new.routes]\nbook = \"../Escape\"\n")
	if err == nil {
		t.Fatalf("expected error for parent traversal in [new.routes]")
	}
}

// TestLoad_NewRoutesWhitespaceTemplateNameRejected verifies route keys with
// surrounding whitespace are rejected. Route keys are matched exactly against
// the template name passed to `bn new`, so a key like " book " silently misses
// the intended "book" template.
func TestLoad_NewRoutesWhitespaceTemplateNameRejected(t *testing.T) {
	_, err := loadTOML(t, "[new.routes]\n\" book \" = \"References/Books\"\n")
	if err == nil {
		t.Fatalf("expected error for whitespace-padded template name in [new.routes]")
	}
	if !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("error should mention surrounding whitespace: %v", err)
	}
}

// TestLoad_RelatedMetadataPropertiesTrimWhitespace verifies that whitespace
// around property names in [related].metadata_properties is stripped, so
// `metadata_properties = [" tags "]` actually matches frontmatter key "tags"
// (the comparison downstream is an exact string match).
func TestLoad_RelatedMetadataPropertiesTrimWhitespace(t *testing.T) {
	cfg, err := loadTOML(t, "[related]\nmetadata_properties = [\" tags \"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Related.MetadataProperties
	if len(got) != 1 || got[0] != "tags" {
		t.Errorf("metadata_properties should be trimmed: got %q", got)
	}
}

// TestLoad_RelatedMetadataPropertiesDropEmpty verifies empty / whitespace-only
// entries are discarded so they don't match every empty key.
func TestLoad_RelatedMetadataPropertiesDropEmpty(t *testing.T) {
	cfg, err := loadTOML(t, "[related]\nmetadata_properties = [\"\", \"   \", \"tags\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range cfg.Related.MetadataProperties {
		if strings.TrimSpace(p) == "" {
			t.Errorf("metadata_properties should not contain empty entries: %v", cfg.Related.MetadataProperties)
		}
	}
}

// TestLoad_RelatedMetadataPropertiesDedup verifies duplicate property names are
// collapsed so callers iterating the list don't double-process keys.
func TestLoad_RelatedMetadataPropertiesDedup(t *testing.T) {
	cfg, err := loadTOML(t, "[related]\nmetadata_properties = [\"tags\", \"tags\", \"topics\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	count := 0
	for _, p := range cfg.Related.MetadataProperties {
		if p == "tags" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("metadata_properties should dedup 'tags': got %v", cfg.Related.MetadataProperties)
	}
}

// TestLoad_EmptyPropertyNameRejected verifies an empty key in
// [frontmatter.properties.""] is rejected; an empty property name cannot match
// any real frontmatter key and silently swallows the user's intent.
func TestLoad_EmptyPropertyNameRejected(t *testing.T) {
	_, err := loadTOML(t, "[frontmatter.properties.\"\"]\ntype = \"string\"\n")
	if err == nil {
		t.Fatal("expected error for empty property name")
	}
}

// TestLoad_WhitespacePropertyNameRejected verifies quoted property keys with
// surrounding whitespace are rejected. Property keys are matched exactly
// against frontmatter keys, so accepting " status " would silently fail to
// validate the user's intended "status" property.
func TestLoad_WhitespacePropertyNameRejected(t *testing.T) {
	_, err := loadTOML(t, "[frontmatter.properties.\" status \"]\ntype = \"string\"\n")
	if err == nil {
		t.Fatal("expected error for whitespace-padded property name")
	}
	if !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("error should mention surrounding whitespace: %v", err)
	}
}

// TestLoad_TemplatesDirWithNUL verifies a NUL byte in templates.dir is
// rejected — it would otherwise pass through to filepath.Walk and either
// panic on some platforms or silently fail to match paths.
func TestLoad_TemplatesDirWithNUL(t *testing.T) {
	// Use a Go escape so the NUL byte is written to the TOML verbatim.
	_, err := loadTOML(t, "[templates]\ndir = \"Tem\x00plates\"\n")
	if err == nil {
		t.Fatal("expected error for NUL byte in templates.dir")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Fatalf("expected control-character error, got: %v", err)
	}
}

func TestLoad_TemplatesDirWithControlCharacter(t *testing.T) {
	_, err := loadTOML(t, "[templates]\ndir = \"Tem\\tplates\"\n")
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
	realVault := newVault(t, "")

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "vault-link")
	if err := os.Symlink(realVault, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	cfg, err := loadVault(t, linkPath)
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
	cfg, err := loadTOML(t, "[open]\neditor = \"  nvim  \"\n")
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
	cfg, err := loadTOML(t, "[open]\neditor = \"   \"\n")
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
	cfg, err := loadVault(t, newVault(t, ""))
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
