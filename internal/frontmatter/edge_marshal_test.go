package frontmatter

import (
	"strings"
	"testing"
)

// TestMarshalNestedMapDeterministic — nested maps must produce a stable byte
// stream across many Marshal calls; CLAUDE.md guarantees "stable diffs".
func TestMarshalNestedMapDeterministic(t *testing.T) {
	in := "---\nauthor:\n  zeta: z\n  alpha: a\n  mu: m\n---\nbody\n"
	results := map[string]struct{}{}
	for i := 0; i < 30; i++ {
		doc, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		out, err := Marshal(doc, nil)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		results[string(out)] = struct{}{}
	}
	if len(results) != 1 {
		t.Errorf("Marshal produced %d distinct outputs for nested map; want 1", len(results))
	}
}

// TestMarshalListOfMaps — verifies a sequence whose items are mappings is
// rendered in block style end-to-end (forceBlockStyle must recurse).
func TestMarshalListOfMaps(t *testing.T) {
	in := "---\nitems:\n  - name: A\n    qty: 1\n  - name: B\n    qty: 2\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "{") || strings.Contains(string(out), "[") {
		t.Errorf("expected block style only, got flow-style markers in %q", string(out))
	}
	// Idempotent.
	doc2, _ := Parse(out)
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%s\n%s", out, out2)
	}
}

// TestMarshalNestedMapDateKeepsISO — a date-only value inside a nested map must
// retain the YYYY-MM-DD format, not be expanded to a full RFC3339 timestamp.
func TestMarshalNestedMapDateKeepsISO(t *testing.T) {
	in := "---\nmeta:\n  created: 2025-12-13\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "created: 2025-12-13") {
		t.Errorf("expected date-only format, got:\n%s", out)
	}
	if strings.Contains(string(out), "T00:00:00") {
		t.Errorf("date should not be expanded to full timestamp, got:\n%s", out)
	}

	// Idempotency.
	doc2, _ := Parse(out)
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}

// TestMarshalEmptyNestedMap — an empty nested map should render as `{}` (flow
// style), matching the convention for empty sequences.
func TestMarshalEmptyNestedMap(t *testing.T) {
	doc := Document{Front: NewMap(), Body: "body\n"}
	doc.Front.Set("meta", map[string]any{})
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "meta: {}") {
		t.Errorf("expected empty map as {}, got:\n%s", out)
	}

	// Round-trip: re-parse must preserve the empty map, not collapse it to nil.
	doc2, _ := Parse(out)
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}
