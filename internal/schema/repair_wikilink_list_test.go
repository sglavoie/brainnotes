package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

func wikilinkProps(keys ...string) map[string]config.PropertyDefinition {
	props := make(map[string]config.PropertyDefinition, len(keys))
	for _, k := range keys {
		props[k] = config.PropertyDefinition{Type: config.KindWikilink}
	}
	return props
}

func TestPlanRepairs_WikilinkWrap_PartialPrefix(t *testing.T) {
	front := buildFront("parent", "[[broken")
	result := PlanRepairs(front, wikilinkProps("parent"))
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "wikilink_wrap" {
		t.Errorf("kind = %q, want %q", act.Kind, "wikilink_wrap")
	}
	if act.NewValue != "[[broken]]" {
		t.Errorf("new value = %q, want %q", act.NewValue, "[[broken]]")
	}
}

func TestPlanRepairs_WikilinkWrap_PartialSuffix(t *testing.T) {
	front := buildFront("parent", "broken]]")
	result := PlanRepairs(front, wikilinkProps("parent"))
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "wikilink_wrap" {
		t.Errorf("kind = %q, want %q", act.Kind, "wikilink_wrap")
	}
	if act.NewValue != "[[broken]]" {
		t.Errorf("new value = %q, want %q", act.NewValue, "[[broken]]")
	}
}

func TestPlanRepairs_IntAllowedNotAllowed_Skip(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"priority": {Type: config.KindInt, Allowed: []string{"1", "2", "3"}},
	}
	front := buildFront("priority", 99)

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
}

func TestPlanRepairs_IntAllowedInList_NoAction(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"priority": {Type: config.KindInt, Allowed: []string{"1", "2", "3"}},
	}
	front := buildFront("priority", 2)

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for valid int, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips for valid int, got %+v", result.Skips)
	}
}

func TestPlanRepairs_WikilinkWrap_PartialInList(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"categories": {Type: config.KindList, Items: config.KindWikilink},
	}
	front := buildFront("categories", []any{"[[broken", "good", "also]]"})

	result := PlanRepairs(front, props)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	want := []any{"[[broken]]", "[[good]]", "[[also]]"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_WikilinkAllowed_PartialWrapperNoStaleSkip(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"category": {Type: config.KindWikilink, Allowed: []string{"Work", "Personal"}},
	}
	front := buildFront("category", "[[Work")

	result := PlanRepairs(front, props)
	if len(result.Skips) != 0 {
		t.Fatalf("expected no stale skips after wrapper repair, got %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "wikilink_wrap" {
		t.Errorf("kind = %q, want %q", act.Kind, "wikilink_wrap")
	}
	if act.NewValue != "[[Work]]" {
		t.Errorf("new value = %q, want %q", act.NewValue, "[[Work]]")
	}
}

func TestPlanRepairs_WikilinkAllowed_PartialListCanonicalizesWithoutStaleSkip(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"categories": {Type: config.KindList, Items: config.KindWikilink, Allowed: []string{"Work", "Personal", "Done"}},
	}
	front := buildFront("categories", []any{"[[work", "Personal]]", "Done"})

	result := PlanRepairs(front, props)
	if len(result.Skips) != 0 {
		t.Fatalf("expected no stale skips after wrapper repair, got %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "canonicalize,wikilink_wrap" {
		t.Errorf("kind = %q, want %q", act.Kind, "canonicalize,wikilink_wrap")
	}
	want := []any{"[[Work]]", "[[Personal]]", "[[Done]]"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_WikilinkAllowed_BareCanonicalizeThenWrap(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"category": {Type: config.KindWikilink, Allowed: []string{"Work", "Personal"}},
	}
	front := buildFront("category", "work")

	result := PlanRepairs(front, props)
	if len(result.Skips) != 0 {
		t.Fatalf("expected no skips, got %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "canonicalize,wikilink_wrap" {
		t.Errorf("kind = %q, want %q", act.Kind, "canonicalize,wikilink_wrap")
	}
	if act.NewValue != "[[Work]]" {
		t.Errorf("new value = %q, want %q", act.NewValue, "[[Work]]")
	}
}

func TestPlanRepairs_WikilinkWrap_ComplexBracketedScalarUnchanged(t *testing.T) {
	cases := []string{
		"[[Books]] extra",
		"prefix [[Books]]",
		"[[[[Books]]]]",
	}

	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			front := buildFront("parent", value)

			result := PlanRepairs(front, wikilinkProps("parent"))
			if len(result.Actions) != 0 {
				t.Fatalf("expected no unsafe wrap action, got %+v", result.Actions)
			}
			if len(result.Skips) != 0 {
				t.Fatalf("expected no skips without allowed values, got %+v", result.Skips)
			}
		})
	}
}

func TestPlanRepairs_WikilinkAllowed_ComplexBracketedValueSkippedWithoutMangle(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"category": {Type: config.KindWikilink, Allowed: []string{"Books", "Papers"}},
	}
	front := buildFront("category", "[[Books]] extra")

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Fatalf("expected no unsafe action, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
	if result.Skips[0].Detail != `no allowed value matches "[[Books]] extra"` {
		t.Errorf("skip detail = %q", result.Skips[0].Detail)
	}
}

func TestPlanRepairs_ListItemCoercedToDeclaredKindBeforeCanonicalize(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{42, "go"})

	result := PlanRepairs(front, props)

	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "type_coerce,canonicalize" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "type_coerce,canonicalize")
	}
	wantNew := []any{"42", "Go"}
	if !reflect.DeepEqual(result.Actions[0].NewValue, wantNew) {
		t.Errorf("new value = %v, want %v", result.Actions[0].NewValue, wantNew)
	}

	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip for the disallowed item, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
}

func TestPlanRepairs_ListItemCoercionMatchingAllowed_NoSkips(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Allowed: []string{"42", "Go"}},
	}
	front := buildFront("tags", []any{42, "go"})

	result := PlanRepairs(front, props)

	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "type_coerce,canonicalize" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "type_coerce,canonicalize")
	}
	wantNew := []any{"42", "Go"}
	if !reflect.DeepEqual(result.Actions[0].NewValue, wantNew) {
		t.Errorf("new value = %v, want %v", result.Actions[0].NewValue, wantNew)
	}
	if len(result.Skips) != 0 {
		t.Fatalf("expected no skips, got %+v", result.Skips)
	}
}

func TestPlanRepairs_ListOfFloatsKeepsFloats(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"ratings": {Type: config.KindList, Items: config.KindFloat, Allowed: []string{"3.0", "4.5"}},
	}
	front := buildFront("ratings", []any{3.0, 4.5})

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Fatalf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Fatalf("expected no skips, got %+v", result.Skips)
	}
}

func TestPlanRepairs_ListItemUnsupportedYAMLIsUnconvertible(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString},
	}
	front := buildFront("tags", []any{"go", map[string]any{"nested": true}})

	result := PlanRepairs(front, props)
	if len(result.Actions) != 0 {
		t.Fatalf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "type_unconvertible" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "type_unconvertible")
	}
}

func TestPlanRepairs_Canonicalize_DeduplicatesRepeatedInvalidListSkips(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Allowed: []string{"Go"}},
	}
	front := buildFront("tags", []any{"bad", "bad", "go"})

	result := PlanRepairs(front, props)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "canonicalize,list_dedup" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "canonicalize,list_dedup")
	}
	wantNew := []any{"bad", "Go"}
	if !reflect.DeepEqual(result.Actions[0].NewValue, wantNew) {
		t.Errorf("new value = %v, want %v", result.Actions[0].NewValue, wantNew)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip for repeated invalid value, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
	if result.Skips[0].Detail != `no allowed value matches "bad"` {
		t.Errorf("skip detail = %q", result.Skips[0].Detail)
	}
}

func TestPlanRepairs_Canonicalize_ListAllNonString(t *testing.T) {
	props := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{42, true})

	result := PlanRepairs(front, props)

	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 type_coerce action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "type_coerce" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "type_coerce")
	}
	if len(result.Skips) != 2 {
		t.Fatalf("expected 2 skips, got %d: %+v", len(result.Skips), result.Skips)
	}
	for _, skip := range result.Skips {
		if skip.Kind != "value_not_allowed" {
			t.Errorf("skip kind = %q, want %q", skip.Kind, "value_not_allowed")
		}
	}
}
