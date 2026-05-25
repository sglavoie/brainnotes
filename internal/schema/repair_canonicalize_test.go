package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestPlanRepairs_Canonicalize(t *testing.T) {
	allowed := []string{"WIP", "Done", "Archived"}

	tests := []struct {
		name    string
		front   *frontmatter.Map
		wantNew any
	}{
		{
			name:    "lowercase_to_canonical",
			front:   buildFront("status", "wip"),
			wantNew: "WIP",
		},
		{
			name:    "mixed_case_to_canonical",
			front:   buildFront("status", "done"),
			wantNew: "Done",
		},
		{
			name:    "uppercase_to_canonical",
			front:   buildFront("status", "ARCHIVED"),
			wantNew: "Archived",
		},
	}

	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Allowed: allowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlanRepairs(tt.front, sch, nil)
			if len(result.Skips) != 0 {
				t.Errorf("unexpected skips: %+v", result.Skips)
			}
			if len(result.Actions) != 1 {
				t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
			}
			act := result.Actions[0]
			if act.Kind != "canonicalize" {
				t.Errorf("kind = %q, want %q", act.Kind, "canonicalize")
			}
			if !reflect.DeepEqual(act.NewValue, tt.wantNew) {
				t.Errorf("new value = %v, want %v", act.NewValue, tt.wantNew)
			}
		})
	}
}

func TestPlanRepairs_Canonicalize_List(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"tags": {Type: "list", Allowed: []string{"Go", "CLI", "Rust"}},
	}
	front := buildFront("tags", []any{"go", "CLI", "rust"})

	result := PlanRepairs(front, sch, nil)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "canonicalize" {
		t.Errorf("kind = %q, want %q", act.Kind, "canonicalize")
	}
	want := []any{"Go", "CLI", "Rust"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_Canonicalize_Skip(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Allowed: []string{"WIP", "Done"}},
	}
	front := buildFront("status", "unknown")

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

func TestPlanRepairs_Canonicalize_ListPartialSkip(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"tags": {Type: "list", Allowed: []string{"Go", "CLI"}},
	}
	front := buildFront("tags", []any{"go", "unknown"})

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "canonicalize" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "canonicalize")
	}
	want := []any{"Go", "unknown"}
	if !reflect.DeepEqual(result.Actions[0].NewValue, want) {
		t.Errorf("new value = %v, want %v", result.Actions[0].NewValue, want)
	}

	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "value_not_allowed" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "value_not_allowed")
	}
}

func TestPlanRepairs_Canonicalize_Wikilink(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"category": {Type: "string", Allowed: []string{"Work", "Personal"}},
	}
	front := buildFront("category", "[[work]]")

	result := PlanRepairs(front, sch, []string{"category"})
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].NewValue != "[[Work]]" {
		t.Errorf("new value = %v, want %q", result.Actions[0].NewValue, "[[Work]]")
	}
}

func TestPlanRepairs_Canonicalize_WikilinkPreservesHeadingAndAlias(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"category": {Type: "string", Allowed: []string{"Work", "Personal"}},
	}
	front := buildFront("category", "[[work#Today|Focus]]")

	result := PlanRepairs(front, sch, []string{"category"})
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "canonicalize" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "canonicalize")
	}
	if result.Actions[0].NewValue != "[[Work#Today|Focus]]" {
		t.Errorf("new value = %v, want %q", result.Actions[0].NewValue, "[[Work#Today|Focus]]")
	}
}

func TestPlanRepairs_CanonicalizeThenWrap_BareWikilinkHeadingAndAlias(t *testing.T) {
	sch := map[string]config.PropertySchema{
		"category": {Type: "string", Allowed: []string{"Work", "Personal"}},
	}
	front := buildFront("category", "work#Today|Focus")

	result := PlanRepairs(front, sch, []string{"category"})
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	if result.Actions[0].Kind != "canonicalize,wikilink_wrap" {
		t.Errorf("kind = %q, want %q", result.Actions[0].Kind, "canonicalize,wikilink_wrap")
	}
	if result.Actions[0].NewValue != "[[Work#Today|Focus]]" {
		t.Errorf("new value = %v, want %q", result.Actions[0].NewValue, "[[Work#Today|Focus]]")
	}
}
