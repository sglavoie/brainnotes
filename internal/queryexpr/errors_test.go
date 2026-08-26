package queryexpr

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseError_ColumnsAreRuneBased is the core of the error contract: a
// query containing accented text must still point at the right character.
// Byte offsets and rune columns diverge the moment a non-ASCII rune precedes
// the span, and they diverge by different amounts for the composed and
// decomposed spellings of the same word — so the test pins both.
func TestParseError_ColumnsAreRuneBased(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		span       Span
		wantColumn int
		wantBytes  int
	}{
		{
			name:       "composed accent",
			source:     "tag:café and backlnks>3",
			span:       Span{14, 22},
			wantColumn: 14,
			wantBytes:  15,
		},
		{
			name:       "decomposed accent",
			source:     "tag:café and backlnks>3",
			span:       Span{15, 23},
			wantColumn: 15,
			wantBytes:  16,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.source[tc.span.Start:tc.span.End]; got != "backlnks" {
				t.Fatalf("fixture span slices %q, want %q", got, "backlnks")
			}
			err := NewParseError(tc.source, tc.span, `unknown field "backlnks"`, []string{"backlinks"})
			if err.Column != tc.wantColumn {
				t.Errorf("Column = %d, want %d", err.Column, tc.wantColumn)
			}
			if tc.span.Start+1 != tc.wantBytes {
				t.Fatalf("fixture is vacuous: byte column %d equals nothing distinguishing", tc.span.Start+1)
			}
			if err.Column == tc.wantBytes {
				t.Errorf("Column %d matches the byte column; it must be rune-based", err.Column)
			}

			lines := strings.Split(err.Render(), "\n")
			if len(lines) != 4 {
				t.Fatalf("Render() produced %d lines, want 4:\n%s", len(lines), err.Render())
			}
			caretLine := lines[2]
			padding := len(caretLine) - len(strings.TrimLeft(caretLine, " "))
			// Two spaces of indent plus one column per rune before the span.
			wantPadding := 2 + utf8.RuneCountInString(tc.source[:tc.span.Start])
			if padding != wantPadding {
				t.Errorf("caret padding = %d, want %d (line %q)", padding, wantPadding, caretLine)
			}
			if got := strings.TrimLeft(caretLine, " "); got != "^^^^^^^^" {
				t.Errorf("carets = %q, want 8 carets", got)
			}
			// The carets must land under the flagged text when both lines are
			// measured in runes, which is what a terminal actually renders.
			sourceLine := []rune(lines[1])
			under := string(sourceLine[padding : padding+len(strings.TrimLeft(caretLine, " "))])
			if under != "backlnks" {
				t.Errorf("carets sit under %q, want %q", under, "backlnks")
			}
		})
	}
}

func TestParseError_RenderExactOutput(t *testing.T) {
	err := NewParseError("tag:café and backlnks>3", Span{14, 22}, `unknown field "backlnks"`, []string{"backlinks"})
	want := strings.Join([]string{
		`query: unknown field "backlnks"`,
		"  tag:café and backlnks>3",
		"               ^^^^^^^^",
		"  did you mean: backlinks",
	}, "\n")
	if got := err.Render(); got != want {
		t.Errorf("Render() =\n%s\n\nwant\n%s", got, want)
	}
	if got, want := err.Error(), `query: unknown field "backlnks" at column 14`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestParseError_RenderWithoutSuggestions(t *testing.T) {
	err := NewParseError("tag:", Span{4, 4}, "expected a value, got end of query", nil)
	want := strings.Join([]string{
		"query: expected a value, got end of query",
		"  tag:",
		"      ^",
	}, "\n")
	if got := err.Render(); got != want {
		t.Errorf("Render() =\n%s\n\nwant\n%s", got, want)
	}
	if err.Column != 5 {
		t.Errorf("Column = %d, want 5", err.Column)
	}
}

// TestParseError_ParserColumnsAreRuneBased pins that the columns the parser
// itself reports — including the one embedded in an "opened at column N"
// message — are rune-based too, not just the ones a caller builds by hand.
func TestParseError_ParserColumnsAreRuneBased(t *testing.T) {
	const src = "tag:café and (a or b"
	_, parseErr := Parse(src)
	if parseErr == nil {
		t.Fatalf("Parse(%q) succeeded, want an unclosed-group error", src)
	}
	perr, ok := parseErr.(*ParseError)
	if !ok {
		t.Fatalf("Parse returned %T, want *ParseError", parseErr)
	}
	if want := `unclosed group opened at column 14: expected ")"`; perr.Msg != want {
		t.Errorf("Msg = %q, want %q", perr.Msg, want)
	}
	// Byte offset of the "(" is 14, so a byte-based column would read 15.
	if got := strings.Index(src, "("); got != 14 {
		t.Fatalf("fixture: \"(\" is at byte %d, want 14", got)
	}
	if perr.Column != utf8.RuneCountInString(src)+1 {
		t.Errorf("Column = %d, want %d (end of query)", perr.Column, utf8.RuneCountInString(src)+1)
	}
}

// TestParseError_Suggestions copies the slice so a caller cannot mutate the
// error's suggestions after the fact.
func TestParseError_SuggestionsAreCopied(t *testing.T) {
	suggest := []string{"backlinks", "links"}
	err := NewParseError("x", Span{0, 1}, "boom", suggest)
	suggest[0] = "mutated"
	if err.Suggest[0] != "backlinks" {
		t.Errorf("Suggest[0] = %q, want %q", err.Suggest[0], "backlinks")
	}
	if !strings.Contains(err.Render(), "did you mean: backlinks, links") {
		t.Errorf("Render() = %q, want a joined suggestion list", err.Render())
	}
}

// TestParseError_RenderClampsAnOversizedSpan keeps Render total for an error
// built by hand rather than through NewParseError.
func TestParseError_RenderClampsAnOversizedSpan(t *testing.T) {
	err := &ParseError{Source: "tag", Span: Span{Start: 99, End: 200}, Msg: "boom"}
	got := err.Render()
	if !strings.HasPrefix(got, "query: boom\n  tag\n") {
		t.Errorf("Render() = %q, want it to survive an out-of-range span", got)
	}
}
