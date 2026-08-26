package queryexpr

import (
	"testing"
	"unicode/utf8"
)

func TestLex_TokenSpans(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []token
	}{
		{
			name: "field and value",
			src:  "tag:foo",
			want: []token{
				{Kind: tokWord, Text: "tag", Span: Span{0, 3}},
				{Kind: tokColon, Text: ":", Span: Span{3, 4}},
				{Kind: tokWord, Text: "foo", Span: Span{4, 7}},
				{Kind: tokEOF, Span: Span{7, 7}},
			},
		},
		{
			name: "hyphenated field keeps its hyphen but a leading hyphen negates",
			src:  "-links-to:x",
			want: []token{
				{Kind: tokMinus, Text: "-", Span: Span{0, 1}},
				{Kind: tokWord, Text: "links-to", Span: Span{1, 9}},
				{Kind: tokColon, Text: ":", Span: Span{9, 10}},
				{Kind: tokWord, Text: "x", Span: Span{10, 11}},
				{Kind: tokEOF, Span: Span{11, 11}},
			},
		},
		{
			name: "comparison operators",
			src:  "a>=1 b<=2 c!=3 d=4 e==5 f>6 g<7",
			want: []token{
				{Kind: tokWord, Text: "a", Span: Span{0, 1}},
				{Kind: tokOp, Text: ">=", Span: Span{1, 3}},
				{Kind: tokWord, Text: "1", Span: Span{3, 4}},
				{Kind: tokWord, Text: "b", Span: Span{5, 6}},
				{Kind: tokOp, Text: "<=", Span: Span{6, 8}},
				{Kind: tokWord, Text: "2", Span: Span{8, 9}},
				{Kind: tokWord, Text: "c", Span: Span{10, 11}},
				{Kind: tokOp, Text: "!=", Span: Span{11, 13}},
				{Kind: tokWord, Text: "3", Span: Span{13, 14}},
				{Kind: tokWord, Text: "d", Span: Span{15, 16}},
				{Kind: tokOp, Text: "=", Span: Span{16, 17}},
				{Kind: tokWord, Text: "4", Span: Span{17, 18}},
				{Kind: tokWord, Text: "e", Span: Span{19, 20}},
				{Kind: tokOp, Text: "=", Span: Span{20, 22}},
				{Kind: tokWord, Text: "5", Span: Span{22, 23}},
				{Kind: tokWord, Text: "f", Span: Span{24, 25}},
				{Kind: tokOp, Text: ">", Span: Span{25, 26}},
				{Kind: tokWord, Text: "6", Span: Span{26, 27}},
				{Kind: tokWord, Text: "g", Span: Span{28, 29}},
				{Kind: tokOp, Text: "<", Span: Span{29, 30}},
				{Kind: tokWord, Text: "7", Span: Span{30, 31}},
				{Kind: tokEOF, Span: Span{31, 31}},
			},
		},
		{
			name: "quoted phrase decodes escapes and spans the quotes",
			src:  `near:"Deep \"Work\"" and x`,
			want: []token{
				{Kind: tokWord, Text: "near", Span: Span{0, 4}},
				{Kind: tokColon, Text: ":", Span: Span{4, 5}},
				{Kind: tokString, Text: `Deep "Work"`, Span: Span{5, 20}},
				{Kind: tokWord, Text: "and", Span: Span{21, 24}},
				{Kind: tokWord, Text: "x", Span: Span{25, 26}},
				{Kind: tokEOF, Span: Span{26, 26}},
			},
		},
		{
			name: "wikilink keeps its inner text and consumes both bracket pairs",
			src:  "near:[[Deep Work#Heading|Alias]]<=2",
			want: []token{
				{Kind: tokWord, Text: "near", Span: Span{0, 4}},
				{Kind: tokColon, Text: ":", Span: Span{4, 5}},
				{Kind: tokWikilink, Text: "Deep Work#Heading|Alias", Span: Span{5, 32}},
				{Kind: tokOp, Text: "<=", Span: Span{32, 34}},
				{Kind: tokWord, Text: "2", Span: Span{34, 35}},
				{Kind: tokEOF, Span: Span{35, 35}},
			},
		},
		{
			name: "a spaced list of wikilinks stays a list",
			src:  "tag:[ [[A]], [[B]]]",
			want: []token{
				{Kind: tokWord, Text: "tag", Span: Span{0, 3}},
				{Kind: tokColon, Text: ":", Span: Span{3, 4}},
				{Kind: tokLBracket, Text: "[", Span: Span{4, 5}},
				{Kind: tokWikilink, Text: "A", Span: Span{6, 11}},
				{Kind: tokComma, Text: ",", Span: Span{11, 12}},
				{Kind: tokWikilink, Text: "B", Span: Span{13, 18}},
				{Kind: tokRBracket, Text: "]", Span: Span{18, 19}},
				{Kind: tokEOF, Span: Span{19, 19}},
			},
		},
		{
			name: "groups",
			src:  "(a or b)",
			want: []token{
				{Kind: tokLParen, Text: "(", Span: Span{0, 1}},
				{Kind: tokWord, Text: "a", Span: Span{1, 2}},
				{Kind: tokWord, Text: "or", Span: Span{3, 5}},
				{Kind: tokWord, Text: "b", Span: Span{6, 7}},
				{Kind: tokRParen, Text: ")", Span: Span{7, 8}},
				{Kind: tokEOF, Span: Span{8, 8}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lex(tc.src)
			if err != nil {
				t.Fatalf("lex(%q) returned error: %v", tc.src, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("lex(%q) produced %d tokens, want %d: %#v", tc.src, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Kind != tc.want[i].Kind || got[i].Text != tc.want[i].Text || got[i].Span != tc.want[i].Span {
					t.Errorf("token %d = %#v, want %#v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestLex_SpansAreBytesAndTextIsNFC pins the two halves of the Unicode
// contract at once: spans index the raw source in bytes, while token text is
// composed through internal/textnorm so a decomposed query matches a composed
// title. The two forms are written with explicit escapes — writing them as
// literal characters silently produces two identical composed strings.
func TestLex_SpansAreBytesAndTextIsNFC(t *testing.T) {
	const decomposed = "tag:e\u0301tude" // "e" + combining acute
	const composed = "tag:\u00e9tude"
	toks, err := lex(decomposed)
	if err != nil {
		t.Fatalf("lex(%q) returned error: %v", decomposed, err)
	}
	value := toks[2]
	if value.Kind != tokWord {
		t.Fatalf("token 2 kind = %v, want tokWord", value.Kind)
	}
	if want := "\u00e9tude"; value.Text != want {
		t.Errorf("token text = %q, want NFC %q", value.Text, want)
	}
	if want := (Span{4, 11}); value.Span != want {
		t.Errorf("token span = %+v, want byte span %+v", value.Span, want)
	}
	if got, want := utf8.RuneCountInString(decomposed[:value.Span.Start]), 4; got != want {
		t.Errorf("rune prefix = %d, want %d", got, want)
	}
	// The composed form of the same query yields the same text at a shorter
	// span, which is exactly why columns cannot be byte offsets.
	composedToks, err := lex(composed)
	if err != nil {
		t.Fatalf("lex(%q) returned error: %v", composed, err)
	}
	if composedToks[2].Text != value.Text {
		t.Errorf("composed text %q != decomposed text %q", composedToks[2].Text, value.Text)
	}
	if composedToks[2].Span == value.Span {
		t.Errorf("composed and decomposed spans are equal (%+v); byte spans must differ", value.Span)
	}
	if want := (Span{4, 10}); composedToks[2].Span != want {
		t.Errorf("composed span = %+v, want %+v", composedToks[2].Span, want)
	}
}

func TestLex_Errors(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantMsg  string
		wantSpan Span
	}{
		{
			name:     "unterminated phrase",
			src:      `tag:"deep work`,
			wantMsg:  `unterminated quoted phrase: missing closing "`,
			wantSpan: Span{4, 14},
		},
		{
			name:     "unterminated wikilink",
			src:      "near:[[Deep Work",
			wantMsg:  `unterminated wikilink: missing "]]"`,
			wantSpan: Span{5, 16},
		},
		{
			name:     "lone bang",
			src:      "tag!foo",
			wantMsg:  `unexpected "!": did you mean "!="?`,
			wantSpan: Span{3, 4},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lex(tc.src)
			if err == nil {
				t.Fatalf("lex(%q) succeeded, want error", tc.src)
			}
			if err.Msg != tc.wantMsg {
				t.Errorf("Msg = %q, want %q", err.Msg, tc.wantMsg)
			}
			if err.Span != tc.wantSpan {
				t.Errorf("Span = %+v, want %+v", err.Span, tc.wantSpan)
			}
			if err.Source != tc.src {
				t.Errorf("Source = %q, want %q", err.Source, tc.src)
			}
		})
	}
}
