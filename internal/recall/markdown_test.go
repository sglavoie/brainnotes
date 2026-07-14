package recall

import (
	"strings"
	"testing"
)

func TestExtractMarkdownPreservesSetextTableAndNestedLocations(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"Setext heading",
		"==============",
		"",
		"| A | B |",
		"| --- | --- |",
		"",
		"> ## Quoted heading",
		"> Quoted paragraph.",
		"",
	}, "\n")
	blocks, fields := extractMarkdown(body)
	document := Document{Body: body, Blocks: blocks, Fields: fields}

	if len(blocks) != 4 {
		t.Fatalf("len(blocks) = %d, want 4: %#v", len(blocks), blocks)
	}
	wantSources := []string{
		"Setext heading\n==============",
		"| A | B |\n| --- | --- |",
		"> ## Quoted heading",
		"> Quoted paragraph.",
	}
	for index, want := range wantSources {
		got, ok := document.BlockSource(index)
		if !ok {
			t.Fatalf("BlockSource(%d) rejected %#v", index, blocks[index].Location)
		}
		if got != want {
			t.Errorf("BlockSource(%d) = %q, want %q", index, got, want)
		}
	}
	if blocks[0].HeadingLevel != 1 || blocks[2].HeadingLevel != 2 {
		t.Errorf("heading levels = %d, %d, want 1, 2", blocks[0].HeadingLevel, blocks[2].HeadingLevel)
	}
	if blocks[1].Kind != BlockTable || blocks[1].Text != "A B" {
		t.Errorf("table block = %#v, want table text A B", blocks[1])
	}
	if blocks[0].Location.StartLine != 1 || blocks[0].Location.EndLine != 2 {
		t.Errorf("setext lines = %#v, want 1-2", blocks[0].Location)
	}
	if blocks[1].Location.StartLine != 4 || blocks[1].Location.EndLine != 5 {
		t.Errorf("table lines = %#v, want 4-5", blocks[1].Location)
	}
}

func TestExtractMarkdownExcludesBlockCodeAndHTMLButKeepsInlineCode(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"Visible `inlineToken` and <span>inline HTML text</span>.",
		"",
		"```text",
		"fencedToken",
		"```",
		"",
		"    indentedToken",
		"",
		"<section>",
		"htmlBlockToken",
		"</section>",
	}, "\n")
	blocks, fields := extractMarkdown(body)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want one visible paragraph", blocks)
	}
	got := fieldText(fields)
	for _, want := range []string{"Visible", "inlineToken", "inline HTML text"} {
		if !strings.Contains(got, want) {
			t.Errorf("text %q does not contain %q", got, want)
		}
	}
	for _, excluded := range []string{"fencedToken", "indentedToken", "htmlBlockToken", "section", "span"} {
		if strings.Contains(got, excluded) {
			t.Errorf("text %q unexpectedly contains %q", got, excluded)
		}
	}
}

func TestExtractMarkdownCRLFSourcesExcludeTrailingLineEnding(t *testing.T) {
	t.Parallel()

	body := "# Heading\r\n\r\nFirst line\r\nsecond line\r\n"
	blocks, _ := extractMarkdown(body)
	document := Document{Body: body, Blocks: blocks}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v, want heading and paragraph", blocks)
	}
	for index, want := range []string{"# Heading", "First line\r\nsecond line"} {
		got, ok := document.BlockSource(index)
		if !ok {
			t.Fatalf("BlockSource(%d) rejected %#v", index, blocks[index].Location)
		}
		if got != want {
			t.Errorf("BlockSource(%d) = %q, want %q", index, got, want)
		}
	}
}
