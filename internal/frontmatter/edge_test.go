package frontmatter

import (
	"math"
	"reflect"
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

// TestMarshalWholeNumberFloatPreservesType — a whole-number float (e.g. `3.0`)
// must round-trip as a float, not an int. yaml.v3's default encoding emits the
// scalar `3`, which the parser then reads back as int and silently changes the
// value's type — causing spurious writes for notes whose frontmatter has
// `rating: 3.0` and other whole-number floats.
func TestMarshalWholeNumberFloatPreservesType(t *testing.T) {
	in := "---\nrating: 3.0\ncount: 3\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rating, _ := doc.Front.Get("rating")
	if _, ok := rating.(float64); !ok {
		t.Fatalf("rating parsed as %T, want float64", rating)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "rating: 3.0") {
		t.Errorf("rating: expected `3.0` in output, got %q", string(out))
	}

	// Re-parse and verify the type round-trips.
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	rating2, _ := doc2.Front.Get("rating")
	if _, ok := rating2.(float64); !ok {
		t.Errorf("rating re-parsed as %T, want float64", rating2)
	}
	count2, _ := doc2.Front.Get("count")
	if _, ok := count2.(int); !ok {
		t.Errorf("count re-parsed as %T, want int (should not be promoted)", count2)
	}

	// Idempotency: a second marshal must produce identical bytes.
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\nfirst: %q\nsecond: %q", out, out2)
	}
}

// TestMarshalFractionalFloatRoundTrip — non-whole-number floats already round-
// trip correctly under yaml.v3's default encoder; the new encoder must not
// regress that path.
func TestMarshalFractionalFloatRoundTrip(t *testing.T) {
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("half", 0.5)
	doc.Front.Set("big", 1.5e10)

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, _ := doc2.Front.Get("half"); v != 0.5 {
		t.Errorf("half: got %T %v want 0.5", v, v)
	}
	if v, _ := doc2.Front.Get("big"); v != 1.5e10 {
		t.Errorf("big: got %T %v want 1.5e10", v, v)
	}
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}

// TestMarshalSpecialFloats — NaN and +/-Inf must serialise with YAML's
// canonical spellings (.nan / .inf / -.inf) and round-trip back as float64.
func TestMarshalSpecialFloats(t *testing.T) {
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("nan", math.NaN())
	doc.Front.Set("pinf", math.Inf(1))
	doc.Front.Set("ninf", math.Inf(-1))

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), ".nan") {
		t.Errorf("NaN: expected .nan token in %q", string(out))
	}
	if !strings.Contains(string(out), ".inf") {
		t.Errorf("+Inf: expected .inf token in %q", string(out))
	}
	if !strings.Contains(string(out), "-.inf") {
		t.Errorf("-Inf: expected -.inf token in %q", string(out))
	}

	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	nan, _ := doc2.Front.Get("nan")
	nf, ok := nan.(float64)
	if !ok || !math.IsNaN(nf) {
		t.Errorf("nan: got %T %v want NaN", nan, nan)
	}
	pinf, _ := doc2.Front.Get("pinf")
	pf, ok := pinf.(float64)
	if !ok || !math.IsInf(pf, 1) {
		t.Errorf("pinf: got %T %v want +Inf", pinf, pinf)
	}
	ninf, _ := doc2.Front.Get("ninf")
	nf2, ok := ninf.(float64)
	if !ok || !math.IsInf(nf2, -1) {
		t.Errorf("ninf: got %T %v want -Inf", ninf, ninf)
	}
}

// TestMarshalNestedMapFloatPreservesType — a whole-number float inside a nested
// map (e.g. `meta:\n  rating: 3.0`) must round-trip as float64, not silently
// drift to int. Before the fix, encodeValue fell through to yaml.Node.Encode
// for map[string]any values, bypassing the custom encodeFloat logic.
func TestMarshalNestedMapFloatPreservesType(t *testing.T) {
	in := "---\nmeta:\n  count: 3\n  rating: 3.0\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "rating: 3.0") {
		t.Errorf("expected rating: 3.0, got:\n%s", out)
	}

	// Re-parse: the float must stay float64, the int must stay int.
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	meta, _ := doc2.Front.Get("meta")
	m := meta.(map[string]any)
	if _, ok := m["rating"].(float64); !ok {
		t.Errorf("rating re-parsed as %T, want float64", m["rating"])
	}
	if _, ok := m["count"].(int); !ok {
		t.Errorf("count re-parsed as %T, want int", m["count"])
	}

	// Idempotency.
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\nfirst:  %q\nsecond: %q", out, out2)
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

// TestMarshalListOfMapsWithFloat — floats inside maps that are themselves
// inside a sequence must also get the encodeFloat treatment.
func TestMarshalListOfMapsWithFloat(t *testing.T) {
	in := "---\nitems:\n  - name: A\n    rating: 3.0\n  - name: B\n    rating: 4.0\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "rating: 3.0") {
		t.Errorf("expected rating: 3.0, got:\n%s", out)
	}
	if !strings.Contains(string(out), "rating: 4.0") {
		t.Errorf("expected rating: 4.0, got:\n%s", out)
	}

	// Idempotency.
	doc2, _ := Parse(out)
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%s\nvs\n%s", out, out2)
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

// TestMarshalNegativeFloat — negative whole-number floats must preserve the
// float type (e.g. -3.0 must not become -3 int).
func TestMarshalNegativeFloat(t *testing.T) {
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("val", -3.0)
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "-3.0") {
		t.Errorf("expected -3.0, got %q", string(out))
	}
	doc2, _ := Parse(out)
	v, _ := doc2.Front.Get("val")
	if _, ok := v.(float64); !ok {
		t.Errorf("val re-parsed as %T, want float64", v)
	}
}

// TestMarshalZeroFloat — float64(0) must be preserved as a float, not become
// int(0) on re-parse.
func TestMarshalZeroFloat(t *testing.T) {
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("val", 0.0)
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "0.0") {
		t.Errorf("expected 0.0, got %q", string(out))
	}
	doc2, _ := Parse(out)
	v, _ := doc2.Front.Get("val")
	if _, ok := v.(float64); !ok {
		t.Errorf("val re-parsed as %T, want float64", v)
	}
}

// TestMarshalEmptyFrontPreservesBodyStartingWithDelimiter — when the Front map
// becomes empty (e.g. after Unset removes all keys) and the body happens to
// begin with a parsable frontmatter block, Marshal must emit an empty
// `---\n---\n` block so the body is not silently reinterpreted as the new
// frontmatter on the next Parse. Before the fix, a doc whose body began with
// `---\nother: yaml\n---\n...` would round-trip into a doc whose Front contained
// `{other: yaml}` and a truncated body — silent corruption.
func TestMarshalEmptyFrontPreservesBodyStartingWithDelimiter(t *testing.T) {
	src := "---\nfoo: bar\n---\n---\nother: yaml\n---\nreal body\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Front.Unset("foo")
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	if !doc2.Front.IsEmpty() {
		t.Errorf("Front should remain empty after round-trip; got keys=%v", doc2.Front.Keys())
	}
	wantBody := "---\nother: yaml\n---\nreal body\n"
	if doc2.Body != wantBody {
		t.Errorf("Body corrupted:\n got %q\nwant %q", doc2.Body, wantBody)
	}

	// Idempotency: marshal again must produce the same bytes.
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}
