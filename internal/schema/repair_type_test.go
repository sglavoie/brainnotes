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
		props    map[string]config.PropertyDefinition
		wantKind string
		wantNew  any
	}{
		{
			name:     "string_to_bool",
			front:    buildFront("draft", "true"),
			props:    map[string]config.PropertyDefinition{"draft": {Type: config.KindBool}},
			wantKind: "type_coerce",
			wantNew:  true,
		},
		{
			name:     "string_to_int",
			front:    buildFront("rating", "5"),
			props:    map[string]config.PropertyDefinition{"rating": {Type: config.KindInt}},
			wantKind: "type_coerce",
			wantNew:  5,
		},
		{
			name:     "string_to_float",
			front:    buildFront("score", "3.14"),
			props:    map[string]config.PropertyDefinition{"score": {Type: config.KindFloat}},
			wantKind: "type_coerce",
			wantNew:  3.14,
		},
		{
			name:     "string_to_date",
			front:    buildFront("created", "2024-01-15"),
			props:    map[string]config.PropertyDefinition{"created": {Type: config.KindDate}},
			wantKind: "type_coerce",
			wantNew:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "string_to_list",
			front:    buildFront("tags", "solo"),
			props:    map[string]config.PropertyDefinition{"tags": {Type: config.KindList, Items: config.KindString}},
			wantKind: "type_coerce",
			wantNew:  []any{"solo"},
		},
		{
			name:     "float_to_int_lossless",
			front:    buildFront("count", float64(7)),
			props:    map[string]config.PropertyDefinition{"count": {Type: config.KindInt}},
			wantKind: "type_coerce",
			wantNew:  7,
		},
		{
			name:     "int_to_float",
			front:    buildFront("ratio", 3),
			props:    map[string]config.PropertyDefinition{"ratio": {Type: config.KindFloat}},
			wantKind: "type_coerce",
			wantNew:  float64(3),
		},
		{
			name:     "bool_to_string",
			front:    buildFront("status", true),
			props:    map[string]config.PropertyDefinition{"status": {Type: config.KindString}},
			wantKind: "type_coerce",
			wantNew:  "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlanRepairs(tt.front, tt.props)
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
		name  string
		front *frontmatter.Map
		props map[string]config.PropertyDefinition
	}{
		{
			name:  "string_maybe_to_bool",
			front: buildFront("draft", "maybe"),
			props: map[string]config.PropertyDefinition{"draft": {Type: config.KindBool}},
		},
		{
			name:  "string_abc_to_int",
			front: buildFront("count", "abc"),
			props: map[string]config.PropertyDefinition{"count": {Type: config.KindInt}},
		},
		{
			name:  "string_leading_zero_to_int_lossy",
			front: buildFront("count", "007"),
			props: map[string]config.PropertyDefinition{"count": {Type: config.KindInt}},
		},
		{
			name:  "float_lossy_to_int",
			front: buildFront("count", 3.14),
			props: map[string]config.PropertyDefinition{"count": {Type: config.KindInt}},
		},
		{
			name:  "string_bad_to_date",
			front: buildFront("created", "not-a-date"),
			props: map[string]config.PropertyDefinition{"created": {Type: config.KindDate}},
		},
		{
			name:  "list_to_string",
			front: buildFront("status", []any{"WIP"}),
			props: map[string]config.PropertyDefinition{"status": {Type: config.KindString}},
		},
		{
			name:  "nil_to_string",
			front: buildFront("status", nil),
			props: map[string]config.PropertyDefinition{"status": {Type: config.KindString}},
		},
		{
			name:  "datetime_to_string_lossy",
			front: buildFront("created", time.Date(2024, 1, 15, 9, 45, 30, 0, time.FixedZone("EST", -5*60*60))),
			props: map[string]config.PropertyDefinition{"created": {Type: config.KindString}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlanRepairs(tt.front, tt.props)
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
	props := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true},
	}

	result := PlanRepairs(front, props)
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

func TestPlanRepairs_ParsedUTCMidnightDateTimeRejectedAsDate(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\ncreated: 2025-12-13T00:00:00Z\n---\nbody\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	props := map[string]config.PropertyDefinition{
		"created": {Type: config.KindDate},
	}

	result := PlanRepairs(&doc.Front, props)
	if len(result.Actions) != 0 {
		t.Fatalf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("expected 1 skip, got %d: %+v", len(result.Skips), result.Skips)
	}
	if result.Skips[0].Kind != "type_unconvertible" {
		t.Errorf("skip kind = %q, want %q", result.Skips[0].Kind, "type_unconvertible")
	}
	if result.Skips[0].Detail != "cannot safely convert datetime to date" {
		t.Errorf("skip detail = %q, want %q", result.Skips[0].Detail, "cannot safely convert datetime to date")
	}
}

func TestPlanRepairs_ParsedDateOnlyAcceptedAsDate(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\ncreated: 2025-12-13\n---\nbody\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	props := map[string]config.PropertyDefinition{
		"created": {Type: config.KindDate},
	}

	result := PlanRepairs(&doc.Front, props)
	if len(result.Actions) != 0 {
		t.Fatalf("expected no actions, got %+v", result.Actions)
	}
	if len(result.Skips) != 0 {
		t.Fatalf("expected no skips, got %+v", result.Skips)
	}
}
