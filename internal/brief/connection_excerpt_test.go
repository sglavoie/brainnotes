package brief

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/brainnotes/internal/recall"
)

func TestConnectCardUsesBridgePassage(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	writeSelectionNote(t, root, "Practice.md", "# Practice\n\nProtect time for [[Deep Work]].\n", now)
	writeSelectionNote(t, root, "Deep Work.md", "# Deep Work\n\nProtect uninterrupted time for focused learning.\n", now)
	writeSelectionNote(t, root, "Weekly Review.md", "# Weekly Review\n\nCheck active projects and decide what to do next week.\n\nBlock time for [[Deep Work]] next week.\n", now)
	notes, graph := loadSelectionFixture(t, root)
	corpus, err := BuildSelectionCorpus(root, notes, graph, SelectionCorpusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := selector.SelectConnect(corpus.Snapshot, NoteRef{RelPath: "Practice.md", Title: "Practice"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Card == nil || result.Card.Candidate == nil || result.Card.Candidate.RelPath != "Weekly Review.md" {
		t.Fatalf("expected the shared-reference connection: %#v", result)
	}
	if result.Card.Excerpt != "Block time for [[Deep Work]] next week." {
		t.Fatalf("connection card should show the relevant passage, got %q", result.Card.Excerpt)
	}
}

func TestConnectionExcerptContextFallbackAndBounds(t *testing.T) {
	const fallback = "An introductory passage."
	selector := &DeterministicSelector{
		excerptsByID: map[string]string{"Candidate.md": fallback},
		blocksByPath: map[string][]recall.MarkdownBlock{"Candidate.md": {
			{Kind: recall.BlockHeading, Text: "Source"},
			{Kind: recall.BlockProse, Text: fallback},
			{Kind: recall.BlockProse, Text: "Return to SOURCE when planning the week."},
			{Kind: recall.BlockProse, Text: strings.Repeat("é🙂 ", 100) + "Deep Work matters here. " + strings.Repeat("More context. ", 60)},
		}},
	}
	for _, test := range []struct{ name, source, bridge, want string }{
		{"direct", "Source", "", "Return to SOURCE when planning the week."},
		{"unmatched", "Missing", "", fallback},
		{"empty context", "", "", fallback},
		{"bridge takes precedence", "Source", "Deep Work", "Deep Work matters here."},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := selector.connectionCardExcerpt(recall.Result{Path: "Candidate.md", BridgeTitle: test.bridge}, NoteRef{Title: test.source})
			if !strings.Contains(got, test.want) || !utf8.ValidString(got) || utf8.RuneCountInString(got) > DefaultCardExcerptRunes {
				t.Fatalf("expected bounded passage containing %q, got %q", test.want, got)
			}
		})
	}
}
