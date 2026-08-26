package queryexpr

import (
	"reflect"
	"testing"
)

// TestParse_CanonicalStringRoundTrips is the round-trip contract:
// Expr.String() re-renders canonical source, and reparsing that source yields
// a tree that renders identically. Canonicalization is therefore idempotent
// even where it is not byte-preserving (`==` becomes `=`, juxtaposition
// becomes an explicit `and`, keyword case is folded).
func TestParse_CanonicalStringRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "empty query is the identity", src: "", want: ""},
		{name: "whitespace-only query is the identity", src: "   \t ", want: ""},
		{name: "bare word", src: "inbox", want: "inbox"},
		{name: "phrase keeps its quotes", src: `"deep work"`, want: `"deep work"`},
		{name: "bare wikilink", src: "[[Deep Work]]", want: "[[Deep Work]]"},
		{name: "field match", src: "tag:foo", want: "tag:foo"},
		{name: "juxtaposition is and", src: "a b", want: "a and b"},
		{name: "explicit and", src: "a and b", want: "a and b"},
		{name: "keyword case folds", src: "a AND b OR c", want: "a and b or c"},
		{name: "and binds tighter than or", src: "a or b and c", want: "a or b and c"},
		{name: "and binds tighter than or, left", src: "a and b or c", want: "a and b or c"},
		{name: "a group around or survives under and", src: "(a or b) and c", want: "(a or b) and c"},
		{name: "a redundant group around and is dropped", src: "(a and b) or c", want: "a and b or c"},
		{name: "not binds tighter than and", src: "not a and b", want: "not a and b"},
		{name: "not over a group keeps the group", src: "not (a and b)", want: "not (a and b)"},
		{name: "not over a disjunction keeps the group", src: "not (a or b)", want: "not (a or b)"},
		{name: "hyphen is not", src: "-tag:foo", want: "not tag:foo"},
		{name: "double negation", src: "not not a", want: "not not a"},
		{name: "hyphen and keyword mix", src: "-a and not b", want: "not a and not b"},
		{name: "strict greater than", src: "backlinks>3", want: "backlinks>3"},
		{name: "greater or equal", src: "backlinks>=3", want: "backlinks>=3"},
		{name: "not equal", src: "status!=wip", want: "status!=wip"},
		{name: "double equals canonicalizes", src: "backlinks==3", want: "backlinks=3"},
		{name: "argument via colon", src: "age:modified>90d", want: "age:modified>90d"},
		{name: "quoted argument via colon", src: `near:"Deep Work"<=2`, want: `near:"Deep Work"<=2`},
		{name: "wikilink argument via colon", src: "near:[[Deep Work]]<=2", want: "near:[[Deep Work]]<=2"},
		{name: "no argument, quoted value", src: `links-to:"Deep Work"`, want: `links-to:"Deep Work"`},
		{name: "parenthesised argument, no operator", src: `links-to("Deep Work")`, want: `links-to("Deep Work")`},
		{name: "parenthesised argument with operator", src: `near("Deep Work")<=2`, want: `near:"Deep Work"<=2`},
		{name: "list is any-of", src: "tag:[a,b,c]", want: "tag:[a, b, c]"},
		{name: "list of wikilinks stays spaced", src: "tag:[ [[A]], [[B]] ]", want: "tag:[ [[A]], [[B]]]"},
		{name: "list with a phrase", src: `status:["in progress",wip]`, want: `status:["in progress", wip]`},
		{
			name: "the worked example from the plan",
			src:  `near:[[Deep Work]]<=2 and status:wip and not folder:Archive`,
			want: `near:[[Deep Work]]<=2 and status:wip and not folder:Archive`,
		},
		{
			name: "mixed precedence with groups",
			src:  `(tag:foo or tag:bar) and -folder:Archive and "deep work"`,
			want: `(tag:foo or tag:bar) and not folder:Archive and "deep work"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.src, err)
			}
			got := expr.String()
			if got != tc.want {
				t.Fatalf("Parse(%q).String() = %q, want %q", tc.src, got, tc.want)
			}
			reparsed, err := Parse(got)
			if err != nil {
				t.Fatalf("Parse(%q) (canonical form) returned error: %v", got, err)
			}
			if again := reparsed.String(); again != got {
				t.Errorf("round trip: Parse(%q).String() = %q, want %q", got, again, got)
			}
			if !reflect.DeepEqual(stripSpans(reparsed), stripSpans(expr)) {
				t.Errorf("round trip changed the tree:\n got %#v\nwant %#v", reparsed, expr)
			}
		})
	}
}

func TestParse_Precedence(t *testing.T) {
	// not > and > or, so this is Or(And(a, b), And(Not(c), d)).
	expr, err := Parse("a and b or not c d")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	or, ok := expr.(*Or)
	if !ok {
		t.Fatalf("root = %T, want *Or", expr)
	}
	if len(or.Operands) != 2 {
		t.Fatalf("or has %d operands, want 2", len(or.Operands))
	}
	left, ok := or.Operands[0].(*And)
	if !ok || len(left.Operands) != 2 {
		t.Fatalf("left = %#v, want a 2-operand *And", or.Operands[0])
	}
	right, ok := or.Operands[1].(*And)
	if !ok || len(right.Operands) != 2 {
		t.Fatalf("right = %#v, want a 2-operand *And", or.Operands[1])
	}
	if _, ok := right.Operands[0].(*Not); !ok {
		t.Errorf("right operand 0 = %#v, want *Not", right.Operands[0])
	}
}

func TestParse_SingleOperandGroupsCollapse(t *testing.T) {
	expr, err := Parse("(((tag:foo)))")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	term, ok := expr.(*Term)
	if !ok {
		t.Fatalf("root = %T, want *Term", expr)
	}
	if term.Field != "tag" {
		t.Errorf("Field = %q, want %q", term.Field, "tag")
	}
}

func TestParse_EmptyQueryIsIdentityAnd(t *testing.T) {
	expr, err := Parse("")
	if err != nil {
		t.Fatalf("Parse(\"\") returned error: %v", err)
	}
	and, ok := expr.(*And)
	if !ok {
		t.Fatalf("root = %T, want *And", expr)
	}
	if len(and.Operands) != 0 {
		t.Errorf("empty query produced %d operands, want 0", len(and.Operands))
	}
}

// TestParse_TermShapes pins the one-token-lookahead rule that decides whether
// the atom after `:` is the term's argument or its value.
func TestParse_TermShapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Term
	}{
		{
			name: "bare word is a text term with no field",
			src:  "inbox",
			want: Term{Value: atomValue(Atom{Text: "inbox"})},
		},
		{
			name: "phrase is a quoted text term",
			src:  `"deep work"`,
			want: Term{Value: atomValue(Atom{Text: "deep work", Quoted: true})},
		},
		{
			name: "bare wikilink is a wikilink text term",
			src:  "[[Deep Work]]",
			want: Term{Value: atomValue(Atom{Text: "Deep Work", Wikilink: true})},
		},
		{
			name: "colon with no following operator is the match operator",
			src:  "tag:foo",
			want: Term{Field: "tag", Op: OpMatch, Value: atomValue(Atom{Text: "foo"})},
		},
		{
			name: "colon followed by an operator makes the atom an argument",
			src:  "age:modified>90d",
			want: Term{
				Field:  "age",
				Arg:    Atom{Text: "modified"},
				HasArg: true,
				Op:     OpGt,
				Value:  atomValue(Atom{Text: "90d"}),
			},
		},
		{
			name: "wikilink argument with a bound",
			src:  "near:[[Deep Work]]<=2",
			want: Term{
				Field:  "near",
				Arg:    Atom{Text: "Deep Work", Wikilink: true},
				HasArg: true,
				Op:     OpLe,
				Value:  atomValue(Atom{Text: "2"}),
			},
		},
		{
			name: "parenthesised argument with no operator",
			src:  `links-to("Deep Work")`,
			want: Term{
				Field:  "links-to",
				Arg:    Atom{Text: "Deep Work", Quoted: true},
				HasArg: true,
				Op:     OpNone,
			},
		},
		{
			name: "a list is never an argument",
			src:  "tag:[a,b]",
			want: Term{
				Field: "tag",
				Op:    OpMatch,
				Value: Value{Kind: ValueList, Atoms: []Atom{{Text: "a"}, {Text: "b"}}},
			},
		},
		{
			name: "operator with no colon",
			src:  "backlinks>3",
			want: Term{Field: "backlinks", Op: OpGt, Value: atomValue(Atom{Text: "3"})},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.src, err)
			}
			term, ok := expr.(*Term)
			if !ok {
				t.Fatalf("Parse(%q) = %T, want *Term", tc.src, expr)
			}
			got := *term
			clearTermSpans(&got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q) =\n %#v\nwant\n %#v", tc.src, got, tc.want)
			}
		})
	}
}

// TestParse_ParenAdjacencyDistinguishesArgumentFromGroup pins the rule that a
// `(` only introduces an argument when it touches the field name.
func TestParse_ParenAdjacencyDistinguishesArgumentFromGroup(t *testing.T) {
	adjacent, err := Parse("orphan(x)")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	term, ok := adjacent.(*Term)
	if !ok || !term.HasArg {
		t.Fatalf("orphan(x) = %#v, want a *Term with an argument", adjacent)
	}

	spaced, err := Parse("orphan (a or b)")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	and, ok := spaced.(*And)
	if !ok || len(and.Operands) != 2 {
		t.Fatalf("orphan (a or b) = %#v, want a 2-operand *And", spaced)
	}
	if _, ok := and.Operands[1].(*Or); !ok {
		t.Errorf("operand 1 = %#v, want *Or", and.Operands[1])
	}
}

// TestParse_QuotingMakesKeywordsLiteral pins that quoting is how a user
// searches for the text "and" rather than combining two terms.
func TestParse_QuotingMakesKeywordsLiteral(t *testing.T) {
	expr, err := Parse(`"and"`)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	term, ok := expr.(*Term)
	if !ok {
		t.Fatalf("root = %T, want *Term", expr)
	}
	if term.Value.Atoms[0].Text != "and" {
		t.Errorf("text = %q, want %q", term.Value.Atoms[0].Text, "and")
	}
	if got := expr.String(); got != `"and"` {
		t.Errorf("String() = %q, want %q — an unquoted keyword would re-lex as an operator", got, `"and"`)
	}
}

// TestParse_TermSpansPointAtSource pins that spans index the source, which is
// what session 5's unknown-field error needs to underline the field.
func TestParse_TermSpansPointAtSource(t *testing.T) {
	const src = "tag:foo and backlinks>3"
	expr, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	and, ok := expr.(*And)
	if !ok || len(and.Operands) != 2 {
		t.Fatalf("root = %#v, want a 2-operand *And", expr)
	}
	if got, want := and.Operands[0].Range(), (Span{0, 7}); got != want {
		t.Errorf("operand 0 span = %+v, want %+v", got, want)
	}
	if got, want := and.Operands[1].Range(), (Span{12, 23}); got != want {
		t.Errorf("operand 1 span = %+v, want %+v", got, want)
	}
	if got, want := src[12:23], "backlinks>3"; got != want {
		t.Errorf("source slice = %q, want %q", got, want)
	}
	if got, want := and.Range(), (Span{0, 23}); got != want {
		t.Errorf("and span = %+v, want %+v", got, want)
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantMsg  string
		wantSpan Span
	}{
		{
			name:     "dangling and",
			src:      "tag:foo and",
			wantMsg:  `expected a term after "and", got end of query`,
			wantSpan: Span{11, 11},
		},
		{
			name:     "dangling or",
			src:      "tag:foo or",
			wantMsg:  `expected a term after "or", got end of query`,
			wantSpan: Span{10, 10},
		},
		{
			name:     "dangling not",
			src:      "not",
			wantMsg:  `expected a term after "not", got end of query`,
			wantSpan: Span{3, 3},
		},
		{
			name:     "dangling hyphen",
			src:      "a and -",
			wantMsg:  `expected a term after "-", got end of query`,
			wantSpan: Span{7, 7},
		},
		{
			name:     "missing value after operator",
			src:      "backlinks>",
			wantMsg:  "expected a value, got end of query",
			wantSpan: Span{10, 10},
		},
		{
			name:     "missing value after colon",
			src:      "tag:",
			wantMsg:  "expected a value, got end of query",
			wantSpan: Span{4, 4},
		},
		{
			name:     "unclosed group",
			src:      "(a or b",
			wantMsg:  `unclosed group opened at column 1: expected ")"`,
			wantSpan: Span{7, 7},
		},
		{
			name:     "empty group",
			src:      "()",
			wantMsg:  `empty group: expected a term between "(" and ")"`,
			wantSpan: Span{1, 2},
		},
		{
			name:     "unmatched close paren",
			src:      "a)",
			wantMsg:  `unexpected ")"`,
			wantSpan: Span{1, 2},
		},
		{
			name:     "unclosed list",
			src:      "tag:[a,b",
			wantMsg:  `unclosed list opened at column 5: expected "," or "]"`,
			wantSpan: Span{8, 8},
		},
		{
			name:     "empty list",
			src:      "tag:[]",
			wantMsg:  `empty list: expected at least one value between "[" and "]"`,
			wantSpan: Span{5, 6},
		},
		{
			name:     "trailing comma in a list",
			src:      "tag:[a,]",
			wantMsg:  `expected a value, got "]"`,
			wantSpan: Span{7, 8},
		},
		{
			name:     "double operator",
			src:      "backlinks>>3",
			wantMsg:  `expected a value, got operator ">"`,
			wantSpan: Span{10, 11},
		},
		{
			name:     "missing argument close paren",
			src:      "links-to(a b)",
			wantMsg:  `expected ")" after the "links-to" argument, got word "b"`,
			wantSpan: Span{11, 12},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", tc.src)
			}
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Parse(%q) returned %T, want *ParseError", tc.src, err)
			}
			if perr.Msg != tc.wantMsg {
				t.Errorf("Msg = %q, want %q", perr.Msg, tc.wantMsg)
			}
			if perr.Span != tc.wantSpan {
				t.Errorf("Span = %+v, want %+v", perr.Span, tc.wantSpan)
			}
			if perr.Source != tc.src {
				t.Errorf("Source = %q, want %q", perr.Source, tc.src)
			}
		})
	}
}

// stripSpans returns a copy of expr with every span zeroed, so two trees can
// be compared for structure alone.
func stripSpans(expr Expr) Expr {
	switch e := expr.(type) {
	case *And:
		out := &And{}
		for _, child := range e.Operands {
			out.Operands = append(out.Operands, stripSpans(child))
		}
		return out
	case *Or:
		out := &Or{}
		for _, child := range e.Operands {
			out.Operands = append(out.Operands, stripSpans(child))
		}
		return out
	case *Not:
		return &Not{Operand: stripSpans(e.Operand)}
	case *Term:
		out := *e
		clearTermSpans(&out)
		return &out
	default:
		return expr
	}
}

func clearTermSpans(t *Term) {
	t.Span = Span{}
	t.Arg.Span = Span{}
	t.Value.Span = Span{}
	if len(t.Value.Atoms) == 0 {
		return
	}
	atoms := make([]Atom, len(t.Value.Atoms))
	for i, a := range t.Value.Atoms {
		a.Span = Span{}
		atoms[i] = a
	}
	t.Value.Atoms = atoms
}
