package frontmatter

import (
	"reflect"
	"testing"
)

// TestListAdd_ZeroValueMap — calling mutation methods on a zero-value Map
// (one obtained without NewMap) must not panic and must lazily initialize.
func TestListAdd_ZeroValueMap(t *testing.T) {
	var m Map
	changed, err := m.ListAdd("tags", "a", "b")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	if got, _ := m.GetList("tags"); !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Errorf("tags: got %v", got)
	}
}

// TestGetWikilinkList_NonStringNonListType — requesting a wikilink list from a
// key whose value is neither string, []any, nor nil should return (nil, false).
func TestGetWikilinkList_NonStringNonListType(t *testing.T) {
	m := NewMap()
	m.Set("count", 42)
	got, ok := m.GetWikilinkList("count")
	if ok {
		t.Errorf("expected ok=false for int value, got %v", got)
	}
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
}

// TestUnset_ZeroValueMap — calling Unset on a zero-value Map (no NewMap call)
// must not panic.
func TestUnset_ZeroValueMap(t *testing.T) {
	var m Map
	m.Unset("anything") // must not panic
	if m.Has("anything") {
		t.Error("key should not exist")
	}
}

// TestReplaceValue_ZeroValueMap — calling ReplaceValue on a zero-value Map
// must not panic and must report no change.
func TestReplaceValue_ZeroValueMap(t *testing.T) {
	var m Map
	changed, err := m.ReplaceValue("key", "a", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false on zero-value Map")
	}
}

// TestListRemove_MultipleValues — removing several values in one call must
// remove all of them.
func TestListRemove_MultipleValues(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"a", "b", "c", "d"})
	changed, err := m.ListRemove("tags", "b", "d")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"a", "c"}) {
		t.Errorf("tags: got %v want [a c]", got)
	}
}

// TestReplaceValue_NilInList — replacing a nil element in a list must work.
func TestReplaceValue_NilInList(t *testing.T) {
	m := NewMap()
	m.Set("items", []any{nil, "b", nil})
	changed, err := m.ReplaceValue("items", nil, "replaced")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	got, _ := m.GetList("items")
	if !reflect.DeepEqual(got, []any{"replaced", "b", "replaced"}) {
		t.Errorf("items: got %v", got)
	}
}

// TestUnset_SequentialMultiple — unsetting several keys in sequence must leave
// the remaining keys and values intact.
func TestUnset_SequentialMultiple(t *testing.T) {
	m := NewMap()
	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)
	m.Set("d", 4)
	m.Unset("b")
	m.Unset("d")
	if !reflect.DeepEqual(m.Keys(), []string{"a", "c"}) {
		t.Errorf("keys after unset: %v", m.Keys())
	}
	if v, _ := m.Get("a"); v != 1 {
		t.Errorf("a: got %v want 1", v)
	}
	if v, _ := m.Get("c"); v != 3 {
		t.Errorf("c: got %v want 3", v)
	}
}

// TestRenameKey_ZeroValueMap — calling RenameKey on a zero-value Map must not
// panic and must report no change.
func TestRenameKey_ZeroValueMap(t *testing.T) {
	var m Map
	changed, err := m.RenameKey("old", "new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false on zero-value Map")
	}
}

// TestGetOnZeroValueMap — Get, GetString, GetList, GetTime, GetWikilinkList
// must all be safe on a zero-value Map (no NewMap call).
func TestGetOnZeroValueMap(t *testing.T) {
	var m Map
	if _, ok := m.Get("k"); ok {
		t.Error("Get should report absent on zero-value Map")
	}
	if _, ok := m.GetString("k"); ok {
		t.Error("GetString should report absent")
	}
	if _, ok := m.GetList("k"); ok {
		t.Error("GetList should report absent")
	}
	if _, ok := m.GetTime("k"); ok {
		t.Error("GetTime should report absent")
	}
	if _, ok := m.GetWikilinkList("k"); ok {
		t.Error("GetWikilinkList should report absent")
	}
}
