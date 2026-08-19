package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
)

func TestServer_CockpitAPI_QuarantinedNoteOmittedFromReads(t *testing.T) {
	ts, root, callCount := newBlockedLoaderServer(t, map[string][]byte{
		"Source.md":      []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
		"StalledNote.md": []byte("---\ntitle: StalledNote\n---\n[[GhostStall]]\n"),
	}, "StalledNote.md", nil, Options{CSRFToken: "csrf-token"})
	blockedPath := filepath.Join(root, "StalledNote.md")

	start := time.Now()
	queue := getCockpitPayload(t, ts.URL)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("GET /api/cockpit took %s, want a bounded response", elapsed)
	}
	// The healthy broken-target task is present; the stalled note's would-be task
	// (its [[GhostStall]] link) never appears because the note was never loaded.
	_ = findCockpitTask(t, queue.Queue, cockpit.RuleBrokenTarget, "nowhere")
	for _, task := range queue.Tasks {
		if task.Kind == cockpit.RuleBrokenTarget && task.Detail == "ghoststall" {
			t.Fatalf("stalled note's task leaked into the queue: %#v", task)
		}
	}

	// The daily-review context is also snapshot-backed and omits the stalled note.
	resp, err := http.Get(ts.URL + "/api/daily-review/context") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/daily-review/context: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("review context status = %d, want 200; body=%q", resp.StatusCode, body)
	}
	for _, needle := range []string{"StalledNote", "GhostStall"} {
		if strings.Contains(body, needle) {
			t.Fatalf("stalled note content %q leaked into review context: %q", needle, body)
		}
	}

	// Read exactly once across both endpoints — the quarantine holds and the
	// cached snapshot is reused without re-attempting the stalled read.
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("stalled note read %d times, want exactly 1", got)
	}
}

// TestServer_CockpitAPI_MutationFlowsSkipQuarantinedNote proves the
// content-addressed cockpit flows (item triage lookup, fix preview, fix apply)
// stay bounded and functional with a stalled note present: each forces a fresh
// bounded snapshot for correct stale detection, yet the quarantined note is
// never re-read and the fix's own vault access no longer performs an unbounded
// walk that would block on it.
func TestServer_CockpitAPI_MutationFlowsSkipQuarantinedNote(t *testing.T) {
	ts, root, callCount := newBlockedLoaderServer(t, map[string][]byte{
		"Source.md":      []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
		"StalledNote.md": []byte("---\ntitle: StalledNote\n---\n[[GhostStall]]\n"),
	}, "StalledNote.md", nil, Options{CSRFToken: "csrf-token"})
	blockedPath := filepath.Join(root, "StalledNote.md")

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")

	// Item triage resolves the task through the force-refreshed raw queue, which
	// re-walks but skips the quarantined note without a read.
	done := decodeCockpitResult(t, readResponseBody(t, postCockpitItem(t, ts.URL, task.ID, "mark_done", "", "csrf-token", "")))
	if _, ok := cockpitTaskByID(done.Queue, task.ID); ok {
		t.Fatalf("done task remained active: %#v", done)
	}
	cleared := decodeCockpitResult(t, readResponseBody(t, postCockpitItem(t, ts.URL, task.ID, "clear", "", "csrf-token", "")))
	if _, ok := cockpitTaskByID(cleared.Queue, task.ID); !ok {
		t.Fatalf("cleared task did not return: %#v", cleared)
	}

	// Fix preview + apply create the stub for the healthy broken target; the
	// snapshot-backed Fixer never walks the vault, so the stalled note is untouched.
	previewResp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "csrf-token", "")
	preview := decodeFixPreview(t, readResponseBody(t, previewResp))
	if previewResp.StatusCode != http.StatusOK || strings.TrimSpace(preview.PreviewID) == "" {
		t.Fatalf("preview = %#v (status %d), want ok + preview id", preview, previewResp.StatusCode)
	}

	applyResp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), preview.PreviewID, "csrf-token", "")
	apply := decodeFixApply(t, readResponseBody(t, applyResp))
	if applyResp.StatusCode != http.StatusOK || len(apply.Created) != 1 || apply.Created[0] != "Nowhere.md" {
		t.Fatalf("apply = %#v (status %d), want Nowhere.md created", apply, applyResp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "Nowhere.md")); err != nil {
		t.Fatalf("apply did not create the stub: %v", err)
	}

	// Across every force-refresh (two item posts, preview, apply) plus the fix's
	// own vault access, the stalled note is read exactly once and never retried.
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("stalled note read %d times across mutation flows, want exactly 1", got)
	}
}
