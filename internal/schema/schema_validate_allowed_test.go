package schema

import (
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestValidateNote_IntAllowedEnforced(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("priority", 99)

	props := map[string]config.PropertyDefinition{
		"priority": {Type: config.KindInt, Allowed: []string{"1", "2", "3"}},
	}

	vs := ValidateNote(&front, "note.md", props)
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

	props := map[string]config.PropertyDefinition{
		"priority": {Type: config.KindInt, Allowed: []string{"1", "2", "3"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_FloatAllowedWholeNumberPasses(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("rating", 3.0)

	props := map[string]config.PropertyDefinition{
		"rating": {Type: config.KindFloat, Allowed: []string{"3.0", "4.0"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestValidateNote_FloatAllowedWholeNumberViolationDetail(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("rating", 3.0)

	props := map[string]config.PropertyDefinition{
		"rating": {Type: config.KindFloat, Allowed: []string{"4.0"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "value" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "value")
	}
	if vs[0].Detail != `invalid value "3.0" (allowed: 4.0)` {
		t.Errorf("detail = %q, want %q", vs[0].Detail, `invalid value "3.0" (allowed: 4.0)`)
	}
}

func TestValidateNote_BoolAllowedEnforced(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("draft", true)

	props := map[string]config.PropertyDefinition{
		"draft": {Type: config.KindBool, Allowed: []string{"false"}},
	}

	vs := ValidateNote(&front, "note.md", props)
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

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true},
	}

	vs := ValidateNote(&front, "note.md", props)
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

func TestValidateNote_StructuredValueNeverStringified(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("status", map[string]any{"nested": "value"})

	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Allowed: []string{"Done"}},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].Kind != "type" {
		t.Errorf("kind = %q, want %q", vs[0].Kind, "type")
	}
	if vs[0].Detail != "expected string, got map" {
		t.Errorf("detail = %q, want %q", vs[0].Detail, "expected string, got map")
	}
}

func TestValidateNote_DateRejectsFullTimestamp(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("created", time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC))

	props := map[string]config.PropertyDefinition{
		"created": {Type: config.KindDate, Allowed: []string{"2024-01-01"}},
	}

	vs := ValidateNote(&front, "note.md", props)
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

func TestValidateNote_DatetimeAcceptsFullTimestamp(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\nupdated: 2025-12-13T09:41:30Z\n---\nbody\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	props := map[string]config.PropertyDefinition{
		"updated": {Type: config.KindDatetime},
	}

	vs := ValidateNote(&doc.Front, "note.md", props)
	if len(vs) != 0 {
		t.Fatalf("expected no violations, got %+v", vs)
	}
}

func TestValidateNote_DatetimeAcceptsNanosecondPrecision(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\nupdated: 2025-12-13T09:41:30.123456789Z\n---\nbody\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	props := map[string]config.PropertyDefinition{
		"updated": {Type: config.KindDatetime},
	}

	vs := ValidateNote(&doc.Front, "note.md", props)
	if len(vs) != 0 {
		t.Fatalf("expected no violations, got %+v", vs)
	}
}

func TestValidateNote_DatetimeRejectsString(t *testing.T) {
	front := frontmatter.NewMap()
	front.Set("updated", "2025-12-13T09:41:30Z")

	props := map[string]config.PropertyDefinition{
		"updated": {Type: config.KindDatetime},
	}

	vs := ValidateNote(&front, "note.md", props)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %+v", vs)
	}
	if vs[0].Detail != "expected datetime, got string" {
		t.Errorf("detail = %q", vs[0].Detail)
	}
}

func TestValidateNote_ParsedUTCMidnightDateTimeRejectedAsDate(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\ncreated: 2025-12-13T00:00:00Z\n---\nbody\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	props := map[string]config.PropertyDefinition{
		"created": {Type: config.KindDate},
	}

	vs := ValidateNote(&doc.Front, "note.md", props)
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

func TestValidateNote_ParsedDateOnlyAcceptedAsDate(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\ncreated: 2025-12-13\n---\nbody\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	props := map[string]config.PropertyDefinition{
		"created": {Type: config.KindDate},
	}

	vs := ValidateNote(&doc.Front, "note.md", props)
	if len(vs) != 0 {
		t.Fatalf("expected no violations, got %+v", vs)
	}
}
