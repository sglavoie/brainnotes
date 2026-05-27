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

func TestListAdd_StringSliceStoredValue(t *testing.T) {
	m := NewMap()
	m.Set("tags", []string{"go"})

	changed, err := m.ListAdd("tags", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Fatal("changed should be true")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatal("tags should still report as a list")
	}
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Fatalf("tags: got %v want %v", got, []any{"go", "yaml"})
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

// TestListAdd_NullExistingValueTreatedAsEmptyList verifies that a key whose
// stored YAML value is null (e.g. `tags: ~`, `tags:`, `tags: null`) is treated
// as an empty list for the purpose of `+=`. Before the fix, ListAdd returned
// ErrNotAList for such notes because the parsed value is Go-native nil, not a
// []any. That made `bn set note.md tags+=foo` fail on otherwise-valid notes.
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

func TestListRemove_StringSliceStoredValue(t *testing.T) {
	m := NewMap()
	m.Set("tags", []string{"go", "yaml"})

	changed, err := m.ListRemove("tags", "go")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Fatal("changed should be true")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatal("tags should still report as a list")
	}
	if !reflect.DeepEqual(got, []any{"yaml"}) {
		t.Fatalf("tags: got %v want %v", got, []any{"yaml"})
	}
}

// TestListRemove_NullExistingValueIsNoOp verifies that a key whose stored YAML
// value is null (e.g. `tags: ~`) is treated as an empty list for the purpose
// of `-=`: the call is a silent no-op. Before the fix, ListRemove returned
// ErrNotAList in this case, so `bn set note.md tags-=foo` failed on
// otherwise-valid notes even though the value to remove was not present.
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

func TestReplaceValue_StringSliceStoredValue(t *testing.T) {
	m := NewMap()
	m.Set("tags", []string{"draft", "go"})

	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Fatal("changed should be true")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatal("tags should still report as a list")
	}
	if !reflect.DeepEqual(got, []any{"published", "go"}) {
		t.Fatalf("tags: got %v want %v", got, []any{"published", "go"})
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

func TestMutateRoundTrip(t *testing.T) {
	src := readFixture(t, "mutate.in.md")
	cfg := defaultCfg()

	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if _, err := doc.Front.ListAdd("topics", "yaml"); err != nil {
		t.Fatalf("ListAdd topics: %v", err)
	}
	if _, err := doc.Front.ListRemove("topics", "go"); err != nil {
		t.Fatalf("ListRemove topics: %v", err)
	}
	if _, err := doc.Front.ReplaceValue("status", "draft", "published"); err != nil {
		t.Fatalf("ReplaceValue status: %v", err)
	}

	got, err := Marshal(doc, cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := readFixture(t, "mutate.out.md")
	if string(got) != string(want) {
		t.Errorf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// Re-parse and confirm alphabetical order is stable.
	doc2, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	out2, err := Marshal(doc2, cfg)
	if err != nil {
		t.Fatalf("Marshal round-trip: %v", err)
	}
	if string(out2) != string(got) {
		t.Errorf("not idempotent after mutation:\nfirst:\n%s\nsecond:\n%s", got, out2)
	}
}
