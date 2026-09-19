package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServer_HistoryAPI_ListsRecentJournalEntries(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	mutationBody := readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", mutationResp.StatusCode, http.StatusOK, mutationBody)
	}
	mutationPayload := decodeManagementResult(t, mutationBody)

	resp, err := http.Get(ts.URL + "/api/history") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/history: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", got)
	}
	payload := decodeHistoryResult(t, body)
	if !payload.OK || len(payload.Entries) != 1 {
		t.Fatalf("history payload = %#v, want one entry", payload)
	}
	entry := payload.Entries[0]
	if entry.ID != mutationPayload.JournalID || entry.Command != "bn web frontmatter set" {
		t.Fatalf("entry = %#v, want mutation journal %q", entry, mutationPayload.JournalID)
	}
	if entry.FileCount != 1 || len(entry.Files) != 1 || entry.Files[0].Path != "Source.md" {
		t.Fatalf("entry files = %#v, want Source.md", entry.Files)
	}
	if entry.Files[0].Before != "file" || entry.Files[0].After != "file" || strings.TrimSpace(entry.Timestamp) == "" {
		t.Fatalf("entry state/timestamp unexpected: %#v", entry)
	}
}

func TestServer_UndoAPI_DefaultsToLatestAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	mutationBody := readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", mutationResp.StatusCode, http.StatusOK, mutationBody)
	}
	mutationPayload := decodeManagementResult(t, mutationBody)
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: done") {
		t.Fatalf("mutation did not update note:\n%s", got)
	}

	resp := postUndoRequest(t, ts.URL, "", "csrf-token", "", false, false)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeUndoResult(t, body)
	if !payload.OK || payload.EntryID != mutationPayload.JournalID || payload.Message != "Undo restored 1 path." {
		t.Fatalf("undo payload = %#v, want latest undo success", payload)
	}
	if len(payload.Changes) != 1 || payload.Changes[0].Path != "Source.md" {
		t.Fatalf("undo changes = %#v, want Source.md", payload.Changes)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") || strings.Contains(got, "status: done") {
		t.Fatalf("undo did not restore note:\n%s", got)
	}

	refreshedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed /n/source: %v", err)
	}
	refreshedBody := readResponseBody(t, refreshedResp)
	if refreshedResp.StatusCode != http.StatusOK ||
		!strings.Contains(refreshedBody, `<span class="frontmatter-text">draft</span>`) ||
		strings.Contains(refreshedBody, `<span class="frontmatter-text">done</span>`) {
		t.Fatalf("state was not refreshed after undo: status=%d body=%q", refreshedResp.StatusCode, refreshedBody)
	}
}

// newUndoConflictFixture journals one frontmatter change and then edits the
// note again behind the journal's back, so the recorded "after" state no longer
// matches disk. Restoring that entry now means replacing work the entry never
// saw, which is the only state in which force means anything. It returns the
// server, the entry identifier, and the path the conflict is at.
func newUndoConflictFixture(t *testing.T) (string, string, string) {
	t.Helper()

	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	mutationBody := readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", mutationResp.StatusCode, http.StatusOK, mutationBody)
	}
	entryID := decodeManagementResult(t, mutationBody).JournalID
	notePath := filepath.Join(root, "Source.md")
	if err := os.WriteFile(notePath, []byte("---\nstatus: blocked\ntitle: Source\n---\nBody marker.\n"), 0o644); err != nil {
		t.Fatalf("write conflicting note: %v", err)
	}
	return ts.URL, entryID, notePath
}

// assertNewerWorkSurvived is the assertion every refusal on this route owes: a
// request that was turned down must leave the newer version exactly where it
// was, or the refusal is a message rather than a guarantee.
func assertNewerWorkSurvived(t *testing.T, notePath, stage string) {
	t.Helper()

	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: blocked") {
		t.Fatalf("%s should not change the note:\n%s", stage, got)
	}
}

// postRefusedUndo posts a restore that must not happen, states the status it
// expects, and checks the newer version is still there before handing the body
// back for whatever else the refusal is supposed to say.
func postRefusedUndo(t *testing.T, baseURL, entryID, notePath, stage string, force, confirm bool, wantStatus int) string {
	t.Helper()

	resp := postUndoRequest(t, baseURL, entryID, "csrf-token", "", force, confirm)
	body := readResponseBody(t, resp)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s status: got %d, want %d; body=%q", stage, resp.StatusCode, wantStatus, body)
	}
	assertNewerWorkSurvived(t, notePath, stage)
	return body
}

// assertUndoConflictPayload checks the refusal names what it refused over. A
// conflict that said only "conflict" would leave somebody with no way to find
// out which of their work was in the way.
func assertUndoConflictPayload(t *testing.T, payload undoPayload, entryID string) {
	t.Helper()

	if payload.OK || payload.Code != "conflict" || !payload.RequiresForce || payload.EntryID != entryID {
		t.Fatalf("conflict payload = %#v, want force-required conflict", payload)
	}
	if len(payload.Conflicts) != 1 || payload.Conflicts[0].Path != "Source.md" {
		t.Fatalf("conflicts = %#v, want Source.md", payload.Conflicts)
	}
	if strings.TrimSpace(payload.Conflicts[0].ExpectedState) == "" || strings.TrimSpace(payload.Conflicts[0].CurrentState) == "" {
		t.Fatalf("conflict states missing: %#v", payload.Conflicts[0])
	}
}

// assertRestorePreviewNamesTheConflict checks the preview says what the consent
// is being asked for: the contested path, that it is a note, and that it is both
// in the content history and portable — the two facts the panel turns into
// "recorded here, and travels to your other replica".
func assertRestorePreviewNamesTheConflict(t *testing.T, preview historyPreviewPayload) {
	t.Helper()

	if !preview.RequiresForce || len(preview.Conflicts) != 1 || preview.Conflicts[0].Path != "Source.md" {
		t.Fatalf("preview = %#v, want the Source.md conflict", preview)
	}
	if len(preview.Changes) != 1 || !preview.Changes[0].Conflict || preview.Changes[0].Class != "note" {
		t.Fatalf("preview changes = %#v, want a conflicting note", preview.Changes)
	}
	if !preview.Changes[0].Recorded || !preview.Changes[0].Travels {
		t.Fatalf("preview change facts = %#v, want a recorded, portable path", preview.Changes[0])
	}
}

func TestServer_UndoAPI_ConflictRefusesAndNamesWhatItWouldReplace(t *testing.T) {
	baseURL, entryID, notePath := newUndoConflictFixture(t)

	body := postRefusedUndo(t, baseURL, entryID, notePath, "the refused restore", false, false, http.StatusConflict)
	assertUndoConflictPayload(t, decodeUndoResult(t, body), entryID)

	postRefusedUndo(t, baseURL, entryID, notePath, "an unconfirmed force", true, false, http.StatusBadRequest)

	// Confirmed but unpreviewed is still refused: the confirmation flag says the
	// overwrite was accepted, and the receipt is what says it was shown.
	unpreviewed := postRefusedUndo(t, baseURL, entryID, notePath, "an unpreviewed force", true, true, http.StatusConflict)
	if got := decodeManagementError(t, unpreviewed).Code; got != "preview_required" {
		t.Fatalf("unpreviewed force code = %q, want preview_required; body=%q", got, unpreviewed)
	}
}

func TestServer_UndoAPI_PreviewedForceReplacesTheNewerWork(t *testing.T) {
	baseURL, entryID, notePath := newUndoConflictFixture(t)

	preview := previewHistoryRestore(t, baseURL, entryID, "csrf-token")
	assertRestorePreviewNamesTheConflict(t, preview)
	assertNewerWorkSurvived(t, notePath, "a preview")

	forceResp := postUndoRequestWithPreview(t, baseURL, entryID, "csrf-token", "", preview.PreviewID, true, true)
	forceBody := readResponseBody(t, forceResp)
	if forceResp.StatusCode != http.StatusOK {
		t.Fatalf("force status: got %d, want %d; body=%q", forceResp.StatusCode, http.StatusOK, forceBody)
	}
	forcePayload := decodeUndoResult(t, forceBody)
	if !forcePayload.OK || forcePayload.EntryID != entryID {
		t.Fatalf("force payload = %#v, want undo success", forcePayload)
	}
	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: draft") || strings.Contains(got, "status: blocked") {
		t.Fatalf("force undo did not restore note:\n%s", got)
	}
}

func TestServer_UndoAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d", mutationResp.StatusCode, http.StatusOK)
	}

	resp := postUndoRequest(t, ts.URL, "", "", "", false, false)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: done") {
		t.Fatalf("note changed without csrf:\n%s", got)
	}
}
