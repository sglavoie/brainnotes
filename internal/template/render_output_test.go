package template

import (
	"os"
	"testing"
	"time"
)

func TestRenderSubstitution(t *testing.T) {
	t.Parallel()
	body := "title: {{ .Title }}\nslug: {{ .Slug }}\nauthor: {{ .Vars.author }}\n"
	ctx := NewContext("My Note", "References", "/vault", map[string]string{"author": "Ada"}, nil)
	got := mustRender(t, body, ctx)
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
	got := mustRender(t, body, ctx)
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
	got := mustRender(t, body, ctx)
	want := "up=AB\nlo=ab\ndef=fallback\nok=value\nwl=[[Books]]\nsl=hello-world\nti=Hello World\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEnv(t *testing.T) {
	t.Setenv("BN_RENDER_TEST_VAR", "ok")
	ctx := NewContext("x", "", "/v", nil, nil)
	got := mustRender(t, `{{env "BN_RENDER_TEST_VAR"}}`, ctx)
	if got != "ok" {
		t.Fatalf("env = %q, want %q", got, "ok")
	}
}

func TestRenderEnvMissing(t *testing.T) {
	// A missing env var must render as the empty string, matching os.Getenv.
	os.Unsetenv("BN_RENDER_TEST_MISSING_VAR_XYZ")
	ctx := NewContext("x", "", "/v", nil, nil)
	got := mustRender(t, `v=[{{env "BN_RENDER_TEST_MISSING_VAR_XYZ"}}]`, ctx)
	if got != "v=[]" {
		t.Fatalf("got %q, want %q", got, "v=[]")
	}
}

func TestRenderMissingVarKey(t *testing.T) {
	t.Parallel()
	// A missing Vars key must produce an empty string, never "<no value>".
	ctx := NewContext("x", "", "/v", nil, nil)
	got := mustRender(t, `v=[{{ .Vars.missing }}]`, ctx)
	if got != "v=[]" {
		t.Fatalf("got %q, want %q", got, "v=[]")
	}
}

func TestRenderNilVarsContext(t *testing.T) {
	t.Parallel()
	// A Context constructed directly with nil Vars must still render safely.
	ctx := Context{Title: "x", Vars: nil}
	got := mustRender(t, `v=[{{ .Vars.missing }}]`, ctx)
	if got != "v=[]" {
		t.Fatalf("got %q, want %q", got, "v=[]")
	}
}

func TestRenderDateFormatBadLayout(t *testing.T) {
	t.Parallel()
	// time.Format does not error on a layout containing no reference tokens;
	// it just emits the literal layout. Pin this behavior so callers know
	// they get the raw layout back rather than a render failure.
	clk := FixedClock{T: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}
	ctx := NewContext("x", "", "/v", nil, clk)
	got := mustRender(t, `{{dateFormat "qwerty" now}}`, ctx)
	if got != "qwerty" {
		t.Fatalf("got %q, want %q", got, "qwerty")
	}
}

func TestRenderVarsEqualsInValue(t *testing.T) {
	t.Parallel()
	// .Vars values containing `=` must be returned verbatim; the CLI parses
	// `--var key=value` and is expected to split on the first `=` only. This
	// test pins the render-side behavior: whatever was stored is what comes
	// out, including `=`-containing values.
	vars := map[string]string{"q": "a=b=c"}
	ctx := NewContext("x", "", "/v", vars, nil)
	got := mustRender(t, `{{ .Vars.q }}`, ctx)
	if got != "a=b=c" {
		t.Fatalf("got %q, want %q", got, "a=b=c")
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
	got := mustRender(t, body, ctx)
	want := "---\ntitle: My Note\ntags: [a, b]\ncreated: 2024-01-01\n---\n\n# Body\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEmptyTemplate(t *testing.T) {
	t.Parallel()
	ctx := NewContext("x", "", "/v", nil, nil)
	got := mustRender(t, "", ctx)
	if got != "" {
		t.Fatalf("Render empty = %q, want %q", got, "")
	}
}
