package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestPlanRepairs_WikilinkWrap(t *testing.T) {
	tests := []struct {
		name    string
		front   *frontmatter.Map
		wantNew any
	}{
		{
			name:    "bare_string",
			front:   buildFront("parent", "Books"),
			wantNew: "[[Books]]",
		},
		{
			name:    "bare_list_elements",
			front:   buildFront("categories", []any{"Books", "[[Papers]]", "Music"}),
			wantNew: []any{"[[Books]]", "[[Papers]]", "[[Music]]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlanRepairs(tt.front, nil, []string{"parent", "categories"})
			if len(result.Skips) != 0 {
				t.Errorf("unexpected skips: %+v", result.Skips)
			}
			if len(result.Actions) != 1 {
				t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
			}
			act := result.Actions[0]
			if act.Kind != "wikilink_wrap" {
				t.Errorf("kind = %q, want %q", act.Kind, "wikilink_wrap")
			}
			if !reflect.DeepEqual(act.NewValue, tt.wantNew) {
				t.Errorf("new value = %v, want %v", act.NewValue, tt.wantNew)
			}
		})
	}
}

func TestPlanRepairs_WikilinkWrap_AlreadyWrapped(t *testing.T) {
	front := buildFront("parent", "[[Books]]")
	result := PlanRepairs(front, nil, []string{"parent"})
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for already-wrapped value, got %+v", result.Actions)
	}
}

func TestPlanRepairs_WikilinkWrap_EmptyString(t *testing.T) {
	front := buildFront("parent", "")
	result := PlanRepairs(front, nil, []string{"parent"})
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for empty wikilink value, got %+v", result.Actions)
	}
}

func TestPlanRepairs_ListDedup(t *testing.T) {
	front := buildFront("tags", []any{"go", "cli", "go", "rust", "cli"})
	result := PlanRepairs(front, nil, nil)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
	}
	act := result.Actions[0]
	if act.Kind != "list_dedup" {
		t.Errorf("kind = %q, want %q", act.Kind, "list_dedup")
	}
	want := []any{"go", "cli", "rust"}
	if !reflect.DeepEqual(act.NewValue, want) {
		t.Errorf("new value = %v, want %v", act.NewValue, want)
	}
}

func TestPlanRepairs_ListDedup_NoDuplicates(t *testing.T) {
	front := buildFront("tags", []any{"go", "cli", "rust"})
	result := PlanRepairs(front, nil, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for unique list, got %+v", result.Actions)
	}
}

func TestPlanRepairs_ListDedup_EmptyList(t *testing.T) {
	front := buildFront("tags", []any{})
	result := PlanRepairs(front, nil, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions for empty list, got %+v", result.Actions)
	}
}
