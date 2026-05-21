package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestPlanRepairs_WikilinkWrap_PartialPrefix(t *testing.T) {
	front := buildFront("parent", "[[broken")
	result := PlanRepairs(front, nil, []string{"parent"})
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
	result := PlanRepairs(front, nil, []string{"parent"})
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
	sch := map[string]config.PropertySchema{
		"priority": {Type: "int", Allowed: []string{"1", "2", "3"}},
	}
	front := buildFront("priority", 99)

	result := PlanRepairs(front, sch, nil)
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
	sch := map[string]config.PropertySchema{
		"priority": {Type: "int", Allowed: []string{"1", "2", "3"}},
	}
	front := buildFront("priority", 2)

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for valid int, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Errorf("expected no skips for valid int, got %+v", result.Skips)
	}
}

func TestPlanRepairs_WikilinkWrap_PartialInList(t *testing.T) {
	front := buildFront("categories", []any{"[[broken", "good", "also]]"})
	result := PlanRepairs(front, nil, []string{"categories"})
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	want := []any{"[[broken]]", "[[good]]", "[[also]]"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_Canonicalize_ListNonStringItem(t *testing.T) {
	// A list containing non-string items should flag those items as
	// unfixable during canonicalization, not silently skip them.
	sch := map[string]config.PropertySchema{
		"tags": {Type: "list", Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{42, "go"})

	result := PlanRepairs(front, sch, nil)

	// The "go" -> "Go" canonicalization should produce an action.
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "canonicalize" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "canonicalize")
	}
	wantNew := []any{42, "Go"}
	if !reflect.DeepEqual(result.Actions[0].NewValue, wantNew) {
		t.Errorf("new value = %v, want %v", result.Actions[0].NewValue, wantNew)
	}

	// The non-string item 42 should be reported as unfixable.
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip for non-string item, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
}

func TestPlanRepairs_Canonicalize_ListAllNonString(t *testing.T) {
	// A list of only non-string items should flag all as unfixable
	// without producing a canonicalize action (nothing changed).
	sch := map[string]config.PropertySchema{
		"tags": {Type: "list", Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{42, true})

	result := PlanRepairs(front, sch, nil)

	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
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
