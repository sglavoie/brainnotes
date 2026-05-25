package wikilink

import (
	"reflect"
	"testing"
)

func TestParseSkipsCodeBlocks(t *testing.T) {
	t.Parallel()

	t.Run("fenced backticks", func(t *testing.T) {
		t.Parallel()
		in := "Outside [[Yes]]\n```\nInside [[No]]\n```\nAfter [[Also]]"
		got := Parse(in)
		targets := collectTargets(got)
		want := []string{"Yes", "Also"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("fenced tildes with info string", func(t *testing.T) {
		t.Parallel()
		in := "[[Open]]\n~~~go\n[[Hidden]]\n~~~\n[[Close]]"
		targets := collectTargets(Parse(in))
		want := []string{"Open", "Close"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("longer fence allows shorter inside", func(t *testing.T) {
		t.Parallel()
		in := "````\n```\n[[NestedFence]]\n```\n````\n[[After]]"
		targets := collectTargets(Parse(in))
		want := []string{"After"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("inline code", func(t *testing.T) {
		t.Parallel()
		in := "Real [[Yes]] but `[[No]]` not this"
		targets := collectTargets(Parse(in))
		want := []string{"Yes"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("inline code double backticks", func(t *testing.T) {
		t.Parallel()
		in := "look ``[[Hidden]]`` and [[Shown]]"
		targets := collectTargets(Parse(in))
		want := []string{"Shown"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("unmatched backtick is not a span", func(t *testing.T) {
		t.Parallel()
		in := "stray ` and a real [[Link]]"
		targets := collectTargets(Parse(in))
		want := []string{"Link"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("indented code block after blank", func(t *testing.T) {
		t.Parallel()
		in := "paragraph one\n\n    [[Hidden]]\n    more code\n\n[[Visible]]"
		targets := collectTargets(Parse(in))
		want := []string{"Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("indented continuation not code", func(t *testing.T) {
		t.Parallel()

		in := "lead in\n    [[Visible]]\n"
		targets := collectTargets(Parse(in))
		want := []string{"Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("tab indented code at doc start", func(t *testing.T) {
		t.Parallel()
		in := "\t[[Hidden]]\n\n[[Visible]]"
		targets := collectTargets(Parse(in))
		want := []string{"Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("backtick fence with backtick in info string is not a fence", func(t *testing.T) {
		t.Parallel()

		in := "[[Before]]\n```foo`bar\n[[Visible]]\n```\n[[After]]"
		targets := collectTargets(Parse(in))
		want := []string{"Before", "Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("backtick fence with backtick in info string full round trip", func(t *testing.T) {
		t.Parallel()

		in := "[[A]]\n```x`y\n[[B]]\ntext\n```\n[[C]]\n```\n[[D]]"
		targets := collectTargets(Parse(in))

		want := []string{"A", "B", "D"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("tilde fence with backtick in info string is valid", func(t *testing.T) {
		t.Parallel()

		in := "[[Before]]\n~~~foo`bar\n[[Hidden]]\n~~~\n[[After]]"
		targets := collectTargets(Parse(in))
		want := []string{"Before", "After"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})
}

func TestParseSkipsFencedCodeWithCRLF(t *testing.T) {
	t.Parallel()
	in := "[[Before]]\r\n```\r\n[[Inside]]\r\n```\r\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"Before", "After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseSkipsIndentedCodeAfterCRLFBlank(t *testing.T) {
	t.Parallel()
	in := "para\r\n\r\n    [[Hidden]]\r\n\r\n[[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseUnclosedFenceMasksRest(t *testing.T) {
	t.Parallel()

	in := "[[Before]]\n```\n[[Inside]]\n[[StillInside]]"
	got := collectTargets(Parse(in))
	want := []string{"Before"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseLongerFenceAllowsBacktickRunsInside(t *testing.T) {
	t.Parallel()

	in := "````\n```\n[[NotEscaping]]\n```\n````\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseDifferentFenceCharsDontClose(t *testing.T) {
	t.Parallel()

	in := "```\n~~~\n[[Inside]]\n~~~\n```\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseSkipsBlockquotedFencedCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "backtick fence",
			in:   "[[Before]]\n> ```\n> [[Hidden]]\n> ```\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "nested blockquote tilde fence",
			in:   "[[Before]]\n> > ~~~md\n> > [[Hidden]]\n> > ~~~\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "indented blockquote fence",
			in:   "[[Before]]\n  > ```\n  > [[Hidden]]\n  > ```\n[[After]]",
			want: []string{"Before", "After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("targets = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseQuotedCloserDoesNotCloseUnquotedFence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "quoted closer does not close plain fence",
			in:   "[[Before]]\n```\n[[InsideA]]\n> ```\n[[InsideB]]",
			want: []string{"Before"},
		},
		{
			name: "quoted closer does not close plain tilde fence",
			in:   "[[Before]]\n~~~\n[[InsideA]]\n> ~~~\n[[InsideB]]",
			want: []string{"Before"},
		},
		{
			name: "plain closer still closes plain fence",
			in:   "[[Before]]\n```\n[[Inside]]\n```\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "quoted opener accepts plain closer (conservative)",
			in:   "[[Before]]\n> ```\n> [[Inside]]\n```\n[[After]]",
			want: []string{"Before", "After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) targets = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseSkipsMultilineInlineCodeSpan(t *testing.T) {
	t.Parallel()
	in := "before `[[Hidden]]\n[[StillHidden]]` after [[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseInlineCodeSpanDoesNotCrossBlankLine(t *testing.T) {
	t.Parallel()
	in := "`[[Visible]]\n\n[[AlsoVisible]]`"
	got := collectTargets(Parse(in))
	want := []string{"Visible", "AlsoVisible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseHandlesLongFences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "10-backtick fence",
			in:   "``````````\n[[Hidden]]\n``````````\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "opener 4 closer 5 (longer closer ok)",
			in:   "````\n[[Hidden]]\n`````\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "opener 4 closer 3 (too short) then opener 4 closer 4",
			in:   "````\n[[Hidden]]\n```\n[[StillHidden]]\n````\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "tilde long fence",
			in:   "~~~~~\n[[Hidden]]\n~~~\n[[StillHidden]]\n~~~~~\n[[After]]",
			want: []string{"After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseMixedFenceCharsDoNotCloseEachOther(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "tilde does not close backticks",
			in:   "```\n[[Hidden]]\n~~~\n[[StillHidden]]\n```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "backtick does not close tildes",
			in:   "~~~\n[[Hidden]]\n```\n[[StillHidden]]\n~~~\n[[After]]",
			want: []string{"After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFenceInfoStringEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "fence opener line with wikilink-shaped info string",
			in:   "```[[InOpener]]\n[[Hidden]]\n```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "tilde opener with backtick in info",
			in:   "~~~ go`info\n[[Hidden]]\n~~~\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "opener with only trailing whitespace info",
			in:   "```   \n[[Hidden]]\n```\n[[After]]",
			want: []string{"After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFenceCloserIndentedUpToThreeSpaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "closer indented 2 spaces",
			in:   "```\n[[Hidden]]\n  ```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "closer indented 3 spaces",
			in:   "```\n[[Hidden]]\n   ```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "closer indented 4 spaces is NOT a closer (still inside fence)",
			in:   "```\n[[Hidden]]\n    ```\n[[StillHidden]]",
			want: nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFenceContentContainsBlankLine(t *testing.T) {
	t.Parallel()

	in := "```\n[[A]]\n\n[[B]]\n```\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
	}
}

func TestParseStrayBacktickBeforeFence(t *testing.T) {
	t.Parallel()

	in := "abc `\n```\n[[Hidden]]\n```\nafter [[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
	}
}

func TestParseInlineCodeAdjacentToWikilink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "wikilink follows inline code with no space",
			in:   "`code`[[Visible]]",
			want: []string{"Visible"},
		},
		{
			name: "wikilink precedes inline code with no space",
			in:   "[[Visible]]`code`",
			want: []string{"Visible"},
		},
		{
			name: "back-to-back inline code spans",
			in:   "`a``b` outside [[Visible]]",
			want: []string{"Visible"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRecognizesCRLFFenceCloser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "CRLF backtick fence",
			in:   "[[Before]]\r\n```\r\n[[Hidden]]\r\n```\r\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "CRLF tilde fence",
			in:   "[[Before]]\r\n~~~go\r\n[[Hidden]]\r\n~~~\r\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "CRLF long fence",
			in:   "[[Before]]\r\n````\r\n```\r\n[[Hidden]]\r\n```\r\n````\r\n[[After]]",
			want: []string{"Before", "After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
