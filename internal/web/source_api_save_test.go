package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestSourceSaveWritesTheSubmittedBytesAndReportsTheNewRevision(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	edited := "---\ntitle: Alpha\nstatus: done\n---\n\nAlpha body, edited, still [[Beta]]."

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", edited)
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	payload := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))

	if got := readSourceNote(t, root); got != edited {
		t.Fatalf("the note on disk is not what was submitted:\n%q", got)
	}
	if !payload.Changed || payload.Created || payload.Replayed {
		t.Fatalf("payload = %+v, want a plain change", payload)
	}
	if want := vault.HashBytes([]byte(edited)); payload.Revision != want {
		t.Fatalf("revision = %q, want %q", payload.Revision, want)
	}
	if payload.JournalID == "" {
		t.Fatal("the response names no undo entry")
	}
	if !strings.Contains(payload.Message, "Saved") {
		t.Fatalf("message = %q", payload.Message)
	}
}

// TestSourceSaveRefusalCarriesTheCurrentSource is the stale case at the HTTP
// boundary. A 409 with a message alone would leave an editor with nothing to do
// but tell somebody to copy their draft somewhere and reload, so the refusal
// carries the note's current bytes and revision and a merge becomes possible.
func TestSourceSaveRefusalCarriesTheCurrentSource(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", "---\ntitle: Alpha\n---\n\nmy draft.\n")
	form.Set("revision", vault.HashBytes([]byte("a version this vault never held")))
	payload := decodeSourceError(t,
		postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken),
		http.StatusConflict, noteops.CodeConflict)

	if payload.Content != sourceTestNote {
		t.Fatalf("the refusal did not carry the current source:\n%q", payload.Content)
	}
	if want := vault.HashBytes([]byte(sourceTestNote)); payload.Revision != want {
		t.Fatalf("revision = %q, want the current %q", payload.Revision, want)
	}
	if payload.Changed {
		t.Fatal("a refused save reported the bytes as changed")
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a refused save wrote anyway:\n%q", got)
	}
}

// TestSourceSaveReportsWhatItDidNotDo covers the successes that are not writes:
// a resubmission the note already agrees with, a replayed save id, and a
// replayed create. Wording any of them "Saved" — or "Created" — is the sentence
// that hides a duplicate.
func TestSourceSaveReportsWhatItDidNotDo(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})
	edited := "---\ntitle: Alpha\nstatus: done\n---\n\nedited once.\n"
	base := vault.HashBytes([]byte(sourceTestNote))

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", edited)
	form.Set("revision", base)
	form.Set("save_id", "editor-1")
	first := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))

	// The same submission again, with the base revision the first one replaced.
	second := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))
	if second.Changed {
		t.Fatalf("the resubmission claimed to write: %+v", second)
	}
	if second.Revision != first.Revision {
		t.Fatalf("revision = %q, want %q", second.Revision, first.Revision)
	}
	if strings.Contains(second.Message, "Saved") {
		t.Fatalf("message = %q, want it to say the content was already there", second.Message)
	}

	// A different edit under the same save id is the one duplicate that must be
	// refused rather than answered.
	form.Set("content", "---\ntitle: Alpha\n---\n\na different edit.\n")
	form.Set("revision", first.Revision)
	decodeSourceError(t,
		postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken),
		http.StatusConflict, noteops.CodeConflict)

	// A replayed create carries both facts at once: the record says the note was
	// created, and this call is the resubmission that did not create it. The
	// answer has to be the second one, or the message is the same lie as "Saved".
	create := url.Values{}
	create.Set("rel_path", "Drafts/Keyed.md")
	create.Set("content", "---\ntitle: Keyed\n---\n\nKeyed body.\n")
	create.Set("save_id", "editor-create-1")
	created := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, create, sourceTestToken))
	if !created.Created || !created.Changed {
		t.Fatalf("the first create = %+v, want a created change", created)
	}
	replayed := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, create, sourceTestToken))
	if !replayed.Replayed || replayed.Changed {
		t.Fatalf("the resubmitted create = %+v, want replayed and unchanged", replayed)
	}
	if strings.Contains(replayed.Message, "Created") {
		t.Fatalf("message = %q, want it to say the save id was already used", replayed.Message)
	}
}

func TestSourceRoutesRefuseInvalidFrontmatter(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	broken := "---\ntitle: [unterminated\n---\n\nbody\n"

	save := url.Values{}
	save.Set("key", "alpha")
	save.Set("content", broken)
	save.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	decodeSourceError(t,
		postSourceRoute(t, ts, sourceSaveAPIRoutePath, save, sourceTestToken),
		http.StatusBadRequest, noteops.CodeInvalidRequest)

	preview := url.Values{}
	preview.Set("content", broken)
	decodeSourceError(t,
		postSourceRoute(t, ts, sourcePreviewAPIRoutePath, preview, sourceTestToken),
		http.StatusBadRequest, noteops.CodeInvalidRequest)

	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a refused save wrote anyway:\n%q", got)
	}
}

// TestSourcePreviewRendersWithoutWriting proves the preview is a read. It also
// checks the wikilink resolved against the real graph, because a preview that
// renders links differently from the saved note is a preview of another note.
func TestSourcePreviewRendersWithoutWriting(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("content", "---\ntitle: Draft\n---\n\n## Heading\n\nA link to [[Beta]] and one to [[Nowhere]].\n")
	resp := postSourceRoute(t, ts, sourcePreviewAPIRoutePath, form, sourceTestToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		OK           bool   `json:"ok"`
		RenderedHTML string `json:"rendered_html"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.OK || !strings.Contains(payload.RenderedHTML, "Heading") {
		t.Fatalf("rendered_html = %q", payload.RenderedHTML)
	}
	// The frontmatter is parsed off rather than rendered as a horizontal rule.
	if strings.Contains(payload.RenderedHTML, "title: Draft") {
		t.Fatalf("the preview rendered the properties block as prose: %q", payload.RenderedHTML)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a preview wrote to the vault:\n%q", got)
	}
}
