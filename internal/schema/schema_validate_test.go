package schema

import (
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestValidateNote_Clean(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", "Done")
	front.Set("tags", []any{"go", "cli"})

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true, Allowed: []string{"WIP", "Done"}},
		"tags":   {Type: config.KindList, Items: config.KindString, Required: true},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_MissingRequired(t *testing.T) {
	front := frontmatter.NewMap()

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(vs))
	}
	if vs[0].Kind != "missing" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "missing")
	}
	if vs[0].Detail != "type: string" {
		t.Errorf("detail = %q, want %q", vs[0].Detail, "type: string")
	}
}

func TestValidateNote_OptionalAbsent(t *testing.T) {
	front := frontmatter.NewMap()

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: false},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations for absent optional, got %d", len(vs))
	}
}

func TestValidateNote_WrongType(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", []any{"WIP"})

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(vs))
	}
	if vs[0].Kind != "type" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "type")
	}
	if vs[0].Detail != "expected string, got list" {
		t.Errorf("detail = %q", vs[0].Detail)
	}
}

func TestValidateNote_ScalarWhereListExpected(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("tags", "go")

	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Detail != "expected list of string, got string" {
		t.Errorf("detail = %q", vs[0].Detail)
	}
}

func TestValidateNote_ListItemKindMismatchReportsIndex(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("tags", []any{"go", 42})

	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Detail != "expected list of string, got int at index 1" {
		t.Errorf("detail = %q", vs[0].Detail)
	}
}

func TestValidateNote_ListOfWikilinks(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("categories", []any{"[[Work]]", "[[Personal]]"})

	props := map[string]config.PropertyDefinition{
		"categories": {Type: config.KindList, Items: config.KindWikilink, Allowed: []string{"Work", "Personal"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_ListOfWikilinksDisallowedValue(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("categories", []any{"[[Work]]", "[[Unknown]]"})

	props := map[string]config.PropertyDefinition{
		"categories": {Type: config.KindList, Items: config.KindWikilink, Allowed: []string{"Work", "Personal"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "value" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "value")
	}
	if vs[0].Detail != `invalid value "Unknown" (allowed: Work, Personal)` {
		t.Errorf("detail = %q", vs[0].Detail)
	}
}

func TestValidateNote_DisallowedValue(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", "wip")

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true, Allowed: []string{"WIP", "Done", "Archived"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(vs))
	}
	if vs[0].Kind != "value" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "value")
	}
}

func TestValidateNote_MultipleViolations(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", 42)

	props := map[string]config.PropertyDefinition{
		"status":  {Type: config.KindString, Required: true},
		"tags":    {Type: config.KindList, Items: config.KindString, Required: true},
		"created": {Type: config.KindDate, Required: true},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 3 {
		t.Errorf("expected 3 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_WikilinkAllowed(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("category", "[[Work]]")

	props := map[string]config.PropertyDefinition{
		"category": {Type: config.KindWikilink, Allowed: []string{"Work", "Personal"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations for valid wikilink, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_WikilinkAllowedHeadingAndAlias(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("category", "[[Work#Today|Focus]]")

	props := map[string]config.PropertyDefinition{
		"category": {Type: config.KindWikilink, Allowed: []string{"Work", "Personal"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations for wikilink target match, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_StableOrder(t *testing.T) {
	front := frontmatter.NewMap()

	props := map[string]config.PropertyDefinition{
		"z_prop": {Type: config.KindString, Required: true},
		"a_prop": {Type: config.KindString, Required: true},
		"m_prop": {Type: config.KindString, Required: true},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 3 {
		t.Fatalf("expected 3 violations, got %d", len(vs))
	}
	if vs[0].Property != "a_prop" || vs[1].Property != "m_prop" || vs[2].Property != "z_prop" {
		t.Errorf("violations not sorted: %s, %s, %s", vs[0].Property, vs[1].Property, vs[2].Property)
	}
}

func TestValidateNote_NilFront(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true},
	}

	vs := ValidateNote(nil, "note.md", props)
	if vs != nil {
		t.Errorf("expected nil for nil front, got %+v", vs)
	}
}

func TestValidateNote_UnknownPropertyIgnored(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", "Done")
	front.Set("undeclared", []any{"anything", 42})

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected undeclared properties to be left alone, got %+v", vs)
	}
}
