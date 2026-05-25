package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestPlanRepairs_OptionalAbsent_NoAction(t *testing.T) {
	front := buildFront("other", "value")
	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Required: false},
	}

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips, got %+v", result.Skips)
	}
}

func TestPlanRepairs_AlreadyValid_NoAction(t *testing.T) {
	front := buildFront("status", "WIP", "tags", []any{"go", "cli"})
	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Allowed: []string{"WIP", "Done"}},
		"tags":   {Type: "list"},
	}

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for valid note, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips for valid note, got %+v", result.Skips)
	}
}

func TestPlanRepairs_EmptyFrontmatter(t *testing.T) {
	front := &frontmatter.Map{}
	result := PlanRepairs(front, nil, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips, got %+v", result.Skips)
	}
}

func TestPlanRepairs_NoSchema_NoWikilink(t *testing.T) {
	front := buildFront("status", "wip", "count", 42)
	result := PlanRepairs(front, nil, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions without schema or wikilink config, got %+v", result.Actions)
	}
}

// --- Combined repair categories ---

func TestPlanRepairs_CanonicalizeThenDedup(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"tags": {Type: "list", Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{"go", "Go", "cli"})

	result := PlanRepairs(front, sch, nil)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "canonicalize,list_dedup" {
		t.Errorf("kind = %q, want %q", act.Kind, "canonicalize,list_dedup")
	}
	want := []any{"Go", "CLI"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_WikilinkWrapThenDedup(t *testing.T) {
	front := buildFront("categories", []any{"Books", "[[Books]]", "Papers"})

	result := PlanRepairs(front, nil, []string{"categories"})
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "wikilink_wrap,list_dedup" {
		t.Errorf("kind = %q, want %q", act.Kind, "wikilink_wrap,list_dedup")
	}
	want := []any{"[[Books]]", "[[Papers]]"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_TypeCoerceThenCanonicalizeString(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"draft": {Type: "string", Allowed: []string{"True", "False"}},
	}
	front := buildFront("draft", true)

	result := PlanRepairs(front, sch, nil)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "type_coerce,canonicalize" {
		t.Errorf("kind = %q, want %q", act.Kind, "type_coerce,canonicalize")
	}
	if act.NewValue != "True" {
		t.Errorf("new value = %v, want %q", act.NewValue, "True")
	}
}

func TestPlanRepairs_TypeCoerceWholeNumberFloatToString(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"rating": {Type: "string", Allowed: []string{"3.0", "4.0"}},
	}
	front := buildFront("rating", 3.0)

	result := PlanRepairs(front, sch, nil)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "type_coerce" {
		t.Errorf("kind = %q, want %q", act.Kind, "type_coerce")
	}
	if act.NewValue != "3.0" {
		t.Errorf("new value = %v, want %q", act.NewValue, "3.0")
	}
}

func TestPlanRepairs_TypeCoercePlusValueSkip(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Allowed: []string{"WIP", "Done"}},
	}
	front := buildFront("status", 42)

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action (type_coerce), got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "type_coerce" {
		t.Errorf("action kind = %q, want %q", result.Actions[0].Kind, "type_coerce")
	}
	if result.Actions[0].NewValue != "42" {
		t.Errorf("new value = %v, want %q", result.Actions[0].NewValue, "42")
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
}

// --- Multiple properties ---

func TestPlanRepairs_MultipleProperties(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"created": {Type: "date"},
		"status":  {Type: "string", Required: true, Allowed: []string{"WIP", "Done"}},
		"tags":    {Type: "list"},
	}
	front := buildFront(
		"status", "wip",
		"created", "2024-06-15",
		"tags", []any{"go", "go", "cli"},
	)

	result := PlanRepairs(front, sch, nil)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d: %+v", len(result.Actions), result.Actions)
	}

	acts := make(map[string]RepairAction)
	for _, a := range result.Actions {
		acts[a.Property] = a
	}
	if acts["created"].Kind != "type_coerce" {
		t.Errorf("created kind = %q, want type_coerce", acts["created"].Kind)
	}
	if acts["status"].Kind != "canonicalize" {
		t.Errorf("status kind = %q, want canonicalize", acts["status"].Kind)
	}
	if acts["tags"].Kind != "list_dedup" {
		t.Errorf("tags kind = %q, want list_dedup", acts["tags"].Kind)
	}
}

func TestPlanRepairs_NonSchemaPropertyGetsStructuralRepair(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"status": {Type: "string"},
	}
	front := buildFront("status", "ok", "parent", "Books")

	result := PlanRepairs(front, sch, []string{"parent"})
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Property != "parent" {
		t.Errorf("property = %q, want %q", result.Actions[0].Property, "parent")
	}
	if result.Actions[0].Kind != "wikilink_wrap" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "wikilink_wrap")
	}
}

// --- Deterministic ordering ---

func TestPlanRepairs_SchemaKeysAreSorted(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"z_prop": {Type: "string"},
		"a_prop": {Type: "string"},
		"m_prop": {Type: "string"},
	}
	front := buildFront("z_prop", 1, "a_prop", 2, "m_prop", 3)

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(result.Actions))
	}
	if result.Actions[0].Property != "a_prop" {
		t.Errorf("first action property = %q, want a_prop", result.Actions[0].Property)
	}
	if result.Actions[1].Property != "m_prop" {
		t.Errorf("second action property = %q, want m_prop", result.Actions[1].Property)
	}
	if result.Actions[2].Property != "z_prop" {
		t.Errorf("third action property = %q, want z_prop", result.Actions[2].Property)
	}
}

func TestPlanRepairs_NonSchemaKeysAreSorted(t *testing.T) {
	front := buildFront(
		"z_prop", []any{"z", "z"},
		"a_prop", "Bare",
		"m_prop", []any{"m", "m"},
	)

	result := PlanRepairs(front, nil, []string{"a_prop"})
	if len(result.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Property != "a_prop" {
		t.Errorf("first action property = %q, want a_prop", result.Actions[0].Property)
	}
	if result.Actions[1].Property != "m_prop" {
		t.Errorf("second action property = %q, want m_prop", result.Actions[1].Property)
	}
	if result.Actions[2].Property != "z_prop" {
		t.Errorf("third action property = %q, want z_prop", result.Actions[2].Property)
	}
}

// --- Edge cases ---

func TestPlanRepairs_NilSchemaMap(t *testing.T) {
	front := buildFront("tags", []any{"a", "a"})
	result := PlanRepairs(front, nil, nil)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 list_dedup action, got %d", len(result.Actions))
	}
	if result.Actions[0].Kind != "list_dedup" {
		t.Errorf("kind = %q, want list_dedup", result.Actions[0].Kind)
	}
}

func TestPlanRepairs_TypeUnconvertible_SkipsAllowedCheck(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"status": {Type: "bool", Allowed: []string{"WIP"}},
	}
	front := buildFront("status", "maybe")

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "type_unconvertible" {
		t.Errorf("skip kind = %q, want type_unconvertible", result.Skips[0].Kind)
	}
}

func TestPlanRepairs_TypeUnconvertible_SuppressesListDedup(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"status": {Type: "string"},
	}
	front := buildFront("status", []any{"WIP", "WIP"})

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 0 {
		t.Fatalf("expected no actions after type_unconvertible, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "type_unconvertible" {
		t.Errorf("skip kind = %q, want type_unconvertible", result.Skips[0].Kind)
	}
}

func TestPlanRepairs_TypeUnconvertible_SuppressesWikilinkWrap(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"parent": {Type: "string"},
	}
	front := buildFront("parent", []any{"Books", "[[Papers]]"})

	result := PlanRepairs(front, sch, []string{"parent"})
	if len(result.Actions) != 0 {
		t.Fatalf("expected no actions after type_unconvertible, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "type_unconvertible" {
		t.Errorf("skip kind = %q, want type_unconvertible", result.Skips[0].Kind)
	}
}

func TestPlanRepairs_NilFront(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true},
	}
	result := PlanRepairs(nil, sch, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for nil front, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips for nil front, got %+v", result.Skips)
	}
}
