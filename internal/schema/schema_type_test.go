package schema

import (
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
)

func TestCheckScalar(t *testing.T) {
	dateOnly := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTime := time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		value    any
		kind     config.PropertyKind
		dateOnly bool
		want     bool
	}{
		{"hello", config.KindString, false, true},
		{42, config.KindString, false, false},
		{"[[Work]]", config.KindWikilink, false, true},
		{42, config.KindWikilink, false, false},
		{dateOnly, config.KindDate, true, true},
		{dateTime, config.KindDate, false, false},
		{"2024-01-01", config.KindDate, false, false},
		{dateTime, config.KindDatetime, false, true},
		{dateOnly, config.KindDatetime, true, true},
		{"2024-01-01T00:00:00Z", config.KindDatetime, false, false},
		{42, config.KindInt, false, true},
		{42.5, config.KindInt, false, false},
		{true, config.KindBool, false, true},
		{"true", config.KindBool, false, false},
		{3.14, config.KindFloat, false, true},
		{3, config.KindFloat, false, false},
		{[]any{"a"}, config.KindList, false, false},
		{"x", config.PropertyKind("unknown"), false, false},
	}

	for _, tt := range tests {
		got := CheckScalar(tt.value, tt.kind, tt.dateOnly)
		if got != tt.want {
			t.Errorf("CheckScalar(%v, %q, %v) = %v, want %v", tt.value, tt.kind, tt.dateOnly, got, tt.want)
		}
	}
}

func TestKindLabel(t *testing.T) {
	tests := []struct {
		def  config.PropertyDefinition
		want string
	}{
		{config.PropertyDefinition{Type: config.KindString}, "string"},
		{config.PropertyDefinition{Type: config.KindDatetime}, "datetime"},
		{config.PropertyDefinition{Type: config.KindList, Items: config.KindWikilink}, "list of wikilink"},
	}

	for _, tt := range tests {
		if got := KindLabel(tt.def); got != tt.want {
			t.Errorf("KindLabel(%+v) = %q, want %q", tt.def, got, tt.want)
		}
	}
}
