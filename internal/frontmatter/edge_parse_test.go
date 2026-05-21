package frontmatter

import (
	"strings"
	"testing"
	"time"
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

// TestParseNestedDuplicateKeys — duplicate keys inside a nested map must
// behave like top-level duplicates: last value wins, no error. Before the fix,
// decodeNode used n.Decode(&v) for MappingNode which delegated to yaml.v3's
// strict decoder and errored on duplicate keys, while top-level dup keys were
// silently accepted (last wins). The inconsistency made it impossible to read
// notes whose nested frontmatter happened to repeat a key.
func TestParseNestedDuplicateKeys(t *testing.T) {
	in := "---\nmeta:\n  key: first\n  key: second\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	meta, _ := doc.Front.Get("meta")
	m, ok := meta.(map[string]any)
	if !ok {
		t.Fatalf("meta: got %T want map[string]any", meta)
	}
	if v := m["key"]; v != "second" {
		t.Errorf("nested key: got %v want \"second\" (last wins)", v)
	}
}

// TestParseNestedPreservesScalarTypes — the manual nested-map decoder must
// preserve scalar types (int, float64, bool, time.Time) the same way the
// top-level decoder does. A regression here would silently change types of
// values nested inside a map property.
func TestParseNestedPreservesScalarTypes(t *testing.T) {
	in := "---\nmeta:\n  count: 5\n  rate: 3.0\n  flag: true\n  ts: 2025-12-13T10:30:00Z\n  date: 2025-12-13\n  s: hello\n  n: ~\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	meta, _ := doc.Front.Get("meta")
	m, ok := meta.(map[string]any)
	if !ok {
		t.Fatalf("meta: got %T want map[string]any", meta)
	}
	if _, ok := m["count"].(int); !ok {
		t.Errorf("count: got %T want int", m["count"])
	}
	if _, ok := m["rate"].(float64); !ok {
		t.Errorf("rate: got %T want float64", m["rate"])
	}
	if _, ok := m["flag"].(bool); !ok {
		t.Errorf("flag: got %T want bool", m["flag"])
	}
	if _, ok := m["ts"].(time.Time); !ok {
		t.Errorf("ts: got %T want time.Time", m["ts"])
	}
	if _, ok := m["date"].(time.Time); !ok {
		t.Errorf("date: got %T want time.Time", m["date"])
	}
	if _, ok := m["s"].(string); !ok {
		t.Errorf("s: got %T want string", m["s"])
	}
	if m["n"] != nil {
		t.Errorf("n: got %T %v want nil", m["n"], m["n"])
	}
}

// TestParseNestedAnchorAlias — an alias inside a nested map must resolve at
// parse, just like at the top level.
func TestParseNestedAnchorAlias(t *testing.T) {
	in := "---\nmeta:\n  a: &id hello\n  b: *id\n---\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	meta, _ := doc.Front.Get("meta")
	m, ok := meta.(map[string]any)
	if !ok {
		t.Fatalf("meta: got %T", meta)
	}
	if m["a"] != "hello" || m["b"] != "hello" {
		t.Errorf("nested alias did not resolve: a=%v b=%v", m["a"], m["b"])
	}
}

// TestParseNestedListWithNullItem — null inside a list inside a nested map
// must decode to Go nil, not be silently dropped or stringified.
func TestParseNestedListWithNullItem(t *testing.T) {
	in := "---\nmeta:\n  items:\n    - ~\n    - x\n---\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	meta, _ := doc.Front.Get("meta")
	m := meta.(map[string]any)
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items: got %T", m["items"])
	}
	if len(items) != 2 {
		t.Fatalf("items: got %d want 2", len(items))
	}
	if items[0] != nil {
		t.Errorf("items[0]: got %v want nil", items[0])
	}
	if items[1] != "x" {
		t.Errorf("items[1]: got %v want \"x\"", items[1])
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
