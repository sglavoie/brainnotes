package queryexpr

import (
	"strings"
	"testing"
)

// A `-` written directly against a number after a comparison operator is a
// negative literal, not negation: negation cannot appear in value position.
// The canonical form quotes it, since a leading `-` elsewhere is negation.
func TestParse_NegativeNumberAfterOperator(t *testing.T) {
	tests := []struct {
		src      string
		want     string
		wantText string
	}{
		{src: "rating>-1", want: `rating>"-1"`, wantText: "-1"},
		{src: "prop:x>=-2.5", want: `prop:x>="-2.5"`, wantText: "-2.5"},
		{src: "score<-.5", want: `score<"-.5"`, wantText: "-.5"},
		{src: "due>-7d", want: `due>"-7d"`, wantText: "-7d"},
		{src: "rating=-3 and -tag:x", want: `rating="-3" and not tag:x`, wantText: "-3"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			expr, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.src, err)
			}
			if got := expr.String(); got != tc.want {
				t.Fatalf("Parse(%q).String() = %q, want %q", tc.src, got, tc.want)
			}
			term := firstTerm(t, expr)
			if term.Value.Atoms[0].Text != tc.wantText || term.Value.Atoms[0].Quoted {
				t.Fatalf("value atom = %+v, want unquoted %q", term.Value.Atoms[0], tc.wantText)
			}
			if _, err := Parse(expr.String()); err != nil {
				t.Fatalf("canonical form %q does not reparse: %v", expr.String(), err)
			}
		})
	}
}

func TestParse_NegativeNumberStaysStrict(t *testing.T) {
	for _, src := range []string{
		"rating>- 1",  // a gap after the minus makes it negation, not a value
		"rating>-foo", // only a numeric word joins the minus
		"rating>-",
	} {
		if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "expected a value") {
			t.Errorf("Parse(%q) error = %v, want an expected-a-value error", src, err)
		}
	}
}

func TestTyped_NegativeLiteralsCompare(t *testing.T) {
	root, notes, env := typedVault(t)
	cases := []struct {
		src  string
		want []string
	}{
		{"rating>-1", []string{"A.md", "B.md", "D.md"}},
		{"rating> -1", []string{"A.md", "B.md", "D.md"}},
		{`rating>"-1"`, []string{"A.md", "B.md", "D.md"}},
		{"score>=-2.5", []string{"A.md", "B.md"}},
		{"prop:tally>-4", []string{"D.md"}},
		{"due>-7d", []string{"A.md"}},
	}
	for _, tc := range cases {
		got := matchPaths(t, tc.src, root, notes, env)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%q matched %v, want %v", tc.src, got, tc.want)
		}
	}
}

func firstTerm(t *testing.T, expr Expr) *Term {
	t.Helper()
	switch e := expr.(type) {
	case *Term:
		return e
	case *And:
		return firstTerm(t, e.Operands[0])
	}
	t.Fatalf("unexpected expression %T", expr)
	return nil
}
