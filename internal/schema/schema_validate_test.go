package schema

import (
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestValidateNote_Clean(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", "Done")
	front.Set("tags", []any{"go", "cli"})

	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true, Allowed: []string{"WIP", "Done"}},
		"tags":   {Type: "list", Required: true},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_MissingRequired(t *testing.T) {
	front := frontmatter.NewMap()

	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(vs))
	}
	if vs[0].Kind != "missing" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "missing")
	}
}

func TestValidateNote_OptionalAbsent(t *testing.T) {
	front := frontmatter.NewMap()

	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: false},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations for absent optional, got %d", len(vs))
	}
}

func TestValidateNote_WrongType(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", []any{"WIP"})

	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
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

func TestValidateNote_DisallowedValue(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", "wip")

	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true, Allowed: []string{"WIP", "Done", "Archived"}},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
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

	schema := map[string]config.PropertySchema{
		"status":  {Type: "string", Required: true},
		"tags":    {Type: "list", Required: true},
		"created": {Type: "date", Required: true},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 3 {
		t.Errorf("expected 3 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_WikilinkAllowed(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("category", "[[Work]]")

	schema := map[string]config.PropertySchema{
		"category": {Type: "string", Allowed: []string{"Work", "Personal"}},
	}

	vs := ValidateNote(&front, "note.md", schema, []string{"category"})
	if len(vs) != 0 {
		t.Errorf("expected 0 violations for valid wikilink, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_StableOrder(t *testing.T) {
	front := frontmatter.NewMap()

	schema := map[string]config.PropertySchema{
		"z_prop": {Type: "string", Required: true},
		"a_prop": {Type: "string", Required: true},
		"m_prop": {Type: "string", Required: true},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 3 {
		t.Fatalf("expected 3 violations, got %d", len(vs))
	}
	if vs[0].Property != "a_prop" || vs[1].Property != "m_prop" || vs[2].Property != "z_prop" {
		t.Errorf("violations not sorted: %s, %s, %s", vs[0].Property, vs[1].Property, vs[2].Property)
	}
}

func TestValidateNote_NilFront(t *testing.T) {
	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true},
	}

	vs := ValidateNote(nil, "note.md", schema, nil)
	if vs != nil {
		t.Errorf("expected nil for nil front, got %+v", vs)
	}
}

func TestValidateNote_IntAllowedEnforced(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("priority", 99)

	schema := map[string]config.PropertySchema{
		"priority": {Type: "int", Allowed: []string{"1", "2", "3"}},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "value" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "value")
	}
}

func TestValidateNote_IntAllowedPasses(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("priority", 2)

	schema := map[string]config.PropertySchema{
		"priority": {Type: "int", Allowed: []string{"1", "2", "3"}},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_BoolAllowedEnforced(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("draft", true)

	schema := map[string]config.PropertySchema{
		"draft": {Type: "bool", Allowed: []string{"false"}},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "value" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "value")
	}
}

func TestValidateNote_NilValue(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", nil)

	schema := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "type" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "type")
	}
	if vs[0].Detail != "expected string, got nil" {
		t.Errorf("detail = %q, want %q", vs[0].Detail, "expected string, got nil")
	}
}

func TestValidateNote_DateRejectsFullTimestamp(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("created", time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC))

	schema := map[string]config.PropertySchema{
		"created": {Type: "date", Allowed: []string{"2024-01-01"}},
	}

	vs := ValidateNote(&front, "note.md", schema, nil)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "type" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "type")
	}
	if vs[0].Detail != "expected date, got datetime" {
		t.Errorf("detail = %q, want %q", vs[0].Detail, "expected date, got datetime")
	}
}
