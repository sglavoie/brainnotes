package web

import (
	"reflect"
	"strings"
	"testing"
)

func TestTableOfContentsForMarkdownExtractsHeadings(t *testing.T) {
	body := strings.Join([]string{
		"# Alpha",
		"",
		"Paragraph.",
		"",
		"## Beta **Bold**",
		"",
		"```md",
		"# Ignored",
		"```",
		"",
		"Gamma",
		"-----",
		"",
	}, "\n")

	got := tableOfContentsForMarkdown(body, "/n/source")
	if len(got) != 3 {
		t.Fatalf("TOC length = %d, want 3: %#v", len(got), got)
	}

	wantText := []string{"Alpha", "Beta Bold", "Gamma"}
	wantLevels := []int{1, 2, 2}
	wantPaths := []string{"/n/source#alpha", "/n/source#beta-bold", "/n/source#gamma"}
	for i, item := range got {
		if item.Text != wantText[i] || item.Level != wantLevels[i] || item.Path != wantPaths[i] {
			t.Fatalf("TOC[%d] = %#v, want text=%q level=%d path=%q", i, item, wantText[i], wantLevels[i], wantPaths[i])
		}
	}
}

func TestTableOfContentsForMarkdownStableDuplicateHeadingIDs(t *testing.T) {
	body := strings.Join([]string{
		"## Duplicate Heading",
		"## Duplicate Heading",
		"## Duplicate-Heading",
		"## !!!",
	}, "\n")

	first := tocIDs(tableOfContentsForMarkdown(body, ""))
	second := tocIDs(tableOfContentsForMarkdown(body, ""))
	want := []string{"duplicate-heading", "duplicate-heading-1", "duplicate-heading-2", "heading"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("ids = %#v, want %#v", first, want)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("ids are not stable across calls: first=%#v second=%#v", first, second)
	}
}

func TestBacklinksForNoteIncludesOccurrenceSnippets(t *testing.T) {
	_, snapshot, notes := newFilterTestSnapshot(t, map[string]string{
		"Target.md": "---\ntitle: Target\n---\nTarget body\n",
		"Ref.md": strings.Join([]string{
			"---",
			"nested:",
			"  refs:",
			"    - \"Again [[Target]]\"",
			"related: \"See [[Target]]\"",
			"---",
			"Before",
			"Body [[Target]] twice [[Target|alias]]",
			"",
		}, "\n"),
	})

	links := backlinksForNote(snapshot, notes["Target.md"])
	if len(links) != 1 {
		t.Fatalf("backlinks length = %d, want 1: %#v", len(links), links)
	}
	link := links[0]
	if link.Title != "Ref" || link.Path != "/n/ref" {
		t.Fatalf("backlink = %#v, want Ref at /n/ref", link)
	}
	if link.Count != 4 || len(link.Occurrences) != 4 {
		t.Fatalf("occurrence count = %d/%d, want 4: %#v", link.Count, len(link.Occurrences), link.Occurrences)
	}

	assertOccurrence(t, link.Occurrences, "frontmatter.nested.refs", 0, "Again [[Target]]")
	assertOccurrence(t, link.Occurrences, "frontmatter.related", 0, "See [[Target]]")
	assertOccurrence(t, link.Occurrences, "line 8", 8, "Body [[Target]] twice [[Target|alias]]")
}

func tocIDs(items []tableOfContentsItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func assertOccurrence(t *testing.T, occurrences []backlinkOccurrence, location string, line int, snippet string) {
	t.Helper()

	for _, occurrence := range occurrences {
		if occurrence.Location == location && occurrence.Line == line && occurrence.Snippet == snippet {
			return
		}
	}
	t.Fatalf("missing occurrence location=%q line=%d snippet=%q in %#v", location, line, snippet, occurrences)
}
