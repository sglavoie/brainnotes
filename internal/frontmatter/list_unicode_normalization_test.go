package frontmatter

import (
	"reflect"
	"testing"
)

// The fixtures are \u escapes, not literals, so an editor that normalizes the
// source cannot collapse the two spellings into one and make the tests vacuous.
const (
	cafeComposed   = "Café"
	cafeDecomposed = "Café"
)

func TestListRemove_MatchesAcrossNormalizationForms(t *testing.T) {
	t.Parallel()
	if cafeComposed == cafeDecomposed {
		t.Fatal("fixtures are the same bytes; the test proves nothing")
	}
	m := NewMap()
	m.Set("tags", []any{cafeDecomposed, "go"})
	changed, err := m.ListRemove("tags", cafeComposed)
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Fatal("removing the composed spelling of a stored decomposed item should change the list")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go"}) {
		t.Errorf("tags = %q, want [go]", got)
	}
}

func TestListAdd_DoesNotDuplicateAcrossNormalizationForms(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set("tags", []any{cafeDecomposed})
	changed, err := m.ListAdd("tags", cafeComposed)
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if changed {
		t.Error("adding the composed spelling of a stored decomposed item should be a no-op")
	}
	got, _ := m.GetList("tags")
	// The stored spelling is kept byte for byte.
	if !reflect.DeepEqual(got, []any{cafeDecomposed}) {
		t.Errorf("tags = %q, want the stored decomposed spelling only", got)
	}
}

func TestListAdd_NewKeyDedupsAcrossNormalizationForms(t *testing.T) {
	t.Parallel()
	m := NewMap()
	if _, err := m.ListAdd("tags", cafeDecomposed, cafeComposed); err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{cafeDecomposed}) {
		t.Errorf("tags = %q, want the first spelling only", got)
	}
}

func TestListRemove_KeepsRetainedItemBytesAcrossNormalizationForms(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set("tags", []any{cafeDecomposed, "go"})
	if _, err := m.ListRemove("tags", "go"); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{cafeDecomposed}) {
		t.Errorf("tags = %q, want the decomposed spelling untouched", got)
	}
}
