package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

func TestPlanRepairs_OptionalAbsent_NoAction(t *testing.T) {
	front := buildFront("other", "value")
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: false},
	}

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips, got %+v", result.Skips)
	}
}

func TestPlanRepairs_AlreadyValid_NoAction(t *testing.T) {
	front := buildFront("status", "WIP", "tags", []any{"go", "cli"})
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Allowed: []string{"WIP", "Done"}},
		"tags":   {Type: config.KindList, Items: config.KindString},
	}

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for valid note, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips for valid note, got %+v", result.Skips)
	}
}

func TestPlanRepairs_EmptyFrontmatter(t *testing.T) {
	front := &frontmatter.Map{}
	result := PlanRepairs(front, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips, got %+v", result.Skips)
	}
}

func TestPlanRepairs_NoDefinitions_NoAction(t *testing.T) {
	front := buildFront("status", "wip", "count", 42)
	result := PlanRepairs(front, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions without property definitions, got %+v", result.Actions)
	}
}

// --- Combined repair categories ---

func TestPlanRepairs_CanonicalizeThenDedup(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{"go", "Go", "cli"})

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"categories": {Type: config.KindList, Items: config.KindWikilink},
	}
	front := buildFront("categories", []any{"Books", "[[Books]]", "Papers"})

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"draft": {Type: config.KindString, Allowed: []string{"True", "False"}},
	}
	front := buildFront("draft", true)

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"rating": {Type: config.KindString, Allowed: []string{"3.0", "4.0"}},
	}
	front := buildFront("rating", 3.0)

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Allowed: []string{"WIP", "Done"}},
	}
	front := buildFront("status", 42)

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"created": {Type: config.KindDate},
		"status":  {Type: config.KindString, Required: true, Allowed: []string{"WIP", "Done"}},
		"tags":    {Type: config.KindList, Items: config.KindString},
	}
	front := buildFront(
		"status", "wip",
		"created", "2024-06-15",
		"tags", []any{"go", "go", "cli"},
	)

	result := PlanRepairs(front, props)
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

func TestPlanRepairs_WikilinkDefinitionWrapsBareString(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString},
		"parent": {Type: config.KindWikilink},
	}
	front := buildFront("status", "ok", "parent", "Books")

	result := PlanRepairs(front, props)
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

func TestPlanRepairs_UndefinedPropertyNeverWikilinkWrapped(t *testing.T) {
	front := buildFront("parent", "Books")

	result := PlanRepairs(front, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected undefined property to be left alone, got %+v", result.Actions)
	}
}

// --- Deterministic ordering ---

func TestPlanRepairs_DefinedKeysAreSorted(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"z_prop": {Type: config.KindString},
		"a_prop": {Type: config.KindString},
		"m_prop": {Type: config.KindString},
	}
	front := buildFront("z_prop", 1, "a_prop", 2, "m_prop", 3)

	result := PlanRepairs(front, props)
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

func TestPlanRepairs_UndefinedKeysAreSorted(t *testing.T) {
	front := buildFront(
		"z_prop", []any{"z", "z"},
		"a_prop", []any{"a", "a"},
		"m_prop", []any{"m", "m"},
	)

	result := PlanRepairs(front, nil)
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

func TestPlanRepairs_NilPropertyMap(t *testing.T) {
	front := buildFront("tags", []any{"a", "a"})
	result := PlanRepairs(front, nil)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 list_dedup action, got %d", len(result.Actions))
	}
	if result.Actions[0].Kind != "list_dedup" {
		t.Errorf("kind = %q, want list_dedup", result.Actions[0].Kind)
	}
}

func TestPlanRepairs_TypeUnconvertible_SkipsAllowedCheck(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindBool, Allowed: []string{"WIP"}},
	}
	front := buildFront("status", "maybe")

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString},
	}
	front := buildFront("status", []any{"WIP", "WIP"})

	result := PlanRepairs(front, props)
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
	props := map[string]config.PropertyDefinition{
		"parent": {Type: config.KindWikilink},
	}
	front := buildFront("parent", []any{"Books", "[[Papers]]"})

	result := PlanRepairs(front, props)
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

func TestPlanRepairs_MissingRequiredSkipUsesDefinitionSummary(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Required: true},
	}

	result := PlanRepairs(buildFront("other", "x"), props)
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %+v", result.Skips)
	}
	want := "required property is missing; no default available (type: list of string)"
	if result.Skips[0].Detail != want {
		t.Errorf("detail = %q, want %q", result.Skips[0].Detail, want)
	}
}

func TestPlanRepairs_NilFront(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true},
	}
	result := PlanRepairs(nil, props)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for nil front, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips for nil front, got %+v", result.Skips)
	}
}
