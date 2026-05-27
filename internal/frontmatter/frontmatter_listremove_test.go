package frontmatter

import (
	"errors"
	"reflect"
	"testing"
)

func TestListRemove_PresentValue(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "yaml", "rust"})
	changed, err := m.ListRemove("tags", "yaml")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "rust"}) {
		t.Errorf("tags: got %v want [go rust]", got)
	}
}

func TestListRemove_AbsentValueIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListRemove("tags", "yaml")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go"}) {
		t.Errorf("tags: got %v want [go]", got)
	}
}

func TestListRemove_AbsentKeyIsNoOp(t *testing.T) {
	m := NewMap()
	changed, err := m.ListRemove("tags", "go")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	if m.Has("tags") {
		t.Errorf("absent key should not be created")
	}
}

func TestListRemove_NonListReturnsError(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	_, err := m.ListRemove("title", "Hello")
	if err == nil {
		t.Fatalf("expected error for non-list value")
	}
	if !errors.Is(err, ErrNotAList) {
		t.Errorf("got %v want ErrNotAList", err)
	}
}

func TestListRemove_EmptyInputOnNonListIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	changed, err := m.ListRemove("title")
	if err != nil {
		t.Fatalf("ListRemove with no values should not validate type: %v", err)
	}
	if changed {
		t.Fatal("changed should be false")
	}
	if got, _ := m.GetString("title"); got != "Hello" {
		t.Fatalf("title changed to %q", got)
	}
}

func TestListRemove_LeavesEmptyList(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListRemove("tags", "go")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatalf("tags should remain present as an empty list, not deleted")
	}
	if len(got) != 0 {
		t.Errorf("tags should be empty, got %v", got)
	}
}

func TestListRemove_NullExistingValueIsNoOp(t *testing.T) {
	src := "---\ntags: ~\n---\nbody\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	changed, err := doc.Front.ListRemove("tags", "foo")
	if err != nil {
		t.Fatalf("ListRemove on null-valued key: %v", err)
	}
	if changed {
		t.Errorf("changed should be false (nothing to remove from null)")
	}
}
