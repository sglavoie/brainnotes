package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// --- cockpit queue + item --------------------------------------------------

func TestServer_CockpitAPI_GetReturnsQueueAndCounts(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	payload := getCockpitPayload(t, ts.URL)
	if !payload.OK {
		t.Fatalf("payload ok = false: %#v", payload)
	}
	if len(payload.Tasks) == 0 {
		t.Fatalf("expected generated tasks, got none")
	}
	if payload.Counts.Active != len(payload.Tasks) || payload.Counts.TotalGenerated < payload.Counts.Active {
		t.Fatalf("counts = %#v, tasks=%d", payload.Counts, len(payload.Tasks))
	}
	broken := findCockpitTask(t, payload.Queue, cockpit.RuleBrokenTarget, "missing")
	if broken.ID == "" || broken.Signature == "" {
		t.Fatalf("broken task missing identity: %#v", broken)
	}
	if len(payload.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", payload.Warnings)
	}
}

// TestServer_CockpitAPI_SensitiveContentRedacted proves the sensitive-content
// scanner's redaction survives serialization: with the scanner enabled the
// /api/cockpit payload advertises the finding but the raw secret-shaped value
// never appears anywhere in the JSON body the browser renders.
func TestServer_CockpitAPI_SensitiveContentRedacted(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE" // fake AWS key ID, not a real credential
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes.md": []byte("---\ntitle: Notes\n---\nkey " + secret + " here\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.SensitiveScan = true
	}, Options{CSRFToken: "csrf-token"})

	resp, err := http.Get(ts.URL + "/api/cockpit") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/cockpit: %v", err)
	}
	body := readResponseBody(t, resp)
	if strings.Contains(body, secret) {
		t.Fatalf("raw secret leaked into cockpit JSON payload: %q", body)
	}

	payload := decodeCockpitResult(t, body)
	task := findCockpitTask(t, payload.Queue, cockpit.RuleSensitiveContent, "Notes.md")
	if task.ID == "" || !task.TriageOnly {
		t.Fatalf("sensitive task missing identity or not triage-only: %#v", task)
	}
}

// TestServer_CockpitAPI_DailyViewCapsTasks proves ?view=daily caps the queue to
// cockpit.daily_limit while the default view returns the full queue. The daily
// slice is the top of the full queue and keeps the full counts so the panel can
// show totals next to the capped list.
func TestServer_CockpitAPI_DailyViewCapsTasks(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"A.md": []byte("---\ntitle: A\n---\n[[GhostA]]\n"),
		"B.md": []byte("---\ntitle: B\n---\n[[GhostB]]\n"),
		"C.md": []byte("---\ntitle: C\n---\n[[GhostC]]\n"),
		"D.md": []byte("---\ntitle: D\n---\n[[GhostD]]\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.DailyLimit = 2
	}, Options{CSRFToken: "csrf-token"})

	full := getCockpitPayload(t, ts.URL)
	if len(full.Tasks) <= 2 {
		t.Fatalf("expected full queue to exceed the daily limit, got %d tasks", len(full.Tasks))
	}

	daily := getCockpitDailyPayload(t, ts.URL)
	if len(daily.Tasks) != 2 {
		t.Fatalf("daily queue = %d tasks, want 2 (daily_limit)", len(daily.Tasks))
	}
	if daily.Counts.Active != full.Counts.Active {
		t.Errorf("daily Active = %d, want full %d (counts stay intact)", daily.Counts.Active, full.Counts.Active)
	}
	if daily.Counts.TotalGenerated != full.Counts.TotalGenerated {
		t.Errorf("daily TotalGenerated = %d, want %d", daily.Counts.TotalGenerated, full.Counts.TotalGenerated)
	}
	for i, task := range daily.Tasks {
		if task.ID != full.Tasks[i].ID {
			t.Errorf("daily task %d id = %q, want %q (top of full queue)", i, task.ID, full.Tasks[i].ID)
		}
	}
}

// TestServer_CockpitAPI_ItemDailyViewCapsTasks proves that a daily-aware item
// mutation (POST /api/cockpit/item?view=daily) keeps the response Tasks capped
// to cockpit.daily_limit while the counts continue to reflect the full queue,
// and that the default POST /api/cockpit/item still returns the full filtered
// queue. This is the regression guard for the Daily Fix Cockpit finding where a
// Done/Snooze/Clear action could replace the capped daily panel with the full
// queue until reload.
func TestServer_CockpitAPI_ItemDailyViewCapsTasks(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"A.md": []byte("---\ntitle: A\n---\n[[GhostA]]\n"),
		"B.md": []byte("---\ntitle: B\n---\n[[GhostB]]\n"),
		"C.md": []byte("---\ntitle: C\n---\n[[GhostC]]\n"),
		"D.md": []byte("---\ntitle: D\n---\n[[GhostD]]\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.DailyLimit = 2
	}, Options{CSRFToken: "csrf-token"})

	full := getCockpitPayload(t, ts.URL)
	if len(full.Tasks) <= 2 {
		t.Fatalf("expected full queue to exceed the daily limit, got %d tasks", len(full.Tasks))
	}
	item := full.Tasks[0]

	// Daily-aware mutation: response stays capped, counts reflect the full queue.
	dailyBody := readResponseBody(t, postCockpitItemDaily(t, ts.URL, item.ID, "mark_done", "", "csrf-token", ""))
	daily := decodeCockpitResult(t, dailyBody)
	if len(daily.Tasks) != 2 {
		t.Fatalf("daily item response = %d tasks, want 2 (daily_limit cap)", len(daily.Tasks))
	}
	if _, ok := cockpitTaskByID(daily.Queue, item.ID); ok {
		t.Fatalf("done item remained active in daily response: %#v", daily)
	}
	if daily.Counts.DoneHidden != 1 {
		t.Fatalf("daily counts DoneHidden = %d, want 1", daily.Counts.DoneHidden)
	}
	if daily.Counts.TotalGenerated != full.Counts.TotalGenerated {
		t.Fatalf("daily counts TotalGenerated = %d, want %d (full totals)", daily.Counts.TotalGenerated, full.Counts.TotalGenerated)
	}

	// Default mutation (no view): response returns the full filtered queue.
	clearBody := readResponseBody(t, postCockpitItem(t, ts.URL, item.ID, "clear", "", "csrf-token", ""))
	cleared := decodeCockpitResult(t, clearBody)
	if len(cleared.Tasks) != len(full.Tasks) {
		t.Fatalf("default item response = %d tasks, want %d (full filtered queue)", len(cleared.Tasks), len(full.Tasks))
	}
}

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
