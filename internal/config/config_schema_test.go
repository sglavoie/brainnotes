package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_SchemaParsesFromTOML(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
type = "string"
required = true
allowed = ["WIP", "Done", "Archived"]

[frontmatter.schema.tags]
type = "list"
required = true

[frontmatter.schema.created]
type = "date"
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Schema) != 3 {
		t.Fatalf("expected 3 schema entries, got %d", len(cfg.Frontmatter.Schema))
	}
	s := cfg.Frontmatter.Schema["status"]
	if s.Type != "string" || !s.Required || len(s.Allowed) != 3 {
		t.Errorf("status schema: %+v", s)
	}
	tags := cfg.Frontmatter.Schema["tags"]
	if tags.Type != "list" || !tags.Required {
		t.Errorf("tags schema: %+v", tags)
	}
	created := cfg.Frontmatter.Schema["created"]
	if created.Type != "date" || created.Required {
		t.Errorf("created schema: %+v", created)
	}
}

func TestLoad_SchemaUnknownTypeRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.rating]
type = "number"
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown schema type")
	}
	if !strings.Contains(err.Error(), `unknown type "number"`) {
		t.Fatalf("error should mention unknown type: %v", err)
	}
}

func TestLoad_SchemaTypeWhitespaceAndCaseNormalized(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.rating]
type = " String "
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Frontmatter.Schema["rating"].Type; got != "string" {
		t.Fatalf("schema type should be normalized to lower-case trimmed value, got %q", got)
	}
}

func TestLoad_SchemaEmptyTypeRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
required = true
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for empty schema type")
	}
	if !strings.Contains(err.Error(), "type is required") {
		t.Fatalf("error should mention type is required: %v", err)
	}
}

func TestLoad_SchemaAllowedOnDateAccepted(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.created]
type = "date"
allowed = ["2024-01-01"]
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Frontmatter.Schema["created"].Allowed; len(got) != 1 || got[0] != "2024-01-01" {
		t.Fatalf("allowed values not preserved: %v", got)
	}
}

func TestLoad_SchemaAllowedOnStringAccepted(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
type = "string"
allowed = ["WIP", "Done"]
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Schema["status"].Allowed) != 2 {
		t.Errorf("expected 2 allowed values, got %d", len(cfg.Frontmatter.Schema["status"].Allowed))
	}
}

func TestLoad_SchemaEmptyIsValid(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Schema) != 0 {
		t.Errorf("expected empty schema after defaults, got %d entries", len(cfg.Frontmatter.Schema))
	}
}

func TestLoad_SourcePathSetWhenFileLoaded(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourcePath != cfgPath {
		t.Errorf("SourcePath: got %q, want %q", cfg.SourcePath, cfgPath)
	}
}

func TestLoad_SourcePathEmptyWhenNoConfigFileExists(t *testing.T) {
	// When no config file exists on disk (implicit default search misses),
	// SourcePath should be empty rather than pointing at a non-existent file.
	resetEnv(t)
	vault := t.TempDir()

	cfg, err := Load(vault, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourcePath != "" {
		t.Errorf("SourcePath should be empty when no config file was loaded, got %q", cfg.SourcePath)
	}
}

func TestLoad_SchemaAllowedOnScalarTypesAccepted(t *testing.T) {
	tests := []struct {
		typ     string
		allowed string
	}{
		{"int", "1"},
		{"bool", "true"},
		{"float", "3.14"},
		{"date", "2024-01-01"},
	}
	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			resetEnv(t)
			vault := t.TempDir()
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.prop]
type = "`+tc.typ+`"
allowed = ["`+tc.allowed+`"]
`)
			cfg, err := Load("", cfgPath)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Frontmatter.Schema["prop"].Allowed; len(got) != 1 || got[0] != tc.allowed {
				t.Fatalf("allowed values not preserved: %v", got)
			}
		})
	}
}
