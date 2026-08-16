package schema

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func scalarDef(kind config.PropertyKind) config.PropertyDefinition {
	return config.PropertyDefinition{Type: kind}
}

func listDef(items config.PropertyKind) config.PropertyDefinition {
	return config.PropertyDefinition{Type: config.KindList, Items: items}
}

func TestTryCoerce(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		val    any
		def    config.PropertyDefinition
		want   any
		wantOK bool
	}{
		{"string_true_to_bool", "true", scalarDef(config.KindBool), true, true},
		{"string_True_to_bool", "True", scalarDef(config.KindBool), true, true},
		{"string_false_to_bool", "false", scalarDef(config.KindBool), false, true},
		{"string_FALSE_to_bool", "FALSE", scalarDef(config.KindBool), false, true},
		{"string_maybe_to_bool", "maybe", scalarDef(config.KindBool), nil, false},
		{"string_42_to_int", "42", scalarDef(config.KindInt), 42, true},
		{"string_neg3_to_int", "-3", scalarDef(config.KindInt), -3, true},
		{"string_007_to_int_lossy", "007", scalarDef(config.KindInt), nil, false},
		{"string_neg007_to_int_lossy", "-007", scalarDef(config.KindInt), nil, false},
		{"string_plus42_to_int", "+42", scalarDef(config.KindInt), nil, false},
		{"string_abc_to_int", "abc", scalarDef(config.KindInt), nil, false},
		{"string_3.14_to_int", "3.14", scalarDef(config.KindInt), nil, false},
		{"string_3.14_to_float", "3.14", scalarDef(config.KindFloat), 3.14, true},
		{"string_42_to_float", "42", scalarDef(config.KindFloat), float64(42), true},
		{"string_abc_to_float", "abc", scalarDef(config.KindFloat), nil, false},
		{"string_date_to_date", "2024-01-15", scalarDef(config.KindDate), date, true},
		{"string_bad_to_date", "not-a-date", scalarDef(config.KindDate), nil, false},
		{"string_partial_to_date", "2024-01", scalarDef(config.KindDate), nil, false},
		{
			"string_rfc3339_to_datetime",
			"2024-01-15T09:45:30Z",
			scalarDef(config.KindDatetime),
			time.Date(2024, 1, 15, 9, 45, 30, 0, time.UTC),
			true,
		},
		{
			"string_rfc3339nano_to_datetime",
			"2024-01-15T09:45:30.123456789Z",
			scalarDef(config.KindDatetime),
			time.Date(2024, 1, 15, 9, 45, 30, 123456789, time.UTC),
			true,
		},
		{"string_date_only_to_datetime", "2024-01-15", scalarDef(config.KindDatetime), nil, false},
		{"string_bad_to_datetime", "not-a-datetime", scalarDef(config.KindDatetime), nil, false},
		{"string_to_list", "foo", listDef(config.KindString), []any{"foo"}, true},
		{"float_42.0_to_int", float64(42), scalarDef(config.KindInt), 42, true},
		{"float_3.14_to_int_lossy", 3.14, scalarDef(config.KindInt), nil, false},
		{"int_to_float", 42, scalarDef(config.KindFloat), float64(42), true},
		{"int_to_int_list", 42, listDef(config.KindInt), []any{42}, true},
		{"int_to_string_list", 42, listDef(config.KindString), []any{"42"}, true},
		{"bool_true_to_string", true, scalarDef(config.KindString), "true", true},
		{"bool_false_to_string", false, scalarDef(config.KindString), "false", true},
		{"int_to_string", 42, scalarDef(config.KindString), "42", true},
		{"float_to_string", 3.14, scalarDef(config.KindString), "3.14", true},
		{"whole_float_to_string", 3.0, scalarDef(config.KindString), "3.0", true},
		{"date_to_string", date, scalarDef(config.KindString), "2024-01-15", true},
		{"bool_to_wikilink", true, scalarDef(config.KindWikilink), "true", true},
		{
			"datetime_to_string_lossy",
			time.Date(2024, 1, 15, 9, 45, 30, 0, time.FixedZone("EST", -5*60*60)),
			scalarDef(config.KindString),
			nil,
			false,
		},
		{"bool_to_list", true, listDef(config.KindBool), []any{true}, true},
		{"list_to_string", []any{"a"}, scalarDef(config.KindString), nil, false},
		{"list_to_int", []any{"a"}, scalarDef(config.KindInt), nil, false},
		{"nested_list_never_wrapped", []any{"a"}, listDef(config.KindString), nil, false},
		{"map_never_wrapped_into_list", map[string]any{"a": 1}, listDef(config.KindString), nil, false},
		{"nil_never_wrapped_into_list", nil, listDef(config.KindString), nil, false},
		{"nil_to_string", nil, scalarDef(config.KindString), nil, false},
		{"nil_to_int", nil, scalarDef(config.KindInt), nil, false},
		{"nil_to_bool", nil, scalarDef(config.KindBool), nil, false},
		{"list_already_valid", []any{"a", "b"}, listDef(config.KindString), nil, false},
		{"list_items_coerced", []any{"1", 2}, listDef(config.KindInt), []any{1, 2}, true},
		{"list_item_unconvertible", []any{"a", 2}, listDef(config.KindInt), nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TryCoerce(tt.val, tt.def)
			if ok != tt.wantOK {
				t.Fatalf("TryCoerce(%v, %+v) ok=%v, want ok=%v", tt.val, tt.def, ok, tt.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TryCoerce(%v, %+v) = %v (%T), want %v (%T)", tt.val, tt.def, got, got, tt.want, tt.want)
			}
		})
	}
}
