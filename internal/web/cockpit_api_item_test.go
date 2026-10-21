package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

func TestServer_CockpitAPI_ItemDoneSnoozeAndClear(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	initial := getCockpitPayload(t, ts.URL)
	item := findCockpitTask(t, initial.Queue, cockpit.RuleBrokenTarget, "missing")

	doneBody := readResponseBody(t, postCockpitItem(t, ts.URL, item.ID, "mark_done", "", "csrf-token", ""))
	done := decodeCockpitResult(t, doneBody)
	if _, ok := cockpitTaskByID(done.Queue, item.ID); ok {
		t.Fatalf("done item remained active: %#v", done)
	}
	if done.Counts.DoneHidden != 1 || done.Counts.SnoozedHidden != 0 || done.Counts.TotalGenerated != initial.Counts.TotalGenerated {
		t.Fatalf("done counts = %#v, initial=%#v", done.Counts, initial.Counts)
	}

	clearBody := readResponseBody(t, postCockpitItem(t, ts.URL, item.ID, "clear", "", "csrf-token", ""))
	cleared := decodeCockpitResult(t, clearBody)
	if _, ok := cockpitTaskByID(cleared.Queue, item.ID); !ok {
		t.Fatalf("cleared item did not return: %#v", cleared)
	}
	if cleared.Counts.DoneHidden != 0 || cleared.Counts.SnoozedHidden != 0 {
		t.Fatalf("cleared counts = %#v, want no hidden", cleared.Counts)
	}

	snoozeBody := readResponseBody(t, postCockpitItem(t, ts.URL, item.ID, "snooze", "week", "csrf-token", ""))
	snoozed := decodeCockpitResult(t, snoozeBody)
	if _, ok := cockpitTaskByID(snoozed.Queue, item.ID); ok {
		t.Fatalf("snoozed item remained active: %#v", snoozed)
	}
	if snoozed.Counts.SnoozedHidden != 1 || snoozed.Counts.DoneHidden != 0 {
		t.Fatalf("snoozed counts = %#v, want one snoozed-hidden", snoozed.Counts)
	}
}

func TestServer_CockpitAPI_ItemAcceptsDoneAlias(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	item := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "missing")
	resp := postCockpitItem(t, ts.URL, item.ID, "done", "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("done-alias status: got %d, want 200; body=%q", resp.StatusCode, body)
	}
	if decodeCockpitResult(t, body).Counts.DoneHidden != 1 {
		t.Fatalf("done-alias did not suppress task; body=%q", body)
	}
}

func TestServer_CockpitAPI_ItemExemptsDeadEndImmediately(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Terminal.md": []byte("---\ntitle: Terminal\nstatus: complete\n---\nIntentionally terminal.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	initial := getCockpitPayload(t, ts.URL)
	task := findCockpitTask(t, initial.Queue, cockpit.RuleDeadEndNote, "Terminal.md")
	if !task.TriageOnly {
		t.Fatalf("dead-end task still requires the user's intentional classification: %#v", task)
	}
	foundAction := false
	for _, action := range task.Actions {
		if action.Kind == cockpit.ActionExemptDeadEnd {
			foundAction = true
		}
	}
	if !foundAction {
		t.Fatalf("dead-end task actions = %#v, want %q", task.Actions, cockpit.ActionExemptDeadEnd)
	}

	resp := postCockpitItem(t, ts.URL, task.ID, string(cockpit.ActionExemptDeadEnd), "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exempt status: got %d, want 200; body=%q", resp.StatusCode, body)
	}
	result := decodeCockpitResult(t, body)
	if result.Message != "Marked Terminal as an intentional dead end." {
		t.Fatalf("message = %q", result.Message)
	}
	for _, item := range result.Tasks {
		if item.Kind == cockpit.RuleDeadEndNote && item.Detail == "Terminal.md" {
			t.Fatalf("exempt dead-end task remained in refreshed queue: %#v", item)
		}
	}

	data, err := os.ReadFile(filepath.Join(root, "Terminal.md"))
	if err != nil {
		t.Fatalf("read Terminal.md: %v", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		t.Fatalf("parse Terminal.md: %v", err)
	}
	value, ok := doc.Front.Get("dead_end")
	if !ok || value != false {
		t.Fatalf("dead_end = %#v, present=%v; want YAML boolean false", value, ok)
	}
	if status, ok := doc.Front.GetString("status"); !ok || status != "complete" {
		t.Fatalf("existing frontmatter was not preserved: status=%q, present=%v", status, ok)
	}
	if !strings.Contains(doc.Body, "Intentionally terminal.") {
		t.Fatalf("body was not preserved: %q", doc.Body)
	}

	journalRoot := filepath.Join(root, config.DefaultJournalDir)
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want 1", len(entries))
	}
	if _, err := os.Stat(filepath.Join(journalRoot, entries[0].Name(), "manifest.json")); err != nil {
		t.Fatalf("stat journal manifest: %v", err)
	}
}

func TestServer_CockpitAPI_ItemCleansLongTailPropertyAcrossAffectedNotes(t *testing.T) {
	files := map[string][]byte{}
	for _, name := range []string{"CommonA", "CommonB", "CommonC", "CommonD"} {
		files[name+".md"] = []byte("---\nstatus: common\ndead_end: false\n---\nCommon body.\n")
	}
	files["RareA.md"] = []byte("---\nstatus: odd\nkeep: alpha\ndead_end: false\n---\nRare A body.\n")
	files["RareB.md"] = []byte("---\nstatus: odd\nkeep: beta\ndead_end: false\n---\nRare B body.\n")
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Cockpit.LongTailMaxCount = 2
		cfg.Cockpit.LongTailHeadFactor = 2
	}, Options{CSRFToken: "csrf-token"})

	initial := getCockpitPayload(t, ts.URL)
	task := findCockpitTask(t, initial.Queue, cockpit.RuleLongTailValue, "status = odd")
	if len(task.Affected) != 2 {
		t.Fatalf("affected = %#v, want RareA and RareB", task.Affected)
	}
	foundAction := false
	for _, action := range task.Actions {
		if action.Kind == cockpit.ActionCleanLongTail {
			foundAction = true
		}
	}
	if !foundAction {
		t.Fatalf("long-tail task actions = %#v, want %q", task.Actions, cockpit.ActionCleanLongTail)
	}

	resp := postCockpitItem(t, ts.URL, task.ID, string(cockpit.ActionCleanLongTail), "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clean status: got %d, want 200; body=%q", resp.StatusCode, body)
	}
	result := decodeCockpitResult(t, body)
	if result.Message != "Removed property \"status\" from 2 affected notes." {
		t.Fatalf("message = %q", result.Message)
	}
	for _, item := range result.Tasks {
		if item.Kind == cockpit.RuleLongTailValue && item.Detail == "status = odd" {
			t.Fatalf("cleaned long-tail task remained in refreshed queue: %#v", item)
		}
	}

	for rel, want := range map[string]struct {
		keep string
		body string
	}{
		"RareA.md": {keep: "alpha", body: "Rare A body."},
		"RareB.md": {keep: "beta", body: "Rare B body."},
	} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		doc, err := frontmatter.Parse(data)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		if doc.Front.Has("status") {
			t.Fatalf("%s still has status: %q", rel, data)
		}
		if keep, ok := doc.Front.GetString("keep"); !ok || keep != want.keep {
			t.Fatalf("%s keep=%q, present=%v; want %q", rel, keep, ok, want.keep)
		}
		if !strings.Contains(doc.Body, want.body) {
			t.Fatalf("%s body was not preserved: %q", rel, doc.Body)
		}
	}
	commonData, err := os.ReadFile(filepath.Join(root, "CommonA.md"))
	if err != nil {
		t.Fatalf("read CommonA.md: %v", err)
	}
	commonDoc, err := frontmatter.Parse(commonData)
	if err != nil {
		t.Fatalf("parse CommonA.md: %v", err)
	}
	if status, ok := commonDoc.Front.GetString("status"); !ok || status != "common" {
		t.Fatalf("unaffected common note status=%q, present=%v", status, ok)
	}

	journalRoot := filepath.Join(root, config.DefaultJournalDir)
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one batch entry", len(entries))
	}
}

func TestServer_CockpitAPI_ItemRejectsRuleSpecificCleanupForOtherTask(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Target]]\n"),
		"Target.md": []byte("---\ntitle: Target\n---\nBody.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	orphan := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleOrphanNote, "Source.md")
	for _, action := range []cockpit.ActionKind{cockpit.ActionExemptDeadEnd, cockpit.ActionCleanLongTail} {
		resp := postCockpitItem(t, ts.URL, orphan.ID, string(action), "", "csrf-token", "")
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("action %q status: got %d, want %d; body=%q", action, resp.StatusCode, http.StatusBadRequest, body)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "Source.md"))
	if err != nil {
		t.Fatalf("read Source.md: %v", err)
	}
	if strings.Contains(string(data), "dead_end:") {
		t.Fatalf("rejected action mutated Source.md: %q", data)
	}
}

func TestServer_CockpitAPI_ItemCSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	item := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleBrokenTarget, "missing")
	resp := postCockpitItem(t, ts.URL, item.ID, "mark_done", "", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes", "cockpit.json")); !os.IsNotExist(err) {
		t.Fatalf("csrf-rejected item wrote state: %v", err)
	}
}

func TestServer_CockpitAPI_ItemRejectsUnknownID(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postCockpitItem(t, ts.URL, "task_deadbeef", "mark_done", "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "not_found" {
		t.Fatalf("payload = %#v, want not_found", payload)
	}
}

func TestServer_CockpitAPI_QueueMethodNotAllowed(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp, err := http.Post(ts.URL+"/api/cockpit", "application/x-www-form-urlencoded", strings.NewReader("")) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("POST /api/cockpit: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusMethodNotAllowed, body)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
}

// --- cockpit resilience with a quarantined note ----------------------------

// TestServer_CockpitAPI_QuarantinedNoteOmittedFromReads proves the read-only
// cockpit surfaces (the queue and the daily-review context) build from the
// resilient shared snapshot: a note whose read stalls is quarantined, omitted
// from every generated task, read at most once, and never stalls the response.
