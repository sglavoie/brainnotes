package web

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/recall"
)

func TestConnectionDraftWritesBothSourcesAndReplays(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{Hosted: true})
	response, err := ts.Client().Get(ts.URL + "/new?source=alpha&source=beta")
	if err != nil {
		t.Fatal(err)
	}
	page := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(page, "[[Alpha]] ↔ [[Beta]]") {
		t.Fatalf("connection sources missing: %d", response.StatusCode)
	}
	body := "## Connection\n\n[[Alpha]] ↔ [[Beta]]\n\nA thought with Unicode: café.\n"
	form := url.Values{"title": {"Connection"}, "body": {body}, "save_id": {"connection-draft-test"}}
	result := decodeSourceSave(t, postSourceRoute(t, ts, newNoteAPIRoutePath, form, sourceTestToken))
	content, err := os.ReadFile(filepath.Join(root, result.RelPath))
	if err != nil || !strings.HasSuffix(string(content), body) {
		t.Fatalf("draft body changed: %q, %v", content, err)
	}
	assertNewNoteReplay(t, ts, form, result.Revision)
}

func TestConnectionDraftRejectsUnavailableSources(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})
	for _, query := range []string{"source=alpha", "source=alpha&source=missing", "source=alpha&source=../../private"} {
		response, err := ts.Client().Get(ts.URL + "/new?" + query)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatalf("accepted unavailable sources: %s", query)
		}
	}
}

func TestReferencePassagesAreBoundedAndFindContextBeyondOpening(t *testing.T) {
	blocks := make([]recall.MarkdownBlock, 20)
	for i := range blocks {
		blocks[i] = recall.MarkdownBlock{Kind: recall.BlockProse, Text: strings.Repeat("é", 900)}
	}
	blocks[19].Text = "A later passage about Deliberate Practice."
	document := recall.Document{Blocks: blocks}
	passages, truncated := peekPassages(document, "")
	count := 0
	for _, passage := range passages {
		count += len([]rune(passage.Text))
	}
	if count > 8000 || len(passages) > 12 || !truncated {
		t.Fatalf("unbounded reference: %d characters, %d passages", count, len(passages))
	}
	matched, _ := peekPassages(document, "Deliberate Practice")
	if len(matched) != 2 || matched[0].Text != blocks[18].Text || !matched[1].Relevant || matched[1].Text != blocks[19].Text {
		t.Fatalf("context lost: %+v", matched)
	}
}
