package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

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

// Every other overlay test on this route asserts a refusal, so none of them
// would notice the overlay requirement being wrong in the direction that matters
// most: an overlay that is present and unmoved has to let the write through. The
// requirement's ExpectHash is built from config's own fingerprint and verified
// by vault against the file, and the two only agree because they hash the same
// way — which nothing here would catch if it stopped being true.
func TestSynthesisApplyAPI_CreatesTheNoteWithAnUnchangedOverlayPresent(t *testing.T) {
	ts, _, root := newSynthesisServer(t, Options{})
	// Written before the context is built, so the packet's requirements name an
	// overlay that exists rather than one that is absent.
	writeSynthesisOverlay(t, root, "[open]\neditor = \"hx\"\n")

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	preview := buildSynthesisPreview(t, ts.URL, defaultSynthesisPreviewForm(built.ContextID))

	status, body := applySynthesisPreview(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(synthesisDraftRel))); err != nil {
		t.Fatalf("stat created note: %v", err)
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
		// The configuration is two files, and the overlay's absence is as much a
		// part of the reading as its contents: one that appeared is a layer
		// nothing in the reviewed plan ever read.
		"overlay appeared": func(t *testing.T, root string) {
			writeSynthesisOverlay(t, root, "[open]\neditor = \"hx\"\n")
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
		"overlay appeared": func(t *testing.T, root string) {
			writeSynthesisOverlay(t, root, "[open]\neditor = \"hx\"\n")
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
