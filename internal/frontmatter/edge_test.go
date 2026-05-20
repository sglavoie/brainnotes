package frontmatter

import (
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
