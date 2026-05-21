package template

import (
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
	}
	for _, c := range cases {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
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
