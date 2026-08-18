package noteops

import (
	"strings"
	"testing"
)

const (
	captureTestHeading = "## Captures"
	captureTestLine    = "- 2026-08-17 09:30 buy milk"
)

func TestPlanCaptureInsert(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		heading   string
		want      string
		wantLine  int
		wantAdded bool
	}{
		{
			name:      "heading_absent",
			body:      "# Note\n\nSome prose.\n",
			want:      "# Note\n\nSome prose.\n\n## Captures\n" + captureTestLine + "\n",
			wantLine:  6,
			wantAdded: true,
		},
		{
			name:      "heading_absent_preserves_trailing_blanks",
			body:      "# Note\n\n\n\n",
			want:      "# Note\n\n\n\n## Captures\n" + captureTestLine + "\n",
			wantLine:  6,
			wantAdded: true,
		},
		{
			name:      "heading_absent_body_without_trailing_newline",
			body:      "# Note",
			want:      "# Note\n\n## Captures\n" + captureTestLine + "\n",
			wantLine:  4,
			wantAdded: true,
		},
		{
			name:      "frontmatter_only_body_empty",
			body:      "",
			want:      "## Captures\n" + captureTestLine + "\n",
			wantLine:  2,
			wantAdded: true,
		},
		{
			name:      "frontmatter_only_body_single_newline",
			body:      "\n",
			want:      "\n## Captures\n" + captureTestLine + "\n",
			wantLine:  3,
			wantAdded: true,
		},
		{
			name:      "heading_absent_crlf_preserves_trailing_blanks",
			body:      "# Note\r\n\r\n",
			want:      "# Note\r\n\r\n## Captures\r\n" + captureTestLine + "\r\n",
			wantLine:  4,
			wantAdded: true,
		},
		{
			name:      "heading_inside_fence_is_not_the_anchor",
			body:      "# Note\n\n```markdown\n## Captures\n- example\n```\n\n## Captures\n- first\n",
			want:      "# Note\n\n```markdown\n## Captures\n- example\n```\n\n## Captures\n- first\n" + captureTestLine + "\n",
			wantLine:  10,
			wantAdded: false,
		},
		{
			name:      "wrong_heading_level_creates_section",
			body:      "# Note\n\n### Captures\n- old\n",
			want:      "# Note\n\n### Captures\n- old\n\n## Captures\n" + captureTestLine + "\n",
			wantLine:  7,
			wantAdded: true,
		},
		{
			name:      "empty_section_inserts_under_heading",
			body:      "## Captures\n\n## Next\n- x\n",
			want:      "## Captures\n" + captureTestLine + "\n\n## Next\n- x\n",
			wantLine:  2,
			wantAdded: false,
		},
		{
			name:      "trailing_blanks_preserved",
			body:      "## Captures\n- a\n\n\n## Next\n",
			want:      "## Captures\n- a\n" + captureTestLine + "\n\n\n## Next\n",
			wantLine:  3,
			wantAdded: false,
		},
		{
			name:      "body_without_trailing_newline",
			body:      "## Captures\n- a",
			want:      "## Captures\n- a\n" + captureTestLine,
			wantLine:  3,
			wantAdded: false,
		},
		{
			name:      "crlf_terminators_preserved",
			body:      "## Captures\r\n- a\r\n",
			want:      "## Captures\r\n- a\r\n" + captureTestLine + "\r\n",
			wantLine:  3,
			wantAdded: false,
		},
		{
			name:      "crlf_heading_absent",
			body:      "# Note\r\n",
			want:      "# Note\r\n\r\n## Captures\r\n" + captureTestLine + "\r\n",
			wantLine:  4,
			wantAdded: true,
		},
		{
			name:      "duplicate_heading_first_match_wins",
			body:      "## Captures\n- a\n\n## Other\n\n## Captures\n- b\n",
			want:      "## Captures\n- a\n" + captureTestLine + "\n\n## Other\n\n## Captures\n- b\n",
			wantLine:  3,
			wantAdded: false,
		},
		{
			name:      "case_and_closing_hashes_still_anchor",
			body:      "## captures ##\n- a\n",
			want:      "## captures ##\n- a\n" + captureTestLine + "\n",
			wantLine:  3,
			wantAdded: false,
		},
		{
			name:      "subheading_stays_inside_section",
			body:      "## Captures\n- a\n\n### Sub\n- b\n\n## Next\n",
			want:      "## Captures\n- a\n\n### Sub\n- b\n" + captureTestLine + "\n\n## Next\n",
			wantLine:  6,
			wantAdded: false,
		},
		{
			name:      "decomposed_body_heading_matches_composed_config",
			body:      "## Re\u0301sume\u0301\n- a\n",
			heading:   "## R\u00e9sum\u00e9",
			want:      "## Re\u0301sume\u0301\n- a\n" + captureTestLine + "\n",
			wantLine:  3,
			wantAdded: false,
		},
		{
			name:      "four_space_indented_heading_is_not_an_anchor",
			body:      "# Note\n\n    ## Captures\n",
			want:      "# Note\n\n    ## Captures\n\n## Captures\n" + captureTestLine + "\n",
			wantLine:  6,
			wantAdded: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			heading := tc.heading
			if heading == "" {
				heading = captureTestHeading
			}
			got, gotLine, gotAdded, err := planCaptureInsert(tc.body, heading, captureTestLine)
			if err != nil {
				t.Fatalf("planCaptureInsert() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("body =\n%q\nwant\n%q", got, tc.want)
			}
			if gotLine != tc.wantLine {
				t.Errorf("line = %d, want %d", gotLine, tc.wantLine)
			}
			if gotAdded != tc.wantAdded {
				t.Errorf("addedHeading = %v, want %v", gotAdded, tc.wantAdded)
			}
			if gotLine >= 1 {
				lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
				if gotLine > len(lines) {
					t.Fatalf("line %d out of range for %d lines", gotLine, len(lines))
				}
				if strings.TrimSuffix(lines[gotLine-1], "\r") != captureTestLine {
					t.Errorf("line %d = %q, want the captured line", gotLine, lines[gotLine-1])
				}
			}
		})
	}
}

func TestPlanCaptureInsertPreservesSurroundingBytes(t *testing.T) {
	body := "# Note\n\nprose\n\n## Captures\n- a\n\n## Tail\n\ntail prose\n"
	got, _, _, err := planCaptureInsert(body, captureTestHeading, captureTestLine)
	if err != nil {
		t.Fatalf("planCaptureInsert() error = %v", err)
	}
	before, after, ok := strings.Cut(got, captureTestLine+"\n")
	if !ok {
		t.Fatalf("captured line missing from %q", got)
	}
	if before+after != body {
		t.Errorf("surrounding bytes changed: %q + %q != %q", before, after, body)
	}
}

func TestPlanCaptureInsertHeadingAbsentOnlyAppends(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantSuffix string
	}{
		{
			name:       "lf_trailing_newline",
			body:       "# Note\n\nSome prose.\n",
			wantSuffix: "\n## Captures\n" + captureTestLine + "\n",
		},
		{
			name:       "lf_trailing_blank_lines",
			body:       "# Note\n\nSome prose.\n\n\n",
			wantSuffix: "## Captures\n" + captureTestLine + "\n",
		},
		{
			name:       "lf_without_trailing_newline",
			body:       "# Note\n\nSome prose.",
			wantSuffix: "\n\n## Captures\n" + captureTestLine + "\n",
		},
		{
			name:       "lf_blank_lines_only",
			body:       "\n\n",
			wantSuffix: "## Captures\n" + captureTestLine + "\n",
		},
		{
			name:       "crlf_trailing_newline",
			body:       "# Note\r\n\r\nSome prose.\r\n",
			wantSuffix: "\r\n## Captures\r\n" + captureTestLine + "\r\n",
		},
		{
			name:       "crlf_trailing_blank_lines",
			body:       "# Note\r\n\r\nSome prose.\r\n\r\n\r\n",
			wantSuffix: "## Captures\r\n" + captureTestLine + "\r\n",
		},
		{
			name:       "crlf_without_trailing_newline",
			body:       "# Note\r\n\r\nSome prose.",
			wantSuffix: "\n\r\n## Captures\r\n" + captureTestLine + "\r\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, gotAdded, err := planCaptureInsert(tc.body, captureTestHeading, captureTestLine)
			if err != nil {
				t.Fatalf("planCaptureInsert() error = %v", err)
			}
			if !gotAdded {
				t.Fatalf("addedHeading = false, want the heading-absent path")
			}
			if got != tc.body+tc.wantSuffix {
				t.Fatalf("body =\n%q\nwant\n%q", got, tc.body+tc.wantSuffix)
			}
			if original := strings.TrimSuffix(got, tc.wantSuffix); original != tc.body {
				t.Errorf("removing the inserted suffix gives %q, want %q", original, tc.body)
			}
		})
	}
}

func TestPlanCaptureInsertErrors(t *testing.T) {
	tests := []struct {
		name    string
		heading string
		line    string
	}{
		{name: "heading_not_atx", heading: "Captures", line: captureTestLine},
		{name: "heading_seven_hashes", heading: "####### Captures", line: captureTestLine},
		{name: "heading_without_title", heading: "## ##", line: captureTestLine},
		{name: "heading_hashes_only", heading: "##", line: captureTestLine},
		{name: "line_with_newline", heading: captureTestHeading, line: "- a\n- b"},
		{name: "line_blank", heading: captureTestHeading, line: "   "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := planCaptureInsert("## Captures\n- a\n", tc.heading, tc.line)
			if err == nil {
				t.Fatal("planCaptureInsert() error = nil, want an error")
			}
			if code := CodeOf(err); code != CodeInvalidRequest {
				t.Errorf("code = %q, want %q", code, CodeInvalidRequest)
			}
		})
	}
}

func TestCaptureCountSectionBullets(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		heading string
		want    int
	}{
		{
			name: "nested_and_fenced_bullets_excluded",
			body: "## Captures\n- one\n    - nested\n- two\n\n```\n- fenced\n```\n\n* three\n+ four\n\n## Next\n- five\n",
			want: 4,
		},
		{
			name: "heading_absent",
			body: "# Note\n- a\n",
			want: 0,
		},
		{
			name: "heading_only_inside_fence",
			body: "```\n## Captures\n- a\n```\n",
			want: 0,
		},
		{
			name: "empty_section",
			body: "## Captures\n\n## Next\n- x\n",
			want: 0,
		},
		{
			name: "wrong_heading_level",
			body: "### Captures\n- a\n- b\n",
			want: 0,
		},
		{
			name: "first_duplicate_section_only",
			body: "## Captures\n- a\n\n## Captures\n- b\n- c\n",
			want: 1,
		},
		{
			name: "subheading_bullets_included",
			body: "## Captures\n- a\n\n### Sub\n- b\n\n## Next\n- c\n",
			want: 2,
		},
		{
			name: "crlf_body",
			body: "## Captures\r\n- a\r\n- b\r\n",
			want: 2,
		},
		{
			name:    "invalid_heading",
			body:    "## Captures\n- a\n",
			heading: "Captures",
			want:    0,
		},
		{
			name: "empty_body",
			body: "",
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			heading := tc.heading
			if heading == "" {
				heading = captureTestHeading
			}
			if got := CountSectionBullets(tc.body, heading); got != tc.want {
				t.Errorf("CountSectionBullets() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNormalizeCaptureText(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "trims_outer_whitespace", raw: "  buy milk \t", want: "buy milk"},
		{name: "folds_interior_newlines", raw: "line one\nline two", want: "line one line two"},
		{name: "folds_crlf_and_blank_lines", raw: "one\r\n\r\ntwo", want: "one two"},
		{name: "folds_tabs_and_runs", raw: "one\t\t  two", want: "one two"},
		{name: "composes_to_nfc", raw: "cafe\u0301", want: "caf\u00e9"},
		{name: "wikilink_passes_through", raw: "ask [[Alice]] about [[Project X|X]]", want: "ask [[Alice]] about [[Project X|X]]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCaptureText(tc.raw)
			if err != nil {
				t.Fatalf("normalizeCaptureText() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("normalizeCaptureText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeCaptureTextErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "whitespace_only", raw: " \t\n "},
		{name: "control_character", raw: "bad\x07text"},
		{name: "null_byte", raw: "bad\x00text"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeCaptureText(tc.raw)
			if err == nil {
				t.Fatal("normalizeCaptureText() error = nil, want an error")
			}
			if code := CodeOf(err); code != CodeInvalidRequest {
				t.Errorf("code = %q, want %q", code, CodeInvalidRequest)
			}
		})
	}
}
