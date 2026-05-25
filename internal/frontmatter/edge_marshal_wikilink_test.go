package frontmatter

import (
	"reflect"
	"strings"
	"testing"
)

// TestMarshalWikilinkListWithNonStringItem — non-string items in a wikilink-
// typed list should pass through unchanged rather than crashing or being
// silently dropped.
func TestMarshalWikilinkListWithNonStringItem(t *testing.T) {
	cfg := defaultCfg()
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("categories", []any{"Books", nil, "Other"})
	out, err := Marshal(doc, cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "[[Books]]") {
		t.Errorf("expected Books to be wrapped, got %q", string(out))
	}
	if !strings.Contains(string(out), "[[Other]]") {
		t.Errorf("expected Other to be wrapped, got %q", string(out))
	}
	if !strings.Contains(string(out), "null") {
		t.Errorf("expected nil item preserved, got %q", string(out))
	}
}

// TestMarshalDoesNotMutateStoredValues — applyWikilinkWrap must not write its
// wrapped form back into the Map; the in-memory representation stays "bare".
func TestMarshalDoesNotMutateStoredValues(t *testing.T) {
	cfg := defaultCfg()
	doc := Document{Front: NewMap(), Body: ""}
	original := []any{"Books", "Reading"}
	doc.Front.Set("categories", original)
	doc.Front.Set("parent", "Some Note")

	if _, err := Marshal(doc, cfg); err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, _ := doc.Front.GetList("categories")
	if !reflect.DeepEqual(got, []any{"Books", "Reading"}) {
		t.Errorf("Marshal mutated stored list: %v", got)
	}
	parent, _ := doc.Front.GetString("parent")
	if parent != "Some Note" {
		t.Errorf("Marshal mutated stored scalar: %q", parent)
	}
}
