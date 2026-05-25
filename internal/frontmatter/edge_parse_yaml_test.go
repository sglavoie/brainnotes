package frontmatter

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

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

// TestParseCyclicAliasErrors ensures self-referential anchors fail fast instead
// of recursing forever. Before the fix, decodeNodeAt followed alias targets with
// no cycle detection, so inputs like `a: &x [*x]` hung Parse indefinitely.
func TestParseCyclicAliasErrors(t *testing.T) {
	cases := map[string]string{
		"self-referential sequence": "---\na: &x [*x]\n---\n",
		"self-referential mapping":  "---\na: &x {self: *x}\n---\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(in))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "cyclic alias") {
				t.Fatalf("expected cyclic alias error, got %v", err)
			}
		})
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
	in := "---\nfirst: keep\nkey: first\nbetween: stay\nkey: second\n---\nbody\n"
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
	if got := doc.Front.Keys(); !reflect.DeepEqual(got, []string{"first", "key", "between"}) {
		t.Errorf("Keys(): got %v want %v", got, []string{"first", "key", "between"})
	}
	if got := doc.Front.Len(); got != 3 {
		t.Errorf("Len(): got %d want 3", got)
	}
}
