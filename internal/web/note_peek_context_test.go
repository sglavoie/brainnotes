package web

import (
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/recall"
)

func TestConnectionPassagesIncludeProseAroundBareReferences(t *testing.T) {
	blocks := []recall.MarkdownBlock{
		{Kind: recall.BlockHeading, Text: "A useful idea"},
		{Kind: recall.BlockProse, Text: "Writing in your own words makes you confront gaps in your understanding before you forget them."},
		{Kind: recall.BlockHeading, Text: "Related"},
		{Kind: recall.BlockProse, Text: "Related: Deep Work, Evergreen Notes."},
	}
	passages, _ := peekPassages(recall.Document{Blocks: blocks}, "Deep Work")
	if len(passages) != 2 || passages[0].Text != blocks[1].Text || !passages[1].Relevant {
		t.Fatalf("expected substantive context and the matching reference: %#v", passages)
	}
}

func TestConnectionPassagesStayBoundedAndOrdered(t *testing.T) {
	blocks := []recall.MarkdownBlock{
		{Kind: recall.BlockProse, Text: strings.Repeat("é", 9000) + " Context"},
		{Kind: recall.BlockProse, Text: "Context again"},
	}
	passages, truncated := peekPassages(recall.Document{Blocks: blocks}, "Context")
	if !truncated || len(passages) != 1 || len([]rune(passages[0].Text)) != 8000 {
		t.Fatalf("unbounded context: %d passages, truncated=%v", len(passages), truncated)
	}
	fallback, _ := peekPassages(recall.Document{Blocks: []recall.MarkdownBlock{{Kind: recall.BlockProse, Text: "Opening prose"}}}, "Missing")
	if len(fallback) != 1 || fallback[0].Relevant || fallback[0].Text != "Opening prose" {
		t.Fatalf("bad fallback: %#v", fallback)
	}
}
