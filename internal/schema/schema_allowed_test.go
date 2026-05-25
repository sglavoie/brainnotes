package schema

import (
	"testing"
	"time"
)

func TestCheckAllowed_Scalar(t *testing.T) {
	allowed := []string{"WIP", "Done", "Archived"}

	ok, bad := CheckAllowed("WIP", allowed, false)
	if !ok {
		t.Errorf("expected ok for WIP, got offending=%q", bad)
	}

	ok, bad = CheckAllowed("wip", allowed, false)
	if ok {
		t.Error("expected failure for case-sensitive mismatch")
	}
	if bad != "wip" {
		t.Errorf("offending = %q, want %q", bad, "wip")
	}
}

func TestCheckAllowed_List(t *testing.T) {
	allowed := []string{"a", "b", "c"}

	ok, _ := CheckAllowed([]any{"a", "b"}, allowed, false)
	if !ok {
		t.Error("expected ok for valid list")
	}

	ok, bad := CheckAllowed([]any{"a", "d"}, allowed, false)
	if ok {
		t.Error("expected failure for list with invalid element")
	}
	if bad != "d" {
		t.Errorf("offending = %q, want %q", bad, "d")
	}
}

func TestCheckAllowed_Wikilink(t *testing.T) {
	allowed := []string{"Alice", "Bob"}

	ok, _ := CheckAllowed("[[Alice]]", allowed, true)
	if !ok {
		t.Error("expected ok for wikilink-wrapped value")
	}

	ok, bad := CheckAllowed("[[Charlie]]", allowed, true)
	if ok {
		t.Error("expected failure for wikilink not in allowed")
	}
	if bad != "Charlie" {
		t.Errorf("offending = %q, want %q", bad, "Charlie")
	}

	ok, _ = CheckAllowed([]any{"[[Alice]]", "[[Bob]]"}, allowed, true)
	if !ok {
		t.Error("expected ok for wikilink list")
	}

	ok, bad = CheckAllowed([]any{"[[Alice]]", "[[Eve]]"}, allowed, true)
	if ok {
		t.Error("expected failure for wikilink list with invalid element")
	}
	if bad != "Eve" {
		t.Errorf("offending = %q, want %q", bad, "Eve")
	}
}

func TestCheckAllowed_WikilinkHeadingAndAliasUsesTarget(t *testing.T) {
	allowed := []string{"Books", "People"}

	ok, bad := CheckAllowed("[[Books#Now|To Read]]", allowed, true)
	if !ok {
		t.Errorf("expected ok for wikilink target match, got offending=%q", bad)
	}

	ok, bad = CheckAllowed("People#Team|Core", allowed, true)
	if !ok {
		t.Errorf("expected ok for bare wikilink-shaped target match, got offending=%q", bad)
	}

	ok, bad = CheckAllowed("[[Archive#Old|Ignored]]", allowed, true)
	if ok {
		t.Error("expected failure when wikilink target is not in allowed list")
	}
	if bad != "Archive#Old|Ignored" {
		t.Errorf("offending = %q, want %q", bad, "Archive#Old|Ignored")
	}
}

func TestCheckAllowed_NonStringType(t *testing.T) {
	ok, _ := CheckAllowed(42, []string{"42"}, false)
	if !ok {
		t.Error("expected ok when int string representation is in allowed list")
	}

	ok, bad := CheckAllowed(99, []string{"1", "2", "3"}, false)
	if ok {
		t.Error("expected failure when int string representation is not in allowed list")
	}
	if bad != "99" {
		t.Errorf("offending = %q, want %q", bad, "99")
	}

	ok, _ = CheckAllowed(true, []string{"true", "false"}, false)
	if !ok {
		t.Error("expected ok when bool string representation is in allowed list")
	}

	ok, bad = CheckAllowed(true, []string{"yes", "no"}, false)
	if ok {
		t.Error("expected failure when bool string representation is not in allowed list")
	}
	if bad != "true" {
		t.Errorf("offending = %q, want %q", bad, "true")
	}

	ok, _ = CheckAllowed(3.14, []string{"3.14", "2.71"}, false)
	if !ok {
		t.Error("expected ok when float string representation is in allowed list")
	}

	ok, bad = CheckAllowed(9.99, []string{"1.5", "2.5"}, false)
	if ok {
		t.Error("expected failure when float string representation is not in allowed list")
	}
	if bad != "9.99" {
		t.Errorf("offending = %q, want %q", bad, "9.99")
	}
}

func TestCheckAllowed_WholeNumberFloatPreservesDecimal(t *testing.T) {
	ok, _ := CheckAllowed(3.0, []string{"3.0", "4.0"}, false)
	if !ok {
		t.Error("expected ok when allowed list contains the whole-number float form")
	}

	ok, bad := CheckAllowed(3.0, []string{"3", "4"}, false)
	if ok {
		t.Error("expected failure when allowed list only contains the int form")
	}
	if bad != "3.0" {
		t.Errorf("offending = %q, want %q", bad, "3.0")
	}
}

func TestCheckAllowed_DateOnlyTime(t *testing.T) {
	dateOnly := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ok, _ := CheckAllowed(dateOnly, []string{"2024-01-01"}, false)
	if !ok {
		t.Error("expected ok when date-only string representation is in allowed list")
	}

	dateTime := time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)
	ok, _ = CheckAllowed(dateTime, []string{"2024-01-01"}, false)
	if ok {
		t.Error("expected failure when full timestamp shares a day with an allowed date")
	}
}

func TestCheckAllowed_NilValue(t *testing.T) {
	allowed := []string{"a", "b"}

	ok, bad := CheckAllowed(nil, allowed, false)
	if ok {
		t.Error("expected failure when value is nil")
	}
	if bad != "<nil>" {
		t.Errorf("offending = %q, want %q", bad, "<nil>")
	}
}

func TestCheckAllowed_MapValue(t *testing.T) {
	allowed := []string{"a", "b"}

	ok, _ := CheckAllowed(map[string]any{"x": 1}, allowed, false)
	if ok {
		t.Error("expected failure when value is a map")
	}
}

func TestCheckAllowed_ListNonStringItem(t *testing.T) {
	allowed := []string{"apple", "banana"}

	ok, bad := CheckAllowed([]any{42, "apple"}, allowed, false)
	if ok {
		t.Error("expected failure when list contains non-string item")
	}
	if bad != "42" {
		t.Errorf("offending = %q, want %q", bad, "42")
	}
}

func TestCheckAllowed_ListNonStringItemWikilink(t *testing.T) {
	allowed := []string{"Alice", "Bob"}

	ok, bad := CheckAllowed([]any{true, "[[Alice]]"}, allowed, true)
	if ok {
		t.Error("expected failure when wikilink list contains non-string item")
	}
	if bad != "true" {
		t.Errorf("offending = %q, want %q", bad, "true")
	}
}
