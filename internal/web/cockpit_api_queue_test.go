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

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
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
