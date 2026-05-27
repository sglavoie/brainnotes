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

func TestMarshalWikilinkStringSliceWrapsValues(t *testing.T) {
	cfg := defaultCfg()
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("categories", []string{"Books", "Reading"})
	out, err := Marshal(doc, cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "- '[[Books]]'") {
		t.Fatalf("expected Books to be wrapped, got %q", s)
	}
	if !strings.Contains(s, "- '[[Reading]]'") {
		t.Fatalf("expected Reading to be wrapped, got %q", s)
	}
}

func TestMarshalWikilinkStringSliceDoesNotMutateStoredValues(t *testing.T) {
	cfg := defaultCfg()
	doc := Document{Front: NewMap(), Body: ""}
	original := []string{"Books", "Reading"}
	doc.Front.Set("categories", original)
	if _, err := Marshal(doc, cfg); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, ok := doc.Front.Get("categories")
	if !ok {
		t.Fatal("categories key missing after Marshal")
	}
	want := []string{"Books", "Reading"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Marshal mutated stored []string: got %#v want %#v", got, want)
	}
}
