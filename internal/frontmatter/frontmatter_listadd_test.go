package frontmatter

import (
	"errors"
	"reflect"
	"testing"
)

func TestListAdd_NewKey(t *testing.T) {
	m := NewMap()
	changed, err := m.ListAdd("tags", "go", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true when creating a new list")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatalf("tags should be present as a list")
	}
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
	if !reflect.DeepEqual(m.Keys(), []string{"tags"}) {
		t.Errorf("keys: got %v want [tags]", m.Keys())
	}
}

func TestListAdd_NewKeyDeduplicatesInput(t *testing.T) {
	m := NewMap()
	changed, err := m.ListAdd("tags", "go", "go", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestListAdd_NewKeyEmptyInput(t *testing.T) {
	m := NewMap()
	changed, err := m.ListAdd("tags")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if changed {
		t.Errorf("changed should be false when no values provided")
	}
	if m.Has("tags") {
		t.Fatalf("empty ListAdd should not create tags")
	}
}

func TestListAdd_AppendUnique(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListAdd("tags", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestListAdd_AppendDuplicateIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "yaml"})
	changed, err := m.ListAdd("tags", "go")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if changed {
		t.Errorf("changed should be false for an existing value")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestListAdd_AppendMixedDuplicateAndNew(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListAdd("tags", "go", "rust")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true when at least one new value is added")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "rust"}) {
		t.Errorf("tags: got %v want [go rust]", got)
	}
}

func TestListAdd_NonListReturnsError(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	_, err := m.ListAdd("title", "x")
	if err == nil {
		t.Fatalf("expected error for non-list value")
	}
	if !errors.Is(err, ErrNotAList) {
		t.Errorf("got %v want ErrNotAList", err)
	}
}

func TestListAdd_EmptyInputOnNonListIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	changed, err := m.ListAdd("title")
	if err != nil {
		t.Fatalf("ListAdd with no values should not validate type: %v", err)
	}
	if changed {
		t.Fatal("changed should be false")
	}
	if got, _ := m.GetString("title"); got != "Hello" {
		t.Fatalf("title changed to %q", got)
	}
}

func TestListAdd_NullExistingValueTreatedAsEmptyList(t *testing.T) {
	src := "---\ntags: ~\n---\nbody\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	changed, err := doc.Front.ListAdd("tags", "foo", "bar")
	if err != nil {
		t.Fatalf("ListAdd on null-valued key: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true when appending to a null-valued key")
	}
	got, ok := doc.Front.GetList("tags")
	if !ok {
		t.Fatalf("tags should be a list after ListAdd")
	}
	if !reflect.DeepEqual(got, []any{"foo", "bar"}) {
		t.Errorf("tags: got %v want [foo bar]", got)
	}
}
