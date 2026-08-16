package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_PropertiesParseFromTOML(t *testing.T) {
	cfg, err := loadTOML(t, `[frontmatter.properties.status]
type = "string"
required = true
allowed = ["WIP", "Done", "Archived"]

[frontmatter.properties.tags]
type = "list"
items = "string"
required = true

[frontmatter.properties.created]
type = "date"

[frontmatter.properties.updated]
type = "datetime"

[frontmatter.properties.parent]
type = "wikilink"
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Properties) != 5 {
		t.Fatalf("expected 5 property definitions, got %d", len(cfg.Frontmatter.Properties))
	}
	status := cfg.Frontmatter.Properties["status"]
	if status.Type != KindString || !status.Required || len(status.Allowed) != 3 {
		t.Errorf("status definition: %+v", status)
	}
	tags := cfg.Frontmatter.Properties["tags"]
	if tags.Type != KindList || tags.Items != KindString || !tags.Required {
		t.Errorf("tags definition: %+v", tags)
	}
	created := cfg.Frontmatter.Properties["created"]
	if created.Type != KindDate || created.Required {
		t.Errorf("created definition: %+v", created)
	}
	if cfg.Frontmatter.Properties["updated"].Type != KindDatetime {
		t.Errorf("updated definition: %+v", cfg.Frontmatter.Properties["updated"])
	}
	if cfg.Frontmatter.Properties["parent"].Type != KindWikilink {
		t.Errorf("parent definition: %+v", cfg.Frontmatter.Properties["parent"])
	}
}

func TestLoad_PropertyKindsAllAccepted(t *testing.T) {
	for _, kind := range ScalarKinds {
		t.Run(string(kind), func(t *testing.T) {
			cfg, err := loadTOML(t, `[frontmatter.properties.prop]
type = "`+string(kind)+`"
`)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Frontmatter.Properties["prop"].Type; got != kind {
				t.Fatalf("type = %q, want %q", got, kind)
			}
		})
	}
}

func TestLoad_ListItemKindsAllAccepted(t *testing.T) {
	for _, kind := range ScalarKinds {
		t.Run(string(kind), func(t *testing.T) {
			cfg, err := loadTOML(t, `[frontmatter.properties.prop]
type  = "list"
items = "`+string(kind)+`"
`)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			def := cfg.Frontmatter.Properties["prop"]
			if def.Type != KindList || def.Items != kind {
				t.Fatalf("definition = %+v, want list of %q", def, kind)
			}
			if !def.IsList() {
				t.Error("IsList() = false, want true")
			}
			if def.ScalarKind() != kind {
				t.Errorf("ScalarKind() = %q, want %q", def.ScalarKind(), kind)
			}
		})
	}
}

func TestLoad_UnknownTypeRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.rating]
type = "number"
`)
	if err == nil {
		t.Fatal("expected error for unknown property type")
	}
	if !strings.Contains(err.Error(), `unknown type "number"`) {
		t.Fatalf("error should mention unknown type: %v", err)
	}
}

func TestLoad_TypeWhitespaceAndCaseNormalized(t *testing.T) {
	cfg, err := loadTOML(t, `[frontmatter.properties.rating]
type = " String "
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Frontmatter.Properties["rating"].Type; got != KindString {
		t.Fatalf("type should be normalized to lower-case trimmed value, got %q", got)
	}
}

func TestLoad_ItemsWhitespaceAndCaseNormalized(t *testing.T) {
	cfg, err := loadTOML(t, `[frontmatter.properties.tags]
type  = "LIST"
items = " Wikilink "
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := cfg.Frontmatter.Properties["tags"]
	if def.Type != KindList || def.Items != KindWikilink {
		t.Fatalf("definition = %+v, want list of wikilink", def)
	}
}

func TestLoad_EmptyTypeRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.status]
required = true
`)
	if err == nil {
		t.Fatal("expected error for empty property type")
	}
	if !strings.Contains(err.Error(), "type is required") {
		t.Fatalf("error should mention type is required: %v", err)
	}
}

func TestLoad_ListWithoutItemsRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.tags]
type = "list"
`)
	if err == nil {
		t.Fatal("expected error for list without items")
	}
	if !strings.Contains(err.Error(), "list type requires items") {
		t.Fatalf("error should mention required items: %v", err)
	}
}

func TestLoad_ListWithUnknownItemsRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.tags]
type  = "list"
items = "number"
`)
	if err == nil {
		t.Fatal("expected error for unknown items kind")
	}
	if !strings.Contains(err.Error(), `unknown items kind "number"`) {
		t.Fatalf("error should mention unknown items kind: %v", err)
	}
}

func TestLoad_ListItemsMayNotBeList(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.tags]
type  = "list"
items = "list"
`)
	if err == nil {
		t.Fatal("expected error for nested list items")
	}
	if !strings.Contains(err.Error(), `unknown items kind "list"`) {
		t.Fatalf("error should reject nested lists: %v", err)
	}
}

func TestLoad_ItemsOnScalarRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.status]
type  = "string"
items = "string"
`)
	if err == nil {
		t.Fatal("expected error for items on a scalar property")
	}
	if !strings.Contains(err.Error(), `items is only valid when type = "list"`) {
		t.Fatalf("error should mention items misuse: %v", err)
	}
}

func TestLoad_EmptyAllowedValueRejected(t *testing.T) {
	_, err := loadTOML(t, `[frontmatter.properties.status]
type    = "string"
allowed = ["WIP", "  "]
`)
	if err == nil {
		t.Fatal("expected error for whitespace-only allowed value")
	}
	if !strings.Contains(err.Error(), "allowed[1] must not be empty or whitespace") {
		t.Fatalf("error should mention the offending allowed entry: %v", err)
	}
}

func TestLoad_AllowedOnDateAccepted(t *testing.T) {
	cfg, err := loadTOML(t, `[frontmatter.properties.created]
type = "date"
allowed = ["2024-01-01"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Frontmatter.Properties["created"].Allowed; len(got) != 1 || got[0] != "2024-01-01" {
		t.Fatalf("allowed values not preserved: %v", got)
	}
}

func TestLoad_AllowedOnStringAccepted(t *testing.T) {
	cfg, err := loadTOML(t, `[frontmatter.properties.status]
type = "string"
allowed = ["WIP", "Done"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Properties["status"].Allowed) != 2 {
		t.Errorf("expected 2 allowed values, got %d", len(cfg.Frontmatter.Properties["status"].Allowed))
	}
}

func TestLoad_PropertiesEmptyIsValid(t *testing.T) {
	cfg, err := loadTOML(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Properties) != 0 {
		t.Errorf("expected no property definitions after defaults, got %d entries", len(cfg.Frontmatter.Properties))
	}
}

func TestFrontmatterConfig_Accessors(t *testing.T) {
	cfg, err := loadTOML(t, `[frontmatter.properties.categories]
type  = "list"
items = "wikilink"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[frontmatter.properties.parent]
type = "wikilink"
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	fm := cfg.Frontmatter
	if !fm.IsList("categories") || !fm.IsWikilink("categories") {
		t.Error("categories should be a wikilink list")
	}
	if !fm.IsList("tags") || fm.IsWikilink("tags") {
		t.Error("tags should be a non-wikilink list")
	}
	if fm.IsList("parent") || !fm.IsWikilink("parent") {
		t.Error("parent should be a scalar wikilink")
	}
	if fm.IsList("undeclared") || fm.IsWikilink("undeclared") {
		t.Error("undeclared properties must have no list or wikilink semantics")
	}
	if _, ok := fm.Definition("undeclared"); ok {
		t.Error("Definition should report undeclared properties as absent")
	}

	names := fm.PropertyNames()
	want := []string{"categories", "parent", "tags"}
	if len(names) != len(want) {
		t.Fatalf("PropertyNames() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("PropertyNames() = %v, want %v (sorted)", names, want)
		}
	}
}

func TestPropertyDefinition_Summary(t *testing.T) {
	tests := []struct {
		def  PropertyDefinition
		want string
	}{
		{PropertyDefinition{Type: KindString}, "type: string"},
		{PropertyDefinition{Type: KindList, Items: KindWikilink}, "type: list of wikilink"},
		{
			PropertyDefinition{Type: KindString, Allowed: []string{"WIP", "Done"}},
			"type: string, allowed: WIP, Done",
		},
	}
	for _, tt := range tests {
		if got := tt.def.Summary(); got != tt.want {
			t.Errorf("Summary() = %q, want %q", got, tt.want)
		}
	}
}

func TestLoad_SourcePathSetWhenFileLoaded(t *testing.T) {
	root := newVault(t, "")

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(root, FileName)
	if cfg.SourcePath != want {
		t.Errorf("SourcePath: got %q, want %q", cfg.SourcePath, want)
	}
}
