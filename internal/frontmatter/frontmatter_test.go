package frontmatter

import (
	"errors"
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

func TestListAdd_NewKey(t *testing.T) {
	m := NewMap()
	changed, err := m.ListAdd("tags", "go", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true when creating a new list")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatalf("tags should be present as a list")
	}
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
	if !reflect.DeepEqual(m.Keys(), []string{"tags"}) {
		t.Errorf("keys: got %v want [tags]", m.Keys())
	}
}

func TestListAdd_NewKeyDeduplicatesInput(t *testing.T) {
	m := NewMap()
	changed, err := m.ListAdd("tags", "go", "go", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestListAdd_NewKeyEmptyInput(t *testing.T) {
	m := NewMap()
	changed, err := m.ListAdd("tags")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if changed {
		t.Errorf("changed should be false when no values provided")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatalf("tags should be present as an empty list")
	}
	if len(got) != 0 {
		t.Errorf("tags: got %v want empty", got)
	}
}

func TestListAdd_AppendUnique(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListAdd("tags", "yaml")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestListAdd_AppendDuplicateIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "yaml"})
	changed, err := m.ListAdd("tags", "go")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if changed {
		t.Errorf("changed should be false for an existing value")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestListAdd_AppendMixedDuplicateAndNew(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListAdd("tags", "go", "rust")
	if err != nil {
		t.Fatalf("ListAdd: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true when at least one new value is added")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "rust"}) {
		t.Errorf("tags: got %v want [go rust]", got)
	}
}

func TestListAdd_NonListReturnsError(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	_, err := m.ListAdd("title", "x")
	if err == nil {
		t.Fatalf("expected error for non-list value")
	}
	if !errors.Is(err, ErrNotAList) {
		t.Errorf("got %v want ErrNotAList", err)
	}
}

func TestListRemove_PresentValue(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "yaml", "rust"})
	changed, err := m.ListRemove("tags", "yaml")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "rust"}) {
		t.Errorf("tags: got %v want [go rust]", got)
	}
}

func TestListRemove_AbsentValueIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListRemove("tags", "yaml")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go"}) {
		t.Errorf("tags: got %v want [go]", got)
	}
}

func TestListRemove_AbsentKeyIsNoOp(t *testing.T) {
	m := NewMap()
	changed, err := m.ListRemove("tags", "go")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	if m.Has("tags") {
		t.Errorf("absent key should not be created")
	}
}

func TestListRemove_NonListReturnsError(t *testing.T) {
	m := NewMap()
	m.Set("title", "Hello")
	_, err := m.ListRemove("title", "Hello")
	if err == nil {
		t.Fatalf("expected error for non-list value")
	}
	if !errors.Is(err, ErrNotAList) {
		t.Errorf("got %v want ErrNotAList", err)
	}
}

func TestListRemove_LeavesEmptyList(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go"})
	changed, err := m.ListRemove("tags", "go")
	if err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, ok := m.GetList("tags")
	if !ok {
		t.Fatalf("tags should remain present as an empty list, not deleted")
	}
	if len(got) != 0 {
		t.Errorf("tags should be empty, got %v", got)
	}
}

func TestReplaceValue_ScalarMatch(t *testing.T) {
	m := NewMap()
	m.Set("status", "draft")
	changed, err := m.ReplaceValue("status", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetString("status")
	if got != "published" {
		t.Errorf("status: got %q want published", got)
	}
}

func TestReplaceValue_ScalarNoMatch(t *testing.T) {
	m := NewMap()
	m.Set("status", "draft")
	changed, err := m.ReplaceValue("status", "archived", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	got, _ := m.GetString("status")
	if got != "draft" {
		t.Errorf("status: got %q want draft", got)
	}
}

func TestReplaceValue_AbsentKeyIsNoOp(t *testing.T) {
	m := NewMap()
	changed, err := m.ReplaceValue("status", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	if m.Has("status") {
		t.Errorf("absent key should not be created")
	}
}

func TestReplaceValue_ListSingleMatch(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "draft", "yaml"})
	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "published", "yaml"}) {
		t.Errorf("tags: got %v want [go published yaml]", got)
	}
}

func TestReplaceValue_ListMultipleMatches(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"draft", "go", "draft"})
	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"published", "go", "published"}) {
		t.Errorf("tags: got %v want [published go published]", got)
	}
}

func TestReplaceValue_NoMatchesInList(t *testing.T) {
	m := NewMap()
	m.Set("tags", []any{"go", "yaml"})
	changed, err := m.ReplaceValue("tags", "draft", "published")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false")
	}
	got, _ := m.GetList("tags")
	if !reflect.DeepEqual(got, []any{"go", "yaml"}) {
		t.Errorf("tags: got %v want [go yaml]", got)
	}
}

func TestReplaceValue_FromEqualsToIsNoOp(t *testing.T) {
	m := NewMap()
	m.Set("status", "draft")
	changed, err := m.ReplaceValue("status", "draft", "draft")
	if err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	if changed {
		t.Errorf("changed should be false when from == to")
	}
}

func TestMutateRoundTrip(t *testing.T) {
	src := readFixture(t, "mutate.in.md")
	cfg := defaultCfg()

	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if _, err := doc.Front.ListAdd("topics", "yaml"); err != nil {
		t.Fatalf("ListAdd topics: %v", err)
	}
	if _, err := doc.Front.ListRemove("topics", "go"); err != nil {
		t.Fatalf("ListRemove topics: %v", err)
	}
	if _, err := doc.Front.ReplaceValue("status", "draft", "published"); err != nil {
		t.Fatalf("ReplaceValue status: %v", err)
	}

	got, err := Marshal(doc, cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := readFixture(t, "mutate.out.md")
	if string(got) != string(want) {
		t.Errorf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// Re-parse and confirm alphabetical order is stable.
	doc2, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	out2, err := Marshal(doc2, cfg)
	if err != nil {
		t.Fatalf("Marshal round-trip: %v", err)
	}
	if string(out2) != string(got) {
		t.Errorf("not idempotent after mutation:\nfirst:\n%s\nsecond:\n%s", got, out2)
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
