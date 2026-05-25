package schema

import (
	"testing"
	"time"
)

func TestValidType(t *testing.T) {
	valid := []string{"string", "list", "date", "int", "bool", "float"}
	for _, v := range valid {
		if !ValidType(v) {
			t.Errorf("ValidType(%q) = false, want true", v)
		}
	}

	invalid := []string{"", "str", "integer", "boolean", "array", "number"}
	for _, v := range invalid {
		if ValidType(v) {
			t.Errorf("ValidType(%q) = true, want false", v)
		}
	}
}

func TestCheckType(t *testing.T) {
	dateOnly := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTime := time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		value      any
		schemaType string
		want       bool
	}{
		{"hello", "string", true},
		{42, "string", false},
		{[]any{"a", "b"}, "list", true},
		{"not a list", "list", false},
		{dateOnly, "date", true},
		{dateTime, "date", false},
		{"2024-01-01", "date", false},
		{42, "int", true},
		{42.5, "int", false},
		{true, "bool", true},
		{"true", "bool", false},
		{3.14, "float", true},
		{3, "float", false},
		{"x", "unknown", false},
	}

	for _, tt := range tests {
		got := CheckType(tt.value, tt.schemaType)
		if got != tt.want {
			t.Errorf("CheckType(%v, %q) = %v, want %v", tt.value, tt.schemaType, got, tt.want)
		}
	}
}
