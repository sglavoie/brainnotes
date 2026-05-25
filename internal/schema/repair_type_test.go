package schema

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestPlanRepairs_TypeCoerce(t *testing.T) {
	tests := []struct {
		name     string
		front    *frontmatter.Map
		schema   map[string]config.PropertySchema
		wantKind string
		wantNew  any
	}{
		{
			name:     "string_to_bool",
			front:    buildFront("draft", "true"),
			schema:   map[string]config.PropertySchema{"draft": {Type: "bool"}},
			wantKind: "type_coerce",
			wantNew:  true,
		},
		{
			name:     "string_to_int",
			front:    buildFront("rating", "5"),
			schema:   map[string]config.PropertySchema{"rating": {Type: "int"}},
			wantKind: "type_coerce",
			wantNew:  5,
		},
		{
			name:     "string_to_float",
			front:    buildFront("score", "3.14"),
			schema:   map[string]config.PropertySchema{"score": {Type: "float"}},
			wantKind: "type_coerce",
			wantNew:  3.14,
		},
		{
			name:     "string_to_date",
			front:    buildFront("created", "2024-01-15"),
			schema:   map[string]config.PropertySchema{"created": {Type: "date"}},
			wantKind: "type_coerce",
			wantNew:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "string_to_list",
			front:    buildFront("tags", "solo"),
			schema:   map[string]config.PropertySchema{"tags": {Type: "list"}},
			wantKind: "type_coerce",
			wantNew:  []any{"solo"},
		},
		{
			name:     "float_to_int_lossless",
			front:    buildFront("count", float64(7)),
			schema:   map[string]config.PropertySchema{"count": {Type: "int"}},
			wantKind: "type_coerce",
			wantNew:  7,
		},
		{
			name:     "int_to_float",
			front:    buildFront("ratio", 3),
			schema:   map[string]config.PropertySchema{"ratio": {Type: "float"}},
			wantKind: "type_coerce",
			wantNew:  float64(3),
		},
		{
			name:     "bool_to_string",
			front:    buildFront("status", true),
			schema:   map[string]config.PropertySchema{"status": {Type: "string"}},
			wantKind: "type_coerce",
			wantNew:  "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlanRepairs(tt.front, tt.schema, nil)
			if len(result.Skips) != 0 {
				t.Errorf("unexpected skips: %+v", result.Skips)
			}
			if len(result.Actions) != 1 {
				t.Fatalf("expected 1 action, got %d: %+v", len(result.Actions), result.Actions)
			}
			act := result.Actions[0]
			if act.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", act.Kind, tt.wantKind)
			}
			if !reflect.DeepEqual(act.NewValue, tt.wantNew) {
				t.Errorf("new value = %v (%T), want %v (%T)", act.NewValue, act.NewValue, tt.wantNew, tt.wantNew)
			}
		})
	}
}

func TestPlanRepairs_TypeCoerce_Skip(t *testing.T) {
	tests := []struct {
		name   string
		front  *frontmatter.Map
		schema map[string]config.PropertySchema
	}{
		{
			name:   "string_maybe_to_bool",
			front:  buildFront("draft", "maybe"),
			schema: map[string]config.PropertySchema{"draft": {Type: "bool"}},
		},
		{
			name:   "string_abc_to_int",
			front:  buildFront("count", "abc"),
			schema: map[string]config.PropertySchema{"count": {Type: "int"}},
		},
		{
			name:   "float_lossy_to_int",
			front:  buildFront("count", 3.14),
			schema: map[string]config.PropertySchema{"count": {Type: "int"}},
		},
		{
			name:   "string_bad_to_date",
			front:  buildFront("created", "not-a-date"),
			schema: map[string]config.PropertySchema{"created": {Type: "date"}},
		},
		{
			name:   "list_to_string",
			front:  buildFront("status", []any{"WIP"}),
			schema: map[string]config.PropertySchema{"status": {Type: "string"}},
		},
		{
			name:   "nil_to_string",
			front:  buildFront("status", nil),
			schema: map[string]config.PropertySchema{"status": {Type: "string"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlanRepairs(tt.front, tt.schema, nil)
			if len(result.Actions) != 0 {
				t.Errorf("expected no actions, got %+v", result.Actions)
			}
			if len(result.Skips) != 1 {
				t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
			}
			if result.Skips[0].Kind != "type_unconvertible" {
				t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "type_unconvertible")
			}
		})
	}
}

func TestPlanRepairs_MissingRequired_Skip(t *testing.T) {
	front := buildFront("other", "value")
	sch := map[string]config.PropertySchema{
		"status": {Type: "string", Required: true},
	}

	result := PlanRepairs(front, sch, nil)
	if len(result.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	skip := result.Skips[0]
	if skip.Kind != "missing_required" {
		t.Errorf("skip kind = %q, want %q", skip.Kind, "missing_required")
	}
	if skip.Property != "status" {
		t.Errorf("skip property = %q, want %q", skip.Property, "status")
	}
}
