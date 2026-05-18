package frontmatter

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func defaultCfg() *config.FrontmatterConfig {
	return &config.FrontmatterConfig{
		WikilinkProperties: []string{"categories", "org", "parent", "area"},
		ListProperties:     []string{"categories", "tags", "topics", "org", "type"},
	}
}

func TestParseEmpty(t *testing.T) {
	doc, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil): %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty Front, got %d keys", doc.Front.Len())
	}
	if doc.Body != "" {
		t.Errorf("expected empty Body, got %q", doc.Body)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	body := "# Heading\n\nHello world.\n"
	doc, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty Front for body-only input")
	}
	if doc.Body != body {
		t.Errorf("Body changed: got %q want %q", doc.Body, body)
	}
}

func TestParseSimple(t *testing.T) {
	in := "---\ntitle: Hello\ncount: 3\n---\n\nBody.\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "Hello" {
		t.Errorf("title: got %q want Hello", got)
	}
	v, _ := doc.Front.Get("count")
	if v != 3 {
		t.Errorf("count: got %v want 3", v)
	}
	if doc.Body != "\nBody.\n" {
		t.Errorf("Body: got %q want %q", doc.Body, "\nBody.\n")
	}
}

func TestParseWindowsLineEndings(t *testing.T) {
	in := "---\r\ntitle: Win\r\n---\r\nBody.\r\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "Win" {
		t.Errorf("title: got %q want Win", got)
	}
}

func TestParseUnterminatedFrontmatterIsBody(t *testing.T) {
	in := "---\ntitle: Hello\nbody only, no closing fence\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty Front when closer is missing")
	}
	if doc.Body != in {
		t.Errorf("Body should equal full input when frontmatter is unclosed")
	}
}

func TestParseBodyWithEmbeddedDelimiter(t *testing.T) {
	body := "\n# Header\n\nSection one.\n\n---\n\nSection two after horizontal rule.\n"
	in := "---\ntitle: With HR\n---" + body
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "With HR" {
		t.Errorf("title: got %q want With HR", got)
	}
	// The closing `---\n` line consumes the first `\n` of body. Everything
	// after that — including the embedded horizontal-rule `---` — is preserved
	// verbatim.
	wantBody := "# Header\n\nSection one.\n\n---\n\nSection two after horizontal rule.\n"
	if doc.Body != wantBody {
		t.Errorf("Body:\n got %q\nwant %q", doc.Body, wantBody)
	}
}

func TestParseDate(t *testing.T) {
	in := "---\ncreated: 2025-12-13\n---\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tm, ok := doc.Front.GetTime("created")
	if !ok {
		t.Fatalf("created should be a time.Time")
	}
	want := time.Date(2025, 12, 13, 0, 0, 0, 0, time.UTC)
	if !tm.Equal(want) {
		t.Errorf("created: got %v want %v", tm, want)
	}
}

func TestMarshalSortsKeys(t *testing.T) {
	doc := Document{Front: NewMap(), Body: "body\n"}
	doc.Front.Set("zeta", "z")
	doc.Front.Set("alpha", "a")
	doc.Front.Set("mu", "m")

	out, err := Marshal(doc, defaultCfg())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "---\nalpha: a\nmu: m\nzeta: z\n---\nbody\n"
	if string(out) != want {
		t.Errorf("Marshal:\n got %q\nwant %q", out, want)
	}
}

func TestMarshalWrapsWikilinks(t *testing.T) {
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("categories", []any{"Books", "[[Already]]"})
	doc.Front.Set("title", "X")

	out, err := Marshal(doc, defaultCfg())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "---\ncategories:\n  - '[[Books]]'\n  - '[[Already]]'\ntitle: X\n---\n"
	if string(out) != want {
		t.Errorf("Marshal:\n got %q\nwant %q", out, want)
	}
}

func TestMarshalWrapsWikilinkScalar(t *testing.T) {
	// Scalar string value on a wikilink-typed property should also be wrapped.
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("parent", "Some Note")

	out, err := Marshal(doc, defaultCfg())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "---\nparent: '[[Some Note]]'\n---\n"
	if string(out) != want {
		t.Errorf("Marshal:\n got %q\nwant %q", out, want)
	}
}

func TestMarshalEmptyFrontmatterEmitsBodyOnly(t *testing.T) {
	doc := Document{Front: NewMap(), Body: "just body\n"}
	out, err := Marshal(doc, defaultCfg())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != "just body\n" {
		t.Errorf("expected body only, got %q", out)
	}
}

func TestMarshalNilConfig(t *testing.T) {
	// nil config: no wikilink wrapping is applied.
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("categories", []any{"Books"})
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "---\ncategories:\n  - Books\n---\n"
	if string(out) != want {
		t.Errorf("Marshal:\n got %q\nwant %q", out, want)
	}
}

func TestMarshalDateKeepsISOFormat(t *testing.T) {
	doc := Document{Front: NewMap(), Body: ""}
	doc.Front.Set("created", time.Date(2025, 12, 13, 0, 0, 0, 0, time.UTC))
	out, err := Marshal(doc, defaultCfg())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "---\ncreated: 2025-12-13\n---\n"
	if string(out) != want {
		t.Errorf("Marshal:\n got %q\nwant %q", out, want)
	}
}

func TestRoundTripIdempotent(t *testing.T) {
	src := readFixture(t, "book.in.md")
	cfg := defaultCfg()

	doc1, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse first: %v", err)
	}
	out1, err := Marshal(doc1, cfg)
	if err != nil {
		t.Fatalf("Marshal first: %v", err)
	}

	doc2, err := Parse(out1)
	if err != nil {
		t.Fatalf("Parse second: %v", err)
	}
	out2, err := Marshal(doc2, cfg)
	if err != nil {
		t.Fatalf("Marshal second: %v", err)
	}

	if string(out1) != string(out2) {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", out1, out2)
	}
}

func TestGoldenBookFixture(t *testing.T) {
	src := readFixture(t, "book.in.md")
	want := readFixture(t, "book.out.md")

	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Marshal(doc, defaultCfg())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMapOrderAndMutations(t *testing.T) {
	m := NewMap()
	m.Set("b", 2)
	m.Set("a", 1)
	m.Set("c", 3)

	if got := m.Keys(); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("insertion order: got %v", got)
	}
	if !m.Has("a") {
		t.Errorf("Has(a) should be true")
	}
	m.Unset("a")
	if m.Has("a") {
		t.Errorf("Has(a) should be false after Unset")
	}
	if got := m.Keys(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("post-unset order: got %v", got)
	}
	// Re-setting an existing key keeps the slot, not appends.
	m.Set("b", 22)
	if got := m.Keys(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("post-update order: got %v", got)
	}
	v, _ := m.Get("b")
	if v != 22 {
		t.Errorf("Get(b) after update: got %v want 22", v)
	}
}

func TestGetWikilinkList(t *testing.T) {
	m := NewMap()
	m.Set("categories", []any{"[[Books]]", "[[Reading]]"})
	m.Set("parent", "[[Parent Note]]")
	m.Set("missing_key_check", nil)

	got, ok := m.GetWikilinkList("categories")
	if !ok {
		t.Fatalf("categories: not present")
	}
	if !reflect.DeepEqual(got, []string{"Books", "Reading"}) {
		t.Errorf("categories: got %v", got)
	}
	got, ok = m.GetWikilinkList("parent")
	if !ok {
		t.Fatalf("parent: not present")
	}
	if !reflect.DeepEqual(got, []string{"Parent Note"}) {
		t.Errorf("parent: got %v", got)
	}
	got, ok = m.GetWikilinkList("missing_key_check")
	if !ok {
		t.Fatalf("missing_key_check should report present")
	}
	if len(got) != 0 {
		t.Errorf("nil value should give empty slice, got %v", got)
	}
	if _, ok := m.GetWikilinkList("not-there"); ok {
		t.Errorf("absent key should report not present")
	}
}

func TestGetTypedAccessors(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	m.Set("tags", []any{"a", "b"})
	m.Set("created", time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC))

	if s, ok := m.GetString("title"); !ok || s != "Hello" {
		t.Errorf("GetString(title): %q %v", s, ok)
	}
	if _, ok := m.GetString("tags"); ok {
		t.Errorf("GetString on list should report false")
	}
	if l, ok := m.GetList("tags"); !ok || len(l) != 2 {
		t.Errorf("GetList(tags): %v %v", l, ok)
	}
	if tm, ok := m.GetTime("created"); !ok || tm.Year() != 2026 {
		t.Errorf("GetTime(created): %v %v", tm, ok)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("testdata", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}
