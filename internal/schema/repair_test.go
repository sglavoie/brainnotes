package schema

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func buildFront(pairs ...any) *frontmatter.Map {
	m := frontmatter.NewMap()
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return &m
}

// --- TryCoerce unit tests ---

func TestTryCoerce(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		val        any
		targetType string
		want       any
		wantOK     bool
	}{
		// string → bool
		{"string_true_to_bool", "true", "bool", true, true},
		{"string_True_to_bool", "True", "bool", true, true},
		{"string_false_to_bool", "false", "bool", false, true},
		{"string_FALSE_to_bool", "FALSE", "bool", false, true},
		{"string_maybe_to_bool", "maybe", "bool", nil, false},

		// string → int
		{"string_42_to_int", "42", "int", 42, true},
		{"string_neg3_to_int", "-3", "int", -3, true},
		{"string_abc_to_int", "abc", "int", nil, false},
		{"string_3.14_to_int", "3.14", "int", nil, false},

		// string → float
		{"string_3.14_to_float", "3.14", "float", 3.14, true},
		{"string_42_to_float", "42", "float", float64(42), true},
		{"string_abc_to_float", "abc", "float", nil, false},

		// string → date
		{"string_date_to_date", "2024-01-15", "date", date, true},
		{"string_bad_to_date", "not-a-date", "date", nil, false},
		{"string_partial_to_date", "2024-01", "date", nil, false},

		// string → list (scalar promotion)
		{"string_to_list", "foo", "list", []any{"foo"}, true},

		// float → int (lossless)
		{"float_42.0_to_int", float64(42), "int", 42, true},
		{"float_3.14_to_int_lossy", 3.14, "int", nil, false},

		// int → float
		{"int_to_float", 42, "float", float64(42), true},

		// int → list
		{"int_to_list", 42, "list", []any{42}, true},

		// bool → string
		{"bool_true_to_string", true, "string", "true", true},
		{"bool_false_to_string", false, "string", "false", true},

		// int → string
		{"int_to_string", 42, "string", "42", true},

		// float → string
		{"float_to_string", 3.14, "string", "3.14", true},

		// date → string
		{"date_to_string", date, "string", "2024-01-15", true},

		// bool → list
		{"bool_to_list", true, "list", []any{true}, true},

		// unconvertible
		{"list_to_string", []any{"a"}, "string", nil, false},
		{"list_to_int", []any{"a"}, "int", nil, false},
		{"nil_to_string", nil, "string", nil, false},
		{"nil_to_int", nil, "int", nil, false},
		{"nil_to_bool", nil, "bool", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TryCoerce(tt.val, tt.targetType)
			if ok != tt.wantOK {
				t.Fatalf("TryCoerce(%v, %q) ok=%v, want ok=%v", tt.val, tt.targetType, ok, tt.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TryCoerce(%v, %q) = %v (%T), want %v (%T)", tt.val, tt.targetType, got, got, tt.want, tt.want)
			}
		})
	}
}

// --- PlanRepairs integration tests ---

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

func TestTryCoerce_FloatNaNToInt(t *testing.T) {
	_, ok := TryCoerce(math.NaN(), "int")
	if ok {
		t.Error("TryCoerce(NaN, int) should return false")
	}
}

func TestTryCoerce_FloatInfToInt(t *testing.T) {
	_, ok := TryCoerce(math.Inf(1), "int")
	if ok {
		t.Error("TryCoerce(+Inf, int) should return false")
	}
	_, ok = TryCoerce(math.Inf(-1), "int")
	if ok {
		t.Error("TryCoerce(-Inf, int) should return false")
	}
}

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
