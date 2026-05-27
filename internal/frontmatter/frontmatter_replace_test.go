package frontmatter

import (
	"reflect"
	"testing"
)

func TestReplaceValue_ScalarMatch(t *testing.T) {
	m := NewMap()
	m.Set("status", "draft")
	changed, err := m.ReplaceValue("status", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetString("status")
	if got != "published" {
		t.Errorf("status: got %q want published", got)
	}
}

func TestReplaceValue_ScalarNoMatch(t *testing.T) {
	m := NewMap()
	m.Set("status", "draft")
	changed, err := m.ReplaceValue("status", "archived", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	got, _ := m.GetString("status")
	if got != "draft" {
		t.Errorf("status: got %q want draft", got)
	}
}

func TestReplaceValue_AbsentKeyIsNoOp(t *testing.T) {
	m := NewMap()
	changed, err := m.ReplaceValue("status", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	if m.Has("status") {
		t.Errorf("absent key should not be created")
	}
}

func TestReplaceValue_ListSingleMatch(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "draft", "yaml"})
	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "published", "yaml"}) {
		t.Errorf("tags: got %v want [go published yaml]", got)
	}
}

func TestReplaceValue_ListMultipleMatches(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"draft", "go", "draft"})
	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"published", "go", "published"}) {
		t.Errorf("tags: got %v want [published go published]", got)
	}
}

func TestReplaceValue_NoMatchesInList(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "yaml"})
	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestReplaceValue_FromEqualsToIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("status", "draft")
	changed, err := m.ReplaceValue("status", "draft", "draft")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false when from == to")
	}
}
