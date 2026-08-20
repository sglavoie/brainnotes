package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const (
	synthesisDraftTitle  = "Quorum Synthesis"
	synthesisDraftFolder = "Notes"
	synthesisDraftRel    = "Notes/Quorum Synthesis.md"
	synthesisDraftBody   = "A synthesized paragraph about quorum size.\n\nAnd a second paragraph."

	// synthesisPlannedNote is the whole file a default preview plans. Pinning
	// the bytes rather than fragments is the point of the preview contract:
	// apply writes this and nothing else.
	synthesisPlannedNote = "---\ntitle: " + synthesisDraftTitle + "\n---\n" +
		synthesisDraftBody + "\n\n" +
		"## Sources\n" +
		"- [[Notes/Anchor|Anchor]]\n" +
		"- [[Notes/Alpha|Alpha]]\n" +
		"- [[Notes/Beta|Beta]]\n"
)

func defaultSynthesisPreviewForm(contextID string) url.Values {
	form := url.Values{}
	form.Set("context_id", contextID)
	form.Set("title", synthesisDraftTitle)
	form.Set("folder", synthesisDraftFolder)
	form.Set("draft", synthesisDraftBody)
	return form
}

func decodeSynthesisPreview(t *testing.T, body string) synthesisPreviewResponse {
	t.Helper()

	var decoded synthesisPreviewResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode preview response %q: %v", body, err)
	}
	return decoded
}

func decodeSynthesisResult(t *testing.T, body string) noteops.Result {
	t.Helper()

	var decoded noteops.Result
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode apply response %q: %v", body, err)
	}
	return decoded
}

// buildSynthesisPreview posts one preview request and fails the test unless it
// succeeds, so the many tests that only need a live receipt do not repeat the
// assertion.
func buildSynthesisPreview(t *testing.T, baseURL string, form url.Values) synthesisPreviewResponse {
	t.Helper()

	status, body := postSynthesisForm(t, baseURL, synthesisPreviewAPIRoutePath, synthesisTestToken, form)
	if status != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", status, body)
	}
	return decodeSynthesisPreview(t, body)
}

// newSynthesisDraftServer builds a server, a live context, and a live receipt
// for the default draft: the starting point of every apply-time case.
func newSynthesisDraftServer(t *testing.T) (baseURL, root string, srv *Server, preview synthesisPreviewResponse) {
	t.Helper()

	ts, srv, root := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	preview = buildSynthesisPreview(t, ts.URL, defaultSynthesisPreviewForm(built.ContextID))
	return ts.URL, root, srv, preview
}

func applySynthesisPreview(t *testing.T, baseURL, previewID string) (int, string) {
	t.Helper()

	status, body, err := postSynthesisApply(baseURL, previewID)
	if err != nil {
		t.Fatalf("POST %s: %v", synthesisApplyAPIRoutePath, err)
	}
	return status, body
}

// postSynthesisApply sends one apply request without touching *testing.T, so the
// concurrency case can call it from a goroutine.
func postSynthesisApply(baseURL, previewID string) (int, string, error) {
	form := url.Values{}
	form.Set("preview_id", previewID)

	req, err := http.NewRequest(http.MethodPost, baseURL+synthesisApplyAPIRoutePath, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeaderName, synthesisTestToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, string(body), nil
}

func synthesisJournalEntries(t *testing.T, root string) []journal.Entry {
	t.Helper()

	entries, err := journal.ListEntries(noteops.JournalRoot(root))
	if err != nil {
		t.Fatalf("list journal entries: %v", err)
	}
	return entries
}

// requireSynthesisRefusal asserts the shape every refusal shares: the planned
// destination was not created and nothing reached the undo journal.
func requireSynthesisRefusal(t *testing.T, root string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))); !os.IsNotExist(err) {
		t.Fatalf("stat destination after a refusal: err = %v, want not-exist", err)
	}
	if entries := synthesisJournalEntries(t, root); len(entries) != 0 {
		t.Fatalf("journal holds %d entries after a refusal, want 0", len(entries))
	}
}

func TestSynthesisPreviewAPI_ReturnsExactPlannedNote(t *testing.T) {
	ts, _, root := newSynthesisServer(t, Options{})

	before := vaultFileSet(t, root)
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	preview := buildSynthesisPreview(t, ts.URL, defaultSynthesisPreviewForm(built.ContextID))

	if !preview.OK {
		t.Fatal("ok = false, want a success envelope")
	}
	if len(preview.PreviewID) != 64 {
		t.Fatalf("preview_id length = %d, want 64", len(preview.PreviewID))
	}
	if _, err := time.Parse(time.RFC3339, preview.ExpiresAt); err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", preview.ExpiresAt, err)
	}
	if preview.ContextID != built.ContextID {
		t.Fatalf("context_id = %q, want %q", preview.ContextID, built.ContextID)
	}
	if preview.Destination != synthesisDraftRel {
		t.Fatalf("destination = %q, want %q", preview.Destination, synthesisDraftRel)
	}
	if preview.Markdown != synthesisPlannedNote {
		t.Fatalf("markdown =\n%q\nwant\n%q", preview.Markdown, synthesisPlannedNote)
	}
	if !strings.Contains(preview.RenderedHTML, "A synthesized paragraph about quorum size.") {
		t.Fatalf("rendered_html did not render the draft: %q", preview.RenderedHTML)
	}
	if !strings.Contains(preview.RenderedHTML, "<h2") {
		t.Fatalf("rendered_html did not render the Sources heading: %q", preview.RenderedHTML)
	}
	if strings.Contains(preview.RenderedHTML, "title: "+synthesisDraftTitle) || strings.Contains(preview.RenderedHTML, "<hr") {
		t.Fatalf("rendered_html rendered the frontmatter block: %q", preview.RenderedHTML)
	}

	requireKeyOrder(t, vaultFileSet(t, root), before, "vault contents")
	requireSynthesisRefusal(t, root)
}

// TestSynthesisPreviewAPI_AcceptsAnAgentGeneratedDraft proves the workbench's
// two draft origins converge: a candidate the command produced becomes the same
// planned note as one a user typed.
func TestSynthesisPreviewAPI_AcceptsAnAgentGeneratedDraft(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				return []byte(synthesisDraftBody + "\n"), nil
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	generate := url.Values{}
	generate.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, generate)
	if status != http.StatusOK {
		t.Fatalf("generate status = %d, body = %s", status, body)
	}
	generated := decodeSynthesisGenerate(t, body)

	form := defaultSynthesisPreviewForm(built.ContextID)
	form.Set("draft", generated.Output)

	preview := buildSynthesisPreview(t, ts.URL, form)
	if preview.Markdown != synthesisPlannedNote {
		t.Fatalf("markdown =\n%q\nwant\n%q", preview.Markdown, synthesisPlannedNote)
	}
}

// TestSynthesisPreviewAPI_UsesTheAnchorOnlySelection covers the smallest packet:
// one source, one link.
func TestSynthesisPreviewAPI_UsesTheAnchorOnlySelection(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	anchorOnly := url.Values{}
	anchorOnly.Set("source", synthesisAnchorKey)
	built := buildSynthesisContextRequest(t, ts.URL, anchorOnly)

	form := defaultSynthesisPreviewForm(built.ContextID)
	form.Set("folder", "")

	preview := buildSynthesisPreview(t, ts.URL, form)
	if preview.Destination != synthesisDraftTitle+".md" {
		t.Fatalf("destination = %q, want a vault-root note", preview.Destination)
	}
	if !strings.HasSuffix(preview.Markdown, "## Sources\n- [[Notes/Anchor|Anchor]]\n") {
		t.Fatalf("markdown does not end with the single source link:\n%q", preview.Markdown)
	}
}

func TestSynthesisPreviewAPI_RejectsBadRequests(t *testing.T) {
	ts, _, root := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())

	blankTitle := defaultSynthesisPreviewForm(built.ContextID)
	blankTitle.Set("title", "   ")

	emptyDraft := defaultSynthesisPreviewForm(built.ContextID)
	emptyDraft.Set("draft", "\n\n   \n")

	reservedHeading := defaultSynthesisPreviewForm(built.ContextID)
	reservedHeading.Set("draft", "Body text.\n\n## Sources\n- [[Notes/Alpha]]\n")

	oversizeDraft := defaultSynthesisPreviewForm(built.ContextID)
	oversizeDraft.Set("draft", strings.Repeat("x", noteops.MaxSynthesisDraftBytes+1))

	escapingFolder := defaultSynthesisPreviewForm(built.ContextID)
	escapingFolder.Set("folder", "../outside")

	for name, form := range map[string]url.Values{
		"missing context id": {"title": {synthesisDraftTitle}, "draft": {synthesisDraftBody}},
		"blank title":        blankTitle,
		"empty draft":        emptyDraft,
		"reserved heading":   reservedHeading,
		"oversize draft":     oversizeDraft,
		"escaping folder":    escapingFolder,
	} {
		t.Run(name, func(t *testing.T) {
			status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, synthesisTestToken, form)
			if status == http.StatusOK {
				t.Fatalf("preview accepted %s: %s", name, body)
			}
			decoded := decodeSynthesisError(t, body)
			if decoded.OK {
				t.Fatalf("ok = true for %s: %s", name, body)
			}
			requireSynthesisRefusal(t, root)
		})
	}
}

// TestSynthesisPreviewAPI_RefusesAnExcludedDestination keeps the workbench from
// creating a note the vault walk would never visit.
func TestSynthesisPreviewAPI_RefusesAnExcludedDestination(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())

	form := defaultSynthesisPreviewForm(built.ContextID)
	form.Set("folder", "Archive")

	status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
}

func TestSynthesisPreviewAPI_RejectsBadTransport(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())

	t.Run("non-POST", func(t *testing.T) {
		resp, err := http.Get(ts.URL + synthesisPreviewAPIRoutePath) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET preview: %v", err)
		}
		body := readResponseBody(t, resp)
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Fatalf("Allow = %q, want POST", got)
		}
		requireSynthesisError(t, resp.StatusCode, body, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	})

	t.Run("csrf", func(t *testing.T) {
		status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, "wrong-token", defaultSynthesisPreviewForm(built.ContextID))
		requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
	})
}

// TestSynthesisPreviewAPI_RefusesStaleContext covers a vault that moved between
// the context build and the preview: the draft was written against bytes that no
// longer exist, so no receipt is minted.
func TestSynthesisPreviewAPI_RefusesStaleContext(t *testing.T) {
	for name, disturb := range map[string]func(t *testing.T, root string){
		"edited anchor": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Anchor.md", "---\ntitle: Anchor\n---\nAnchor says something else now.\n\nA second paragraph.\n")
		},
		"edited supporter": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha says something else now.\n\nA second paragraph.\n")
		},
		"removed supporter": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "Notes", "Beta.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		},
		"changed configuration": func(t *testing.T, root string) {
			writeSynthesisConfig(t, root, synthesisTestConfigTOML+"\n[frontmatter.properties.status]\ntype = \"string\"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			ts, srv, root := newSynthesisServer(t, Options{})
			built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
			disturb(t, root)

			status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, synthesisTestToken, defaultSynthesisPreviewForm(built.ContextID))
			requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
			if size := srv.synthesisPreviews.size(); size != 0 {
				t.Fatalf("preview store holds %d receipts after a refusal, want 0", size)
			}
			requireSynthesisRefusal(t, root)
		})
	}
}

func TestSynthesisApplyAPI_CreatesTheReviewedNote(t *testing.T) {
	baseURL, root, srv, preview := newSynthesisDraftServer(t)

	status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", status, body)
	}
	result := decodeSynthesisResult(t, body)
	if !result.OK {
		t.Fatalf("ok = false: %s", body)
	}
	if result.RelPath != synthesisDraftRel {
		t.Fatalf("rel_path = %q, want %q", result.RelPath, synthesisDraftRel)
	}
	if result.Key != "notes/quorum synthesis" {
		t.Fatalf("key = %q, want notes/quorum synthesis", result.Key)
	}
	if result.Path != "/n/notes/quorum%20synthesis" {
		t.Fatalf("path = %q, want /n/notes/quorum%%20synthesis", result.Path)
	}
	if result.JournalID == "" {
		t.Fatal("journal_id is empty")
	}

	onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))) // #nosec G304 -- test-owned temp vault
	if err != nil {
		t.Fatalf("read created note: %v", err)
	}
	if string(onDisk) != preview.Markdown {
		t.Fatalf("created note =\n%q\nwant the previewed bytes\n%q", string(onDisk), preview.Markdown)
	}

	if size := srv.synthesisPreviews.size(); size != 0 {
		t.Fatalf("preview store holds %d receipts after apply, want 0", size)
	}

	// The snapshot must already carry the new note: the browser follows the
	// returned route immediately.
	page, err := http.Get(baseURL + result.Path) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET created note: %v", err)
	}
	pageBody := readResponseBody(t, page)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", result.Path, page.StatusCode)
	}
	if !strings.Contains(pageBody, "A synthesized paragraph about quorum size.") {
		t.Fatal("the created note page does not render the draft")
	}
}

func TestSynthesisApplyAPI_RecordsAnUndoableEntry(t *testing.T) {
	baseURL, root, _, preview := newSynthesisDraftServer(t)

	status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", status, body)
	}
	result := decodeSynthesisResult(t, body)

	historyResp, err := http.Get(baseURL + "/api/history") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET history: %v", err)
	}
	historyBody := readResponseBody(t, historyResp)
	if historyResp.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", historyResp.StatusCode, historyBody)
	}
	var history historyResponse
	if err := json.Unmarshal([]byte(historyBody), &history); err != nil {
		t.Fatalf("decode history %q: %v", historyBody, err)
	}
	if len(history.Entries) != 1 {
		t.Fatalf("history holds %d entries, want 1", len(history.Entries))
	}
	if history.Entries[0].ID != result.JournalID {
		t.Fatalf("history id = %q, want %q", history.Entries[0].ID, result.JournalID)
	}
	if history.Entries[0].Command != "bn web synthesize" {
		t.Fatalf("history command = %q, want bn web synthesize", history.Entries[0].Command)
	}
	if history.Entries[0].FileCount != 1 {
		t.Fatalf("history file_count = %d, want 1", history.Entries[0].FileCount)
	}

	undo := url.Values{}
	undo.Set("id", result.JournalID)
	status, body = postSynthesisForm(t, baseURL, "/api/undo", synthesisTestToken, undo)
	if status != http.StatusOK {
		t.Fatalf("undo status = %d, body = %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))); !os.IsNotExist(err) {
		t.Fatalf("stat destination after undo: err = %v, want not-exist", err)
	}
}

func TestSynthesisApplyAPI_RejectsUnusableReceipts(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		baseURL, root, _, _ := newSynthesisDraftServer(t)
		status, body := applySynthesisPreview(t, baseURL, "")
		requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewRequired)
		requireSynthesisRefusal(t, root)
	})

	t.Run("unknown", func(t *testing.T) {
		baseURL, root, _, _ := newSynthesisDraftServer(t)
		status, body := applySynthesisPreview(t, baseURL, strings.Repeat("a", 64))
		requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewRequired)
		requireSynthesisRefusal(t, root)
	})

	// A context identifier is the same width as a receipt, so posting one is the
	// mistake a confused client actually makes.
	t.Run("mismatched", func(t *testing.T) {
		ts, _, root := newSynthesisServer(t, Options{})
		built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
		buildSynthesisPreview(t, ts.URL, defaultSynthesisPreviewForm(built.ContextID))

		status, body := applySynthesisPreview(t, ts.URL, built.ContextID)
		requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewRequired)
		requireSynthesisRefusal(t, root)
	})

	t.Run("expired", func(t *testing.T) {
		baseURL, root, srv, preview := newSynthesisDraftServer(t)
		srv.synthesisPreviews.setNow(func() time.Time { return time.Now().Add(2 * synthesisPreviewTTL) })

		status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
		requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewRequired)
		requireSynthesisRefusal(t, root)
	})

	t.Run("replayed", func(t *testing.T) {
		baseURL, root, _, preview := newSynthesisDraftServer(t)

		status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
		if status != http.StatusOK {
			t.Fatalf("first apply status = %d, body = %s", status, body)
		}
		status, body = applySynthesisPreview(t, baseURL, preview.PreviewID)
		requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewRequired)

		if entries := synthesisJournalEntries(t, root); len(entries) != 1 {
			t.Fatalf("journal holds %d entries after a replay, want 1", len(entries))
		}
	})

	t.Run("csrf", func(t *testing.T) {
		baseURL, root, _, preview := newSynthesisDraftServer(t)
		form := url.Values{}
		form.Set("preview_id", preview.PreviewID)
		status, body := postSynthesisForm(t, baseURL, synthesisApplyAPIRoutePath, "", form)
		requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
		requireSynthesisRefusal(t, root)
	})

	t.Run("non-POST", func(t *testing.T) {
		baseURL, _, _, _ := newSynthesisDraftServer(t)
		resp, err := http.Get(baseURL + synthesisApplyAPIRoutePath) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET apply: %v", err)
		}
		body := readResponseBody(t, resp)
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Fatalf("Allow = %q, want POST", got)
		}
		requireSynthesisError(t, resp.StatusCode, body, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	})

	t.Run("oversize body", func(t *testing.T) {
		baseURL, root, _, preview := newSynthesisDraftServer(t)
		form := url.Values{}
		form.Set("preview_id", preview.PreviewID)
		form.Set("filler", strings.Repeat("x", synthesisApplyBodyLimit+1))
		status, body := postSynthesisForm(t, baseURL, synthesisApplyAPIRoutePath, synthesisTestToken, form)
		requireSynthesisError(t, status, body, http.StatusBadRequest, noteops.CodeInvalidRequest)
		requireSynthesisRefusal(t, root)
	})
}

// TestSynthesisApplyAPI_RedeemsAReceiptOnce proves the one-use contract holds
// under concurrency, which is the case a replay guard keyed on anything but the
// store's own mutex would lose.
func TestSynthesisApplyAPI_RedeemsAReceiptOnce(t *testing.T) {
	baseURL, root, _, preview := newSynthesisDraftServer(t)

	const attempts = 4
	statuses := make([]int, attempts)
	bodies := make([]string, attempts)
	failures := make([]error, attempts)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range attempts {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			statuses[index], bodies[index], failures[index] = postSynthesisApply(baseURL, preview.PreviewID)
		}()
	}
	start.Done()
	done.Wait()

	created := 0
	for index := range attempts {
		if failures[index] != nil {
			t.Fatalf("attempt %d: %v", index, failures[index])
		}
		switch statuses[index] {
		case http.StatusOK:
			created++
		case http.StatusConflict:
			if decoded := decodeSynthesisError(t, bodies[index]); decoded.Code != noteops.CodePreviewRequired {
				t.Fatalf("losing attempt code = %q, want %q", decoded.Code, noteops.CodePreviewRequired)
			}
		default:
			t.Fatalf("attempt %d status = %d, body = %s", index, statuses[index], bodies[index])
		}
	}
	if created != 1 {
		t.Fatalf("%d concurrent attempts created the note, want 1", created)
	}
	if entries := synthesisJournalEntries(t, root); len(entries) != 1 {
		t.Fatalf("journal holds %d entries, want 1", len(entries))
	}
}

// TestSynthesisApplyAPI_RefusesStalePreview covers the window between the review
// and the apply: the user approved a note assembled from bytes that have since
// changed, so the receipt is refused before anything is written.
func TestSynthesisApplyAPI_RefusesStalePreview(t *testing.T) {
	for name, disturb := range map[string]func(t *testing.T, root string){
		"edited anchor": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Anchor.md", "---\ntitle: Anchor\n---\nAnchor says something else now.\n\nA second paragraph.\n")
		},
		"edited supporter": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Beta.md", "---\ntitle: Beta\n---\nBeta says something else now.\n\nA second paragraph.\n")
		},
		"removed supporter": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "Notes", "Alpha.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		},
		"changed configuration": func(t *testing.T, root string) {
			writeSynthesisConfig(t, root, synthesisTestConfigTOML+"\n[frontmatter.properties.status]\ntype = \"string\"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			baseURL, root, _, preview := newSynthesisDraftServer(t)
			disturb(t, root)

			status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
			requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewStale)
			requireSynthesisRefusal(t, root)
		})
	}
}

// TestSynthesisApplyAPI_RefusesADestinationTakenAfterPreview keeps a synthesis
// create-only: a note that appeared at the reviewed path is never overwritten.
func TestSynthesisApplyAPI_RefusesADestinationTakenAfterPreview(t *testing.T) {
	baseURL, root, _, preview := newSynthesisDraftServer(t)
	writeTestFile(t, root, synthesisDraftRel, "---\ntitle: Quorum Synthesis\n---\nSomeone else got here first.\n")

	status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
	requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodePreviewStale)

	onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))) // #nosec G304 -- test-owned temp vault
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if !strings.Contains(string(onDisk), "Someone else got here first.") {
		t.Fatalf("the pre-existing note was overwritten: %q", string(onDisk))
	}
	if entries := synthesisJournalEntries(t, root); len(entries) != 0 {
		t.Fatalf("journal holds %d entries after a refusal, want 0", len(entries))
	}
}

// TestSynthesisApplyAPI_RefusesAChangeDuringCommit covers the window the
// web-layer revalidation cannot close: the write itself has to refuse a source
// replaced after the receipt was verified. The pre-commit seam is the only way
// to land that edit deterministically.
func TestSynthesisApplyAPI_RefusesAChangeDuringCommit(t *testing.T) {
	for name, disturb := range map[string]func(t *testing.T, root string){
		"edited supporter": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha changed during the commit.\n\nA second paragraph.\n")
		},
		"changed configuration": func(t *testing.T, root string) {
			writeSynthesisConfig(t, root, synthesisTestConfigTOML+"\n[frontmatter.properties.status]\ntype = \"string\"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			baseURL, root, srv, preview := newSynthesisDraftServer(t)
			srv.beforeSynthesisCommit = func() { disturb(t, root) }

			status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
			requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
			requireSynthesisRefusal(t, root)
		})
	}
}

func TestSynthesisApplyAPI_RefusesADestinationTakenDuringCommit(t *testing.T) {
	baseURL, root, srv, preview := newSynthesisDraftServer(t)
	srv.beforeSynthesisCommit = func() {
		writeTestFile(t, root, synthesisDraftRel, "---\ntitle: Quorum Synthesis\n---\nSomeone else got here first.\n")
	}

	status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
	requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)

	onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))) // #nosec G304 -- test-owned temp vault
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if !strings.Contains(string(onDisk), "Someone else got here first.") {
		t.Fatalf("the racing note was overwritten: %q", string(onDisk))
	}
	if entries := synthesisJournalEntries(t, root); len(entries) != 0 {
		t.Fatalf("journal holds %d entries after a refusal, want 0", len(entries))
	}
}

// TestSynthesisApplyAPI_SurvivesAnExpiredContext pins the decision that a
// receipt stands on its own: the plan already carries every source revision, so
// a draft that outlived its context packet still applies.
func TestSynthesisApplyAPI_SurvivesAnExpiredContext(t *testing.T) {
	ts, srv, root := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	preview := buildSynthesisPreview(t, ts.URL, defaultSynthesisPreviewForm(built.ContextID))

	srv.synthesisContexts.setNow(func() time.Time { return time.Now().Add(2 * synthesisContextTTL) })

	status, body := applySynthesisPreview(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", status, body)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))) // #nosec G304 -- test-owned temp vault
	if err != nil {
		t.Fatalf("read created note: %v", err)
	}
	if string(onDisk) != preview.Markdown {
		t.Fatalf("created note =\n%q\nwant the previewed bytes", string(onDisk))
	}
}

func writeSynthesisConfig(t *testing.T, root, source string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(source), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
