package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// --- fix preview + apply ---------------------------------------------------

func TestServer_FixesAPI_CreateStubPreviewAndApply(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")

	previewResp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want 200; body=%q", previewResp.StatusCode, previewBody)
	}
	preview := decodeFixPreview(t, previewBody)
	if !preview.OK || strings.TrimSpace(preview.PreviewID) == "" {
		t.Fatalf("preview = %#v, want ok + preview id", preview)
	}
	if len(preview.Previews) != 1 || preview.Previews[0].RelPath != "Nowhere.md" {
		t.Fatalf("preview previews = %#v, want a Nowhere.md preview", preview.Previews)
	}
	// Preview must not create the stub.
	if _, err := os.Stat(filepath.Join(root, "Nowhere.md")); !os.IsNotExist(err) {
		t.Fatalf("preview created the stub: %v", err)
	}

	applyResp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), preview.PreviewID, "csrf-token", "")
	applyBody := readResponseBody(t, applyResp)
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("apply status: got %d, want 200; body=%q", applyResp.StatusCode, applyBody)
	}
	apply := decodeFixApply(t, applyBody)
	if !apply.OK || len(apply.Created) != 1 || apply.Created[0] != "Nowhere.md" {
		t.Fatalf("apply = %#v, want Nowhere.md created", apply)
	}
	if apply.JournalID == "" {
		t.Fatalf("apply journal id empty: %#v", apply)
	}
	if got := readNoteFile(t, filepath.Join(root, "Nowhere.md")); !strings.Contains(got, "title: Nowhere") {
		t.Fatalf("stub note missing title:\n%s", got)
	}
}

func TestServer_FixesAPI_CreateStubPreviewConflictReturns409(t *testing.T) {
	// The broken target [[Archive/Nowhere]] resolves to no walked note (Archive
	// is ignored), but its stub destination Archive/Nowhere.md already exists on
	// disk. Preview must surface a 409 conflict, not a 200 no-op.
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":          []byte("---\ntitle: Source\n---\n[[Archive/Nowhere]]\n"),
		"Archive/Nowhere.md": []byte("---\ntitle: Nowhere\n---\noccupied\n"),
	}, func(cfg *config.Config) {
		cfg.Ignore = []string{"Archive"}
	}, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "archive/nowhere")

	previewResp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusConflict {
		t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusConflict, previewBody)
	}
	if payload := decodeManagementError(t, previewBody); payload.OK || payload.Code != "conflict" {
		t.Fatalf("preview payload = %#v, want conflict", payload)
	}

	// The occupied destination must be left untouched.
	if got := readNoteFile(t, filepath.Join(root, "Archive", "Nowhere.md")); !strings.Contains(got, "occupied") {
		t.Fatalf("occupied destination clobbered:\n%s", got)
	}
}

func TestServer_FixesAPI_CreateStubApplyConflictReturns409(t *testing.T) {
	// Preview succeeds while the stub destination is free, then the destination
	// becomes occupied before apply. The apply must surface a 409 conflict rather
	// than a 200 "made no changes", and must not clobber the occupying file.
	// Archive is ignored so writing into it after preview does not change the
	// affected-note hash baked into the previewed task id (keeping the receipt
	// valid), and the link stays unresolved so the broken-target task persists.
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Archive/Nowhere]]\n"),
	}, func(cfg *config.Config) {
		cfg.Ignore = []string{"Archive"}
	}, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "archive/nowhere")

	previewResp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want 200; body=%q", previewResp.StatusCode, previewBody)
	}
	preview := decodeFixPreview(t, previewBody)
	if !preview.OK || strings.TrimSpace(preview.PreviewID) == "" {
		t.Fatalf("preview = %#v, want ok + preview id", preview)
	}

	// The destination is occupied between preview and apply.
	writeTestFile(t, root, "Archive/Nowhere.md", "---\ntitle: Nowhere\n---\noccupied\n")

	applyResp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), preview.PreviewID, "csrf-token", "")
	applyBody := readResponseBody(t, applyResp)
	if applyResp.StatusCode != http.StatusConflict {
		t.Fatalf("apply status: got %d, want %d; body=%q", applyResp.StatusCode, http.StatusConflict, applyBody)
	}
	if payload := decodeManagementError(t, applyBody); payload.OK || payload.Code != "conflict" {
		t.Fatalf("apply payload = %#v, want conflict", payload)
	}

	// The occupying file must be left untouched.
	if got := readNoteFile(t, filepath.Join(root, "Archive", "Nowhere.md")); !strings.Contains(got, "occupied") {
		t.Fatalf("occupied destination clobbered:\n%s", got)
	}
}

func TestServer_FixesAPI_SchemaRepairPreviewAndApply(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Note.md": []byte("---\nstatus: Active\ntitle: Note\n---\nBody.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"status": {Type: config.KindString, Allowed: []string{"active", "done"}},
		}
	}, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleSchemaViolation, `status invalid value "Active" (allowed: active, done)`)

	previewResp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionSchemaRepair), "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want 200; body=%q", previewResp.StatusCode, previewBody)
	}
	preview := decodeFixPreview(t, previewBody)
	if len(preview.Previews) != 1 || len(preview.Previews[0].Changes) != 1 {
		t.Fatalf("preview = %#v, want one frontmatter change", preview)
	}
	change := preview.Previews[0].Changes[0]
	if change.Key != "status" || change.Old.Text != "Active" || change.New.Text != "active" {
		t.Fatalf("preview change = %#v, want status Active -> active", change)
	}
	if got := readNoteFile(t, filepath.Join(root, "Note.md")); !strings.Contains(got, "status: Active") {
		t.Fatalf("preview mutated the note:\n%s", got)
	}

	applyResp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionSchemaRepair), preview.PreviewID, "csrf-token", "")
	applyBody := readResponseBody(t, applyResp)
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("apply status: got %d, want 200; body=%q", applyResp.StatusCode, applyBody)
	}
	apply := decodeFixApply(t, applyBody)
	if len(apply.Changed) != 1 || apply.Changed[0] != "Note.md" || apply.JournalID == "" {
		t.Fatalf("apply = %#v, want Note.md changed with journal", apply)
	}
	if got := readNoteFile(t, filepath.Join(root, "Note.md")); !strings.Contains(got, "status: active") {
		t.Fatalf("apply did not canonicalize status:\n%s", got)
	}
}

func TestServer_FixesAPI_ApplyRecordsOperationID(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Note.md": []byte("---\nstatus: Active\ntitle: Note\n---\nBody.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"status": {Type: config.KindString, Allowed: []string{"active", "done"}},
		}
	}, Options{CSRFToken: "csrf-token"})
	const id = "77777777777777777777777777777777"

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleSchemaViolation, `status invalid value "Active" (allowed: active, done)`)
	preview := decodeFixPreview(t, readResponseBody(t, postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionSchemaRepair), "csrf-token", "")))

	form := fixForm(task.ID, string(cockpit.ActionSchemaRepair), preview.PreviewID, "")
	form.Set("operation_id", id)
	applyResp := doFormPost(t, ts.URL+"/api/fixes/apply", form, "csrf-token")
	applyBody := readResponseBody(t, applyResp)
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("apply status: got %d, want 200; body=%q", applyResp.StatusCode, applyBody)
	}
	apply := decodeFixApply(t, applyBody)
	if got := checkOperationOutcome(t, ts.URL, id); got["status"] != "confirmed" || got["journal_id"] != apply.JournalID {
		t.Fatalf("fix outcome = %#v, want journal %q", got, apply.JournalID)
	}
}

func TestServer_FixesAPI_ApplyRequiresPreview(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")
	resp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "preview_required" {
		t.Fatalf("payload = %#v, want preview_required", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Nowhere.md")); !os.IsNotExist(err) {
		t.Fatalf("apply without preview created the stub: %v", err)
	}
}

func TestServer_FixesAPI_ApplyRejectsExpiredPreview(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Nowhere]]\n")

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	srv.fixPreviews.now = func() time.Time { return now }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")
	preview := decodeFixPreview(t, readResponseBody(t, postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "csrf-token", "")))
	if strings.TrimSpace(preview.PreviewID) == "" {
		t.Fatalf("preview id empty: %#v", preview)
	}

	now = now.Add(fixPreviewTTL + time.Second)
	resp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), preview.PreviewID, "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "preview_required" {
		t.Fatalf("payload = %#v, want preview_required", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Nowhere.md")); !os.IsNotExist(err) {
		t.Fatalf("expired apply created the stub: %v", err)
	}
}

func TestServer_FixesAPI_ApplyRejectsStalePreview(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")
	preview := decodeFixPreview(t, readResponseBody(t, postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "csrf-token", "")))

	// Mutate the affected note after previewing: its file hash changes, so the
	// previewed task id no longer exists in the freshly generated queue.
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Nowhere]]\n\nAppended.\n")

	resp := postFixApply(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), preview.PreviewID, "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "preview_stale" {
		t.Fatalf("payload = %#v, want preview_stale", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Nowhere.md")); !os.IsNotExist(err) {
		t.Fatalf("stale apply created the stub: %v", err)
	}
}

func TestServer_FixesAPI_PreviewCSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")
	resp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionCreateStub), "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Nowhere.md")); !os.IsNotExist(err) {
		t.Fatalf("csrf-rejected preview created the stub: %v", err)
	}
}

func TestServer_FixesAPI_PreviewRejectsUnknownTask(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postFixPreview(t, ts.URL, "task_deadbeef", string(cockpit.ActionCreateStub), "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "not_found" {
		t.Fatalf("payload = %#v, want not_found", payload)
	}
}

func TestServer_FixesAPI_PreviewRejectsTriageAction(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Nowhere]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "nowhere")
	resp := postFixPreview(t, ts.URL, task.ID, string(cockpit.ActionOpen), "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "invalid_request" {
		t.Fatalf("payload = %#v, want invalid_request", payload)
	}
}
