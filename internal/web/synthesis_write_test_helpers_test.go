package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
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

	entries, warnings, err := journal.ListEntries(noteops.JournalRoot(root))
	if err != nil {
		t.Fatalf("list journal entries: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("journal warnings = %v, want none", warnings)
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

func writeSynthesisConfig(t *testing.T, root, source string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(source), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// writeSynthesisOverlay creates the machine-local layer. Every synthesis fixture
// starts without one, so calling this is the "a second source appeared" case.
func writeSynthesisOverlay(t *testing.T, root, source string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, config.LocalFileName), []byte(source), 0o644); err != nil {
		t.Fatalf("write %s: %v", config.LocalFileName, err)
	}
}
