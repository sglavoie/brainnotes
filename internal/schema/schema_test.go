package schema

import (
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestValidType(t *testing.T) {
	valid := []string{"string", "list", "date", "int", "bool", "float"}
	for _, v := range valid {
		if !ValidType(v) {
			t.Errorf("ValidType(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "str", "integer", "boolean", "array", "number"}
	for _, v := range invalid {
		if ValidType(v) {
			t.Errorf("ValidType(%q) = true, want false", v)
		}
	}
}

func TestCheckType(t *testing.T) {
	dateOnly := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTime := time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		value      any
		schemaType string
		want       bool
	}{
		{"hello", "string", true},
		{42, "string", false},
		{[]any{"a", "b"}, "list", true},
		{"not a list", "list", false},
		{dateOnly, "date", true},
		{dateTime, "date", false},
		{"2024-01-01", "date", false},
		{42, "int", true},
		{42.5, "int", false},
		{true, "bool", true},
		{"true", "bool", false},
		{3.14, "float", true},
		{3, "float", false},
		{"x", "unknown", false},
	}
	for _, tt := range tests {
		got := CheckType(tt.value, tt.schemaType)
		if got != tt.want {
			t.Errorf("CheckType(%v, %q) = %v, want %v", tt.value, tt.schemaType, got, tt.want)
		}
	}
}

func TestCheckAllowed_Scalar(t *testing.T) {
	allowed := []string{"WIP", "Done", "Archived"}

	ok, bad := CheckAllowed("WIP", allowed, false)
	if !ok {
		t.Errorf("expected ok for WIP, got offending=%q", bad)
	}

	ok, bad = CheckAllowed("wip", allowed, false)
	if ok {
		t.Error("expected failure for case-sensitive mismatch")
	}
	if bad != "wip" {
		t.Errorf("offending = %q, want %q", bad, "wip")
	}
}

func TestCheckAllowed_List(t *testing.T) {
	allowed := []string{"a", "b", "c"}

	ok, _ := CheckAllowed([]any{"a", "b"}, allowed, false)
	if !ok {
		t.Error("expected ok for valid list")
	}

	ok, bad := CheckAllowed([]any{"a", "d"}, allowed, false)
	if ok {
		t.Error("expected failure for list with invalid element")
	}
	if bad != "d" {
		t.Errorf("offending = %q, want %q", bad, "d")
	}
}

func TestCheckAllowed_Wikilink(t *testing.T) {
	allowed := []string{"Alice", "Bob"}

	ok, _ := CheckAllowed("[[Alice]]", allowed, true)
	if !ok {
		t.Error("expected ok for wikilink-wrapped value")
	}

	ok, bad := CheckAllowed("[[Charlie]]", allowed, true)
	if ok {
		t.Error("expected failure for wikilink not in allowed")
	}
	if bad != "Charlie" {
		t.Errorf("offending = %q, want %q", bad, "Charlie")
	}

	ok, _ = CheckAllowed([]any{"[[Alice]]", "[[Bob]]"}, allowed, true)
	if !ok {
		t.Error("expected ok for wikilink list")
	}

	ok, bad = CheckAllowed([]any{"[[Alice]]", "[[Eve]]"}, allowed, true)
	if ok {
		t.Error("expected failure for wikilink list with invalid element")
	}
	if bad != "Eve" {
		t.Errorf("offending = %q, want %q", bad, "Eve")
	}
}

func TestCheckAllowed_NonStringType(t *testing.T) {
	// Int value whose string representation is in the allowed list.
	ok, _ := CheckAllowed(42, []string{"42"}, false)
	if !ok {
		t.Error("expected ok when int string representation is in allowed list")
	}

	// Int value whose string representation is NOT in the allowed list.
	ok, bad := CheckAllowed(99, []string{"1", "2", "3"}, false)
	if ok {
		t.Error("expected failure when int string representation is not in allowed list")
	}
	if bad != "99" {
		t.Errorf("offending = %q, want %q", bad, "99")
	}

	// Bool value whose string representation is in the allowed list.
	ok, _ = CheckAllowed(true, []string{"true", "false"}, false)
	if !ok {
		t.Error("expected ok when bool string representation is in allowed list")
	}

	// Bool value whose string representation is NOT in the allowed list.
	ok, bad = CheckAllowed(true, []string{"yes", "no"}, false)
	if ok {
		t.Error("expected failure when bool string representation is not in allowed list")
	}
	if bad != "true" {
		t.Errorf("offending = %q, want %q", bad, "true")
	}

	// Float value whose string representation is in the allowed list.
	ok, _ = CheckAllowed(3.14, []string{"3.14", "2.71"}, false)
	if !ok {
		t.Error("expected ok when float string representation is in allowed list")
	}

	// Float value whose string representation is NOT in the allowed list.
	ok, bad = CheckAllowed(9.99, []string{"1.5", "2.5"}, false)
	if ok {
		t.Error("expected failure when float string representation is not in allowed list")
	}
	if bad != "9.99" {
		t.Errorf("offending = %q, want %q", bad, "9.99")
	}
}

func TestCheckAllowed_DateOnlyTime(t *testing.T) {
	dateOnly := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ok, _ := CheckAllowed(dateOnly, []string{"2024-01-01"}, false)
	if !ok {
		t.Error("expected ok when date-only string representation is in allowed list")
	}

	dateTime := time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)
	ok, _ = CheckAllowed(dateTime, []string{"2024-01-01"}, false)
	if ok {
		t.Error("expected failure when full timestamp shares a day with an allowed date")
	}
}

func TestCheckAllowed_NilValue(t *testing.T) {
	allowed := []string{"a", "b"}

	ok, bad := CheckAllowed(nil, allowed, false)
	if ok {
		t.Error("expected failure when value is nil")
	}
	if bad != "<nil>" {
		t.Errorf("offending = %q, want %q", bad, "<nil>")
	}
}

func TestCheckAllowed_MapValue(t *testing.T) {
	allowed := []string{"a", "b"}

	ok, _ := CheckAllowed(map[string]any{"x": 1}, allowed, false)
	if ok {
		t.Error("expected failure when value is a map")
	}
}

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

func TestCheckAllowed_ListNonStringItem(t *testing.T) {
	allowed := []string{"apple", "banana"}

	ok, bad := CheckAllowed([]any{42, "apple"}, allowed, false)
	if ok {
		t.Error("expected failure when list contains non-string item")
	}
	if bad != "42" {
		t.Errorf("offending = %q, want %q", bad, "42")
	}
}

func TestCheckAllowed_ListNonStringItemWikilink(t *testing.T) {
	allowed := []string{"Alice", "Bob"}

	ok, bad := CheckAllowed([]any{true, "[[Alice]]"}, allowed, true)
	if ok {
		t.Error("expected failure when wikilink list contains non-string item")
	}
	if bad != "true" {
		t.Errorf("offending = %q, want %q", bad, "true")
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
