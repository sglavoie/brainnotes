package frontmatter

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestParseBOMWithCRLF covers a UTF-8 BOM combined with Windows line endings —
// both normalisations have to happen for the opening fence to be recognised.
func TestParseBOMWithCRLF(t *testing.T) {
	in := append([]byte{0xEF, 0xBB, 0xBF}, []byte("---\r\ntitle: T\r\n---\r\nbody\r\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	if doc.Body != "body\r\n" {
		t.Errorf("body: got %q want %q", doc.Body, "body\r\n")
	}
}

// TestParseClosingFenceAtEOF accepts a closing `---` with no trailing newline
// (last byte of file).
func TestParseClosingFenceAtEOF(t *testing.T) {
	in := "---\ntitle: T\n---"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	if doc.Body != "" {
		t.Errorf("body: got %q want empty", doc.Body)
	}
}

// TestParseClosingFenceTrailingWhitespaceCRLF mirrors the open-delimiter
// tolerance for the close delimiter, including CRLF.
func TestParseClosingFenceTrailingWhitespaceCRLF(t *testing.T) {
	in := "---\r\ntitle: T\r\n--- \t\r\nbody\r\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	if doc.Body != "body\r\n" {
		t.Errorf("body: got %q want %q", doc.Body, "body\r\n")
	}
}

// TestParseEmptyFrontmatterBlock — `---\n---\n` is an empty mapping. Marshal
// then drops the empty block (no keys) and emits body-only.
func TestParseEmptyFrontmatterBlock(t *testing.T) {
	in := "---\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty front")
	}
	if doc.Body != "body\n" {
		t.Errorf("body: got %q", doc.Body)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != "body\n" {
		t.Errorf("out: got %q want %q", string(out), "body\n")
	}
}

// TestParseTopLevelNonMappingErrors guards the rough-edge fix that removed the
// doubled `frontmatter:` prefix in errors bubbled out of parseYAML.
func TestParseTopLevelNonMappingErrors(t *testing.T) {
	cases := map[string]string{
		"top-level sequence": "---\n- one\n- two\n---\nbody\n",
		"top-level scalar":   "---\nhello\n---\nbody\n",
	}
	for name, in := range cases {
		_, err := Parse([]byte(in))
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		msg := err.Error()
		// Single "frontmatter:" segment only — the inner error should not
		// repeat the package prefix.
		if strings.Count(msg, "frontmatter:") != 1 {
			t.Errorf("%s: doubled frontmatter prefix in %q", name, msg)
		}
	}
}

// TestParseBodyNoTrailingNewline verifies that a missing trailing newline is
// preserved verbatim through Marshal.
func TestParseBodyNoTrailingNewline(t *testing.T) {
	in := "---\ntitle: T\n---\nbody no newline"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Body != "body no newline" {
		t.Errorf("body: got %q", doc.Body)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasSuffix(string(out), "body no newline") {
		t.Errorf("Marshal lost body suffix: %q", string(out))
	}
}

// TestParseDateTimeNonUTC checks that timestamps with a non-UTC offset
// round-trip with their offset preserved (encodeTime should not falsely
// downgrade them to date-only).
func TestParseDateTimeNonUTC(t *testing.T) {
	in := "---\ncreated: 2025-12-13T00:00:00-05:00\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "2025-12-13T00:00:00-05:00") {
		t.Errorf("expected offset-preserving timestamp, got %q", string(out))
	}
	// Idempotent round-trip.
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}

// TestTimestampSubSecondPrecisionRoundTrip — timestamps with fractional seconds
// (e.g. milliseconds, nanoseconds) must not lose their sub-second component on
// round-trip. Before the fix, encodeTime used time.RFC3339 which silently
// dropped nanoseconds, causing data loss for timestamps like
// `2025-12-13T10:30:00.5Z`.
func TestTimestampSubSecondPrecisionRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"nanoseconds", "---\ncreated: 2025-12-13T10:30:00.123456789Z\n---\nbody\n"},
		{"milliseconds", "---\ncreated: 2025-12-13T10:30:00.500Z\n---\nbody\n"},
		{"microseconds", "---\ncreated: 2025-12-13T10:30:00.000001Z\n---\nbody\n"},
		{"fractional with offset", "---\ncreated: 2025-12-13T10:30:00.5-05:00\n---\nbody\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			tm, ok := doc.Front.GetTime("created")
			if !ok {
				t.Fatalf("created should be time.Time")
			}
			if tm.Nanosecond() == 0 {
				t.Fatalf("expected non-zero nanoseconds after parse")
			}

			out, err := Marshal(doc, nil)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			// Re-parse and verify nanoseconds are preserved.
			doc2, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse2: %v", err)
			}
			tm2, ok := doc2.Front.GetTime("created")
			if !ok {
				t.Fatalf("created should be time.Time after round-trip")
			}
			if tm.Nanosecond() != tm2.Nanosecond() {
				t.Errorf("nanoseconds lost: %d -> %d", tm.Nanosecond(), tm2.Nanosecond())
			}
			if !tm.Equal(tm2) {
				t.Errorf("timestamps differ: %v -> %v", tm, tm2)
			}

			// Idempotency.
			out2, err := Marshal(doc2, nil)
			if err != nil {
				t.Fatalf("Marshal2: %v", err)
			}
			if string(out) != string(out2) {
				t.Errorf("not idempotent:\n%q\n%q", out, out2)
			}
		})
	}
}

// TestTimestampNoSubSecondUnchanged — timestamps without fractional seconds
// must continue to emit the same format as before (no trailing `.0`).
func TestTimestampNoSubSecondUnchanged(t *testing.T) {
	in := "---\ncreated: 2025-12-13T10:30:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "2025-12-13T10:30:00Z") {
		t.Errorf("expected clean timestamp without fractional part, got %q", string(out))
	}
}

// TestParseQuotedNullString — a quoted `"null"` value must stay a string,
// not be collapsed to nil. yaml.v3 distinguishes them via the !!str tag; this
// test pins the behaviour so a future "simplification" of decodeNode does not
// silently destroy data.
func TestParseQuotedNullString(t *testing.T) {
	in := "---\nkey: \"null\"\nother: null\n---\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v, _ := doc.Front.Get("key")
	if s, ok := v.(string); !ok || s != "null" {
		t.Errorf("key: expected string %q, got %T %v", "null", v, v)
	}
	v2, _ := doc.Front.Get("other")
	if v2 != nil {
		t.Errorf("other: expected nil, got %T %v", v2, v2)
	}
}

// TestParseExplicitEmptyString — `via: ""` is an empty string, not nil.
func TestParseExplicitEmptyString(t *testing.T) {
	in := "---\nvia: \"\"\nempty:\n---\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v, _ := doc.Front.Get("via")
	if s, ok := v.(string); !ok || s != "" {
		t.Errorf("via: expected empty string, got %T %v", v, v)
	}
	v2, _ := doc.Front.Get("empty")
	if v2 != nil {
		t.Errorf("empty: expected nil, got %T %v", v2, v2)
	}
}

// TestParseAnchorAlias — YAML anchors and aliases should resolve at parse so
// downstream mutation code doesn't have to deal with alias nodes.
func TestParseAnchorAlias(t *testing.T) {
	in := "---\na: &id1 hello\nb: *id1\n---\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	va, _ := doc.Front.Get("a")
	vb, _ := doc.Front.Get("b")
	if va != "hello" || vb != "hello" {
		t.Errorf("alias did not resolve: a=%v b=%v", va, vb)
	}
}

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

// TestParseIndentedDelimiterIsBody — a leading-space `---` line is NOT a
// frontmatter delimiter (Obsidian and Markdown both require the fence at
// column 0).
func TestParseIndentedDelimiterIsBody(t *testing.T) {
	in := " ---\ntitle: T\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("indented `---` should be treated as body, not a delimiter")
	}
	if doc.Body != in {
		t.Errorf("body should equal input verbatim")
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

// TestParseEmbeddedDelimiterMultiline — a multi-line block scalar inside the
// YAML that contains an indented `---` line must not be picked up as a closing
// delimiter (isDelimLine only matches a flush-left `---`).
func TestParseEmbeddedDelimiterMultiline(t *testing.T) {
	in := "---\ndesc: |\n  Line 1\n  ---\n  Line 3\ntitle: T\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	desc, _ := doc.Front.GetString("desc")
	if !strings.Contains(desc, "---") {
		t.Errorf("desc lost embedded `---`: %q", desc)
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

// TestParseWhitespaceOnlyFrontmatter — whitespace-only content between the
// delimiters should parse as an empty Map (no keys).
func TestParseWhitespaceOnlyFrontmatter(t *testing.T) {
	in := "---\n  \n\t\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty front for whitespace-only content, got %d keys", doc.Front.Len())
	}
	if doc.Body != "body\n" {
		t.Errorf("body: got %q", doc.Body)
	}
}

// TestParseNullDocument — a YAML `null` document between the delimiters should
// parse as an empty Map, not return an error.
func TestParseNullDocument(t *testing.T) {
	in := "---\nnull\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty front for null document")
	}
	if doc.Body != "body\n" {
		t.Errorf("body: got %q", doc.Body)
	}
}

// TestParseTildeDocument — a YAML `~` (tilde, alternate null) document between
// the delimiters should parse as an empty Map.
func TestParseTildeDocument(t *testing.T) {
	in := "---\n~\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty front for tilde document")
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

// TestParseDuplicateKeys — when YAML contains duplicate keys, the last value
// wins (standard YAML behavior) and no panic occurs.
func TestParseDuplicateKeys(t *testing.T) {
	in := "---\nkey: first\nkey: second\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v, ok := doc.Front.GetString("key")
	if !ok {
		t.Fatal("key not found")
	}
	if v != "second" {
		t.Errorf("key: got %q want %q (last value should win)", v, "second")
	}
	// The key should appear only once in the keys list.
	count := 0
	for _, k := range doc.Front.Keys() {
		if k == "key" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected key to appear once in Keys(), appeared %d times", count)
	}
}
