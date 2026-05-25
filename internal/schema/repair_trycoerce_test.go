package schema

import (
	"reflect"
	"testing"
	"time"
)

func TestTryCoerce(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		val        any
		targetType string
		want       any
		wantOK     bool
	}{
		{"string_true_to_bool", "true", "bool", true, true},
		{"string_True_to_bool", "True", "bool", true, true},
		{"string_false_to_bool", "false", "bool", false, true},
		{"string_FALSE_to_bool", "FALSE", "bool", false, true},
		{"string_maybe_to_bool", "maybe", "bool", nil, false},
		{"string_42_to_int", "42", "int", 42, true},
		{"string_neg3_to_int", "-3", "int", -3, true},
		{"string_abc_to_int", "abc", "int", nil, false},
		{"string_3.14_to_int", "3.14", "int", nil, false},
		{"string_3.14_to_float", "3.14", "float", 3.14, true},
		{"string_42_to_float", "42", "float", float64(42), true},
		{"string_abc_to_float", "abc", "float", nil, false},
		{"string_date_to_date", "2024-01-15", "date", date, true},
		{"string_bad_to_date", "not-a-date", "date", nil, false},
		{"string_partial_to_date", "2024-01", "date", nil, false},
		{"string_to_list", "foo", "list", []any{"foo"}, true},
		{"float_42.0_to_int", float64(42), "int", 42, true},
		{"float_3.14_to_int_lossy", 3.14, "int", nil, false},
		{"int_to_float", 42, "float", float64(42), true},
		{"int_to_list", 42, "list", []any{42}, true},
		{"bool_true_to_string", true, "string", "true", true},
		{"bool_false_to_string", false, "string", "false", true},
		{"int_to_string", 42, "string", "42", true},
		{"float_to_string", 3.14, "string", "3.14", true},
		{"whole_float_to_string", 3.0, "string", "3.0", true},
		{"date_to_string", date, "string", "2024-01-15", true},
		{
			"datetime_to_string_lossy",
			time.Date(2024, 1, 15, 9, 45, 30, 0, time.FixedZone("EST", -5*60*60)),
			"string",
			nil,
			false,
		},
		{"bool_to_list", true, "list", []any{true}, true},
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
