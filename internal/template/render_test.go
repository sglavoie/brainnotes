package template

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello-world"},
		{"  Foo  Bar  ", "foo-bar"},
		{"snake_case.value", "snake-case-value"},
		{"Trailing!!!", "trailing"},
		{"Numbers 123 mix", "numbers-123-mix"},
		{"", ""},
		{"---", ""},
		// Leading punctuation must not produce a leading dash.
		{"-foo", "foo"},
		{"!!!foo", "foo"},
		// Unicode letters are lowercased and preserved (not stripped).
		{"Café 2", "café-2"},
		// Only whitespace/punctuation collapses to empty after trim.
		{"   \t\n  ", ""},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"hello world", "Hello World"},
		{"already Capital", "Already Capital"},
		{"  spaced  out", "  Spaced  Out"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderSubstitution(t *testing.T) {
	t.Parallel()
	body := "title: {{ .Title }}\nslug: {{ .Slug }}\nauthor: {{ .Vars.author }}\n"
	ctx := NewContext("My Note", "References", "/vault", map[string]string{"author": "Ada"}, nil)
	got, err := Render(body, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "title: My Note\nslug: my-note\nauthor: Ada\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderDateDeterministic(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2024, 3, 14, 15, 9, 26, 0, time.UTC)
	ctx := NewContext("Pi Day", "", "/vault", nil, FixedClock{T: fixed})

	body := `today={{today}}
year={{dateFormat "2006" .Date}}
ts={{dateFormat "15:04:05" (now)}}
`
	got, err := Render(body, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "today=2024-03-14\nyear=2024\nts=15:09:26\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderFuncs(t *testing.T) {
	t.Parallel()
	ctx := NewContext("ok", "", "/vault", nil, FixedClock{T: time.Unix(0, 0).UTC()})
	body := `up={{upper "ab"}}
lo={{lower "AB"}}
def={{default "fallback" ""}}
ok={{default "fallback" "value"}}
wl={{wikilink "Books"}}
sl={{slug "Hello World"}}
ti={{title "hello world"}}
`
	got, err := Render(body, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "up=AB\nlo=ab\ndef=fallback\nok=value\nwl=[[Books]]\nsl=hello-world\nti=Hello World\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderParseError(t *testing.T) {
	t.Parallel()
	ctx := NewContext("x", "", "/v", nil, nil)
	_, err := Render("{{ .Unclosed", ctx)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestRenderEnv(t *testing.T) {
	t.Setenv("BN_RENDER_TEST_VAR", "ok")
	ctx := NewContext("x", "", "/v", nil, nil)
	got, err := Render(`{{env "BN_RENDER_TEST_VAR"}}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "ok" {
		t.Fatalf("env = %q, want %q", got, "ok")
	}
}

func TestRenderMissingVarKey(t *testing.T) {
	t.Parallel()
	// A missing Vars key must produce an empty string, never "<no value>".
	ctx := NewContext("x", "", "/v", nil, nil)
	got, err := Render(`v=[{{ .Vars.missing }}]`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "v=[]" {
		t.Fatalf("got %q, want %q", got, "v=[]")
	}
}

func TestRenderNilVarsContext(t *testing.T) {
	t.Parallel()
	// A Context constructed directly with nil Vars must still render safely.
	ctx := Context{Title: "x", Vars: nil}
	got, err := Render(`v=[{{ .Vars.missing }}]`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "v=[]" {
		t.Fatalf("got %q, want %q", got, "v=[]")
	}
}

func TestRenderExecuteError(t *testing.T) {
	t.Parallel()
	ctx := NewContext("x", "", "/v", nil, nil)
	// Calling a known function with the wrong number of args triggers an
	// execute error (dateFormat expects two args).
	_, err := Render(`{{ dateFormat "2006" }}`, ctx)
	if err == nil {
		t.Fatal("expected execute error")
	}
	if !strings.Contains(err.Error(), "execute") && !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected execute or parse error, got %v", err)
	}
}

func TestNewContextNilVars(t *testing.T) {
	t.Parallel()
	ctx := NewContext("t", "", "/v", nil, nil)
	if ctx.Vars == nil {
		t.Fatal("Vars must not be nil")
	}
}

func TestTitleUnicode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"café latte", "Café Latte"},
		{"über cool", "Über Cool"},
		// Digit at word start stays as-is (ToUpper is identity for digits).
		{"123 abc", "123 Abc"},
		// A leading combining mark must not consume the word start; the first
		// real letter still gets capitalized.
		{"́test", "́Test"},
	}
	for _, c := range cases {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugCombiningMarks(t *testing.T) {
	t.Parallel()
	// NFD-normalized "Café" is C + a + f + e + U+0301 (COMBINING ACUTE ACCENT).
	// The combining mark must stay attached to the base letter, producing
	// "café" not "caf-e".
	cases := []struct {
		in, want string
	}{
		// NFD "Café 2": e + combining acute accent must produce café, not caf-e.
		{"Café 2", "café-2"},
		// NFD "über": u + combining diaeresis.
		{"über", "über"},
		// Multiple combining marks on one base letter (e.g. Vietnamese).
		{"ạ́b", "ạ́b"},
		// Orphan combining mark at start (pathological but should not leak
		// into the slug).
		{"́test", "test"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugTrailingOrphanMark(t *testing.T) {
	t.Parallel()
	// A combining mark that ends up at the end of the output after a
	// separator-induced dash is an orphan (no preceding base letter). It
	// must be trimmed along with the dash, instead of producing artifacts
	// like "foo-́".
	cases := []struct {
		in, want string
	}{
		{"foo!́", "foo"},
		{"abc-́", "abc"},
		{"abc-́́́", "abc"}, // multiple orphan marks
		{"abc-́def", "abc-def"},
		// Mark attached to a real base letter must be preserved.
		{"abć", "abć"},
		// Leading orphan mark is skipped.
		{"́test", "test"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugSingleChar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"A", "a"},
		{"1", "1"},
		{"-", ""},
		{".", ""},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderEnvMissing(t *testing.T) {
	// A missing env var must render as the empty string, matching os.Getenv.
	os.Unsetenv("BN_RENDER_TEST_MISSING_VAR_XYZ")
	ctx := NewContext("x", "", "/v", nil, nil)
	got, err := Render(`v=[{{env "BN_RENDER_TEST_MISSING_VAR_XYZ"}}]`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "v=[]" {
		t.Fatalf("got %q, want %q", got, "v=[]")
	}
}

func TestRenderUnknownFunction(t *testing.T) {
	t.Parallel()
	// Referencing a non-existent helper must produce a parse error.
	ctx := NewContext("x", "", "/v", nil, nil)
	_, err := Render(`{{ nosuchfunction "x" }}`, ctx)
	if err == nil {
		t.Fatal("expected parse error for unknown function")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestRenderYAMLFrontmatterBody(t *testing.T) {
	t.Parallel()
	// A template body that contains a YAML frontmatter block must be
	// rendered verbatim aside from {{...}} substitutions; the renderer must
	// not strip or reformat the YAML.
	clk := FixedClock{T: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := NewContext("My Note", "Refs", "/v", nil, clk)
	body := "---\ntitle: {{.Title}}\ntags: [a, b]\ncreated: {{today}}\n---\n\n# Body\n"
	got, err := Render(body, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "---\ntitle: My Note\ntags: [a, b]\ncreated: 2024-01-01\n---\n\n# Body\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEmptyTemplate(t *testing.T) {
	t.Parallel()
	ctx := NewContext("x", "", "/v", nil, nil)
	got, err := Render("", ctx)
	if err != nil {
		t.Fatalf("Render empty: %v", err)
	}
	if got != "" {
		t.Fatalf("Render empty = %q, want %q", got, "")
	}
}

func TestNewContextDateTruncation(t *testing.T) {
	t.Parallel()
	// Time with non-zero hours/minutes/seconds should truncate to midnight.
	clk := FixedClock{T: time.Date(2024, 12, 25, 14, 30, 59, 123456789, time.UTC)}
	ctx := NewContext("Christmas", "", "/v", nil, clk)
	want := time.Date(2024, 12, 25, 0, 0, 0, 0, time.UTC)
	if !ctx.Date.Equal(want) {
		t.Fatalf("Date = %v, want %v", ctx.Date, want)
	}
	// Time field should preserve full precision.
	if !ctx.Time.Equal(clk.T) {
		t.Fatalf("Time = %v, want %v", ctx.Time, clk.T)
	}
}

func TestNewContextSlugGeneration(t *testing.T) {
	t.Parallel()
	ctx := NewContext("My Great Note", "", "/v", nil, nil)
	if ctx.Slug != "my-great-note" {
		t.Fatalf("Slug = %q, want %q", ctx.Slug, "my-great-note")
	}
}

func TestRenderAllFieldsAccessible(t *testing.T) {
	t.Parallel()
	clk := FixedClock{T: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)}
	ctx := NewContext("Test Note", "subfolder", "/my/vault", map[string]string{"k": "v"}, clk)
	body := "T={{.Title}} S={{.Slug}} F={{.Folder}} V={{.Vault}} K={{.Vars.k}}"
	got, err := Render(body, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "T=Test Note S=test-note F=subfolder V=/my/vault K=v"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestWikilinkNoDoubleWrap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Books", "[[Books]]"},
		{"[[Books]]", "[[Books]]"},
		{"  [[Books]]  ", "  [[Books]]  "},
		{"", ""},
		{"[[", "[[[[]]"},         // sentinel: too short to be a real link
		{"[[a]]b", "[[[[a]]b]]"}, // not fully wrapped — wrap again
		// Two separate wikilinks are not "already wrapped" — must wrap.
		{"[[a]] and [[b]]", "[[[[a]] and [[b]]]]"},
		// Closing ]] before end means outer pair is not a single wrap.
		{"[[a]]b]]", "[[[[a]]b]]]]"},
		// Single valid wikilink with heading/alias.
		{"[[Page#Heading|Alias]]", "[[Page#Heading|Alias]]"},
		// Empty wikilink [[]] is treated as already wrapped (len==4).
		{"[[]]", "[[]]"},
		// Whitespace-only must not be wrapped into an invalid wikilink.
		{"   ", "   "},
		{"\t", "\t"},
		{" \n ", " \n "},
		// Inner [[ means the outer [[ ]] does NOT form a single wrap —
		// must wrap rather than be detected as already-wrapped.
		{"[[a[[b]]", "[[[[a[[b]]]]"},
	}
	ctx := NewContext("x", "", "/v", nil, FixedClock{T: time.Unix(0, 0).UTC()})
	for _, c := range cases {
		body := `{{wikilink .Vars.v}}`
		ctx.Vars = map[string]string{"v": c.in}
		got, err := Render(body, ctx)
		if err != nil {
			t.Fatalf("Render(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("wikilink(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
