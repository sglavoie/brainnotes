package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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

func TestServer_HealthKeepsFilteredQueueWhenStateCleanupContends(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}

	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	raw, err := cockpit.BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	task := findCockpitTask(t, raw, cockpit.RuleBrokenTarget, "missing")
	statePath := seedCockpitDoneStateWithStaleRecord(t, root, task)

	runCockpitTestGit(t, root, "init", "-q")
	runCockpitTestGit(t, root, "add", "-f", "Source.md", ".bnotes/cockpit.json")
	runCockpitTestGit(t, root, "-c", "user.name=BrainNotes Test", "-c", "user.email=brainnotes@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "baseline")

	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	lockPath := filepath.Join(root, ".git", "brainnotes-safety", "safety.lock")
	release, acquired, err := vault.TryLockPath(lockPath)
	if err != nil {
		t.Fatalf("hold Git safety lock: %v", err)
	}
	if !acquired {
		t.Fatal("Git safety lock was unexpectedly busy")
	}
	defer release()

	resp, err := http.Get(ts.URL + "/health") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, cockpitStateWarning) || strings.Contains(body, cockpitStateCleanupWarning) {
		t.Fatalf("transient safety-lock contention surfaced a cockpit warning: %q", body)
	}
	if !strings.Contains(body, `<dd data-fixes-count-done-hidden>1</dd>`) {
		t.Fatalf("health page did not preserve filtered counts during cleanup contention: %q", body)
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after deferred cleanup: %v", err)
	}
	if !strings.Contains(string(data), `"task_stale"`) {
		t.Fatalf("cleanup was not deferred while the safety lock was held: %q", data)
	}
}

func TestServer_HealthUsesFilteredQueueWhenStateCleanupCannotPersist(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	raw, err := cockpit.BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	task := findCockpitTask(t, raw, cockpit.RuleBrokenTarget, "missing")
	statePath := seedCockpitDoneStateWithStaleRecord(t, root, task)
	targetPath := filepath.Join(filepath.Dir(statePath), "cockpit-target.json")
	if err := os.Rename(statePath, targetPath); err != nil {
		t.Fatalf("move cockpit state behind symlink: %v", err)
	}
	if err := os.Symlink(filepath.Base(targetPath), statePath); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}

	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/health") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, cockpitStateCleanupWarning) || strings.Contains(body, cockpitStateWarning) {
		t.Fatalf("health page did not distinguish cleanup failure from load failure: %q", body)
	}
	if !strings.Contains(body, `<dd data-fixes-count-done-hidden>1</dd>`) {
		t.Fatalf("health page did not preserve filtered counts after cleanup failure: %q", body)
	}
}

func seedCockpitDoneStateWithStaleRecord(t *testing.T, root string, task cockpit.Task) string {
	t.Helper()
	if err := cockpit.NewStateStore(root).MarkDone(task); err != nil {
		t.Fatalf("seed done state: %v", err)
	}

	statePath := filepath.Join(root, ".bnotes", "cockpit.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read seeded cockpit state: %v", err)
	}
	var state struct {
		Version int                               `json:"version"`
		Items   map[string]map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode seeded cockpit state: %v", err)
	}
	state.Items["task_stale"] = map[string]interface{}{
		"id":         "task_stale",
		"signature":  "stale",
		"state":      "done",
		"updated_at": time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	data, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("encode cockpit state fixture: %v", err)
	}
	if err := os.WriteFile(statePath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write cockpit state fixture: %v", err)
	}
	return statePath
}

func runCockpitTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	argv := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", argv...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
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
