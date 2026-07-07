package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// --- helpers ---------------------------------------------------------------

func getCockpitPayload(t *testing.T, baseURL string) cockpitAPIResponse {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/cockpit") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/cockpit: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cockpit status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	return decodeCockpitResult(t, body)
}

func getCockpitDailyPayload(t *testing.T, baseURL string) cockpitAPIResponse {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/cockpit?view=daily") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/cockpit?view=daily: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cockpit daily status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	return decodeCockpitResult(t, body)
}

func decodeCockpitResult(t *testing.T, body string) cockpitAPIResponse {
	t.Helper()
	var payload cockpitAPIResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode cockpit result: %v; body=%q", err, body)
	}
	return payload
}

func decodeFixPreview(t *testing.T, body string) fixPreviewResponse {
	t.Helper()
	var payload fixPreviewResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode fix preview: %v; body=%q", err, body)
	}
	return payload
}

func decodeFixApply(t *testing.T, body string) fixApplyResponse {
	t.Helper()
	var payload fixApplyResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode fix apply: %v; body=%q", err, body)
	}
	return payload
}

func findCockpitTask(t *testing.T, queue cockpit.Queue, kind cockpit.RuleKind, detail string) cockpit.Task {
	t.Helper()
	for _, task := range queue.Tasks {
		if task.Kind == kind && task.Detail == detail {
			return task
		}
	}
	t.Fatalf("no cockpit task kind=%q detail=%q; tasks=%#v", kind, detail, queue.Tasks)
	return cockpit.Task{}
}

func postCockpitItem(t *testing.T, baseURL, id, action, snooze, headerToken, formToken string) *http.Response {
	t.Helper()
	form := url.Values{}
	if id != "" {
		form.Set("id", id)
	}
	if action != "" {
		form.Set("action", action)
	}
	if snooze != "" {
		form.Set("snooze", snooze)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return doFormPost(t, baseURL+"/api/cockpit/item", form, headerToken)
}

func postCockpitItemDaily(t *testing.T, baseURL, id, action, snooze, headerToken, formToken string) *http.Response {
	t.Helper()
	form := url.Values{}
	if id != "" {
		form.Set("id", id)
	}
	if action != "" {
		form.Set("action", action)
	}
	if snooze != "" {
		form.Set("snooze", snooze)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return doFormPost(t, baseURL+"/api/cockpit/item?view=daily", form, headerToken)
}

func postFixPreview(t *testing.T, baseURL, id, action, headerToken, formToken string) *http.Response {
	t.Helper()
	return doFormPost(t, baseURL+"/api/fixes/preview", fixForm(id, action, "", formToken), headerToken)
}

func postFixApply(t *testing.T, baseURL, id, action, previewID, headerToken, formToken string) *http.Response {
	t.Helper()
	return doFormPost(t, baseURL+"/api/fixes/apply", fixForm(id, action, previewID, formToken), headerToken)
}

func fixForm(id, action, previewID, formToken string) url.Values {
	form := url.Values{}
	if id != "" {
		form.Set("id", id)
	}
	if action != "" {
		form.Set("action", action)
	}
	if previewID != "" {
		form.Set("preview_id", previewID)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return form
}

func doFormPost(t *testing.T, rawURL string, form url.Values, headerToken string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	return resp
}

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

// TestServer_DailyReviewContext_ReturnsRedactedContext proves the read-only
// daily-review context endpoint returns the deterministic, redacted snapshot of
// the daily queue, caps to cockpit.daily_limit while keeping the full counts,
// and never leaks a raw sensitive value.
func TestServer_DailyReviewContext_ReturnsRedactedContext(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE" // fake AWS key ID, not a real credential
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes.md": []byte("---\ntitle: Notes\n---\nkey " + secret + " here\n"),
		"A.md":     []byte("---\ntitle: A\n---\n[[GhostA]]\n"),
		"B.md":     []byte("---\ntitle: B\n---\n[[GhostB]]\n"),
		"C.md":     []byte("---\ntitle: C\n---\n[[GhostC]]\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.SensitiveScan = true
		cfg.Cockpit.DailyLimit = 2
	}, Options{CSRFToken: "csrf-token"})

	resp, err := http.Get(ts.URL + "/api/daily-review/context") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/daily-review/context: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	if strings.Contains(body, secret) {
		t.Fatalf("raw secret leaked into review-context payload: %q", body)
	}

	var payload dailyReviewContextResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode review context: %v; body=%q", err, body)
	}
	if !payload.OK {
		t.Fatalf("payload ok = false: %#v", payload)
	}
	if payload.TaskCount != 2 {
		t.Errorf("TaskCount = %d, want 2 (daily_limit)", payload.TaskCount)
	}
	if len(payload.Items) != 2 {
		t.Errorf("Items = %d, want 2", len(payload.Items))
	}
	if payload.Counts.Active <= 2 {
		t.Errorf("Counts.Active = %d, want full active total > 2", payload.Counts.Active)
	}
	// The sensitive finding sorts first (priority 15), so it survives the cap and
	// carries the server-redacted location string, never the raw secret.
	first := payload.Items[0]
	if first.Kind != cockpit.RuleSensitiveContent {
		t.Fatalf("first item kind = %q, want %q", first.Kind, cockpit.RuleSensitiveContent)
	}
	if len(first.Affected) == 0 || !strings.Contains(first.Affected[0].Location, "AWS access key ID") {
		t.Errorf("expected redacted detector location, got %#v", first.Affected)
	}
}

func TestServer_DailyReviewContext_MethodNotAllowed(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp, err := http.Post(ts.URL+"/api/daily-review/context", "application/x-www-form-urlencoded", strings.NewReader("")) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("POST /api/daily-review/context: %v", err)
	}
	_ = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Errorf("Allow = %q, want %q", allow, http.MethodGet)
	}
}

// --- daily-review agent ----------------------------------------------------

func agentForm(formToken string) url.Values {
	form := url.Values{}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return form
}

func decodeAgentResult(t *testing.T, body string) dailyReviewAgentResponse {
	t.Helper()
	var payload dailyReviewAgentResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode agent result: %v; body=%q", err, body)
	}
	return payload
}

// TestServer_DailyReviewAgent_RunsConfiguredCommand proves the optional agent
// route invokes the configured command as an argv slice (no shell parsing),
// feeds it the redacted daily-review context JSON on stdin, and returns its
// stdout. The raw sensitive value never reaches the command's stdin.
func TestServer_DailyReviewAgent_RunsConfiguredCommand(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE" // fake AWS key ID, not a real credential
	var gotArgv []string
	var gotStdin []byte
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes.md": []byte("---\ntitle: Notes\n---\nkey " + secret + " here\n"),
		"A.md":     []byte("---\ntitle: A\n---\n[[GhostA]]\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.SensitiveScan = true
		cfg.Cockpit.AgentCommand = "review-agent --daily"
	}, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(_ context.Context, argv []string, stdin []byte) ([]byte, error) {
			gotArgv = argv
			gotStdin = append([]byte(nil), stdin...)
			return []byte("# Review\nAll good."), nil
		},
	})

	resp := doFormPost(t, ts.URL+"/api/daily-review/agent", agentForm("csrf-token"), "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%q", resp.StatusCode, body)
	}
	payload := decodeAgentResult(t, body)
	if !payload.OK || !payload.Enabled {
		t.Fatalf("payload = %#v, want ok + enabled", payload)
	}
	if payload.Output != "# Review\nAll good." {
		t.Fatalf("output = %q, want the command stdout", payload.Output)
	}

	if len(gotArgv) != 2 || gotArgv[0] != "review-agent" || gotArgv[1] != "--daily" {
		t.Fatalf("argv = %#v, want [review-agent --daily] (split, no shell)", gotArgv)
	}
	if strings.Contains(string(gotStdin), secret) {
		t.Fatalf("raw secret leaked into agent stdin: %q", gotStdin)
	}
	var ctx cockpit.ReviewContext
	if err := json.Unmarshal(gotStdin, &ctx); err != nil {
		t.Fatalf("agent stdin is not a review context: %v; stdin=%q", err, gotStdin)
	}
	if ctx.TaskCount == 0 {
		t.Fatalf("agent stdin review context is empty: %#v", ctx)
	}
}

// TestServer_DailyReviewAgent_DisabledWhenUnconfigured proves the route returns a
// clean disabled response (not an error, no command run) when no agent command
// is configured.
func TestServer_DailyReviewAgent_DisabledWhenUnconfigured(t *testing.T) {
	ran := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	resp := doFormPost(t, ts.URL+"/api/daily-review/agent", agentForm("csrf-token"), "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%q", resp.StatusCode, body)
	}
	payload := decodeAgentResult(t, body)
	if !payload.OK || payload.Enabled {
		t.Fatalf("payload = %#v, want ok + disabled", payload)
	}
	if payload.Output != "" {
		t.Fatalf("output = %q, want empty", payload.Output)
	}
	if ran {
		t.Fatalf("agent command ran while unconfigured")
	}
}

func TestServer_DailyReviewAgent_CSRFRequired(t *testing.T) {
	ran := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.AgentCommand = "review-agent"
	}, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	resp := doFormPost(t, ts.URL+"/api/daily-review/agent", agentForm(""), "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if ran {
		t.Fatalf("agent command ran without csrf token")
	}
}

func TestServer_DailyReviewAgent_MethodNotAllowed(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp, err := http.Get(ts.URL + "/api/daily-review/agent") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/daily-review/agent: %v", err)
	}
	_ = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want %q", allow, http.MethodPost)
	}
}

// TestServer_DailyReviewAgent_CommandFailureReturns500 proves a failing agent
// command surfaces a server error rather than a 200 with empty output.
func TestServer_DailyReviewAgent_CommandFailureReturns500(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.AgentCommand = "review-agent"
	}, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			return nil, errors.New("boom")
		},
	})

	resp := doFormPost(t, ts.URL+"/api/daily-review/agent", agentForm("csrf-token"), "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusInternalServerError, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "internal_error" {
		t.Fatalf("payload = %#v, want internal_error", payload)
	}
}

// TestRunAgentCommand_StdinToStdout exercises the default runner: it invokes the
// argv without a shell, pipes stdin through, and returns stdout. `cat` echoes its
// stdin, so the returned bytes must equal the payload.
func TestRunAgentCommand_StdinToStdout(t *testing.T) {
	payload := []byte(`{"task_count":1}`)
	out, err := runAgentCommand(context.Background(), []string{"cat"}, payload)
	if err != nil {
		t.Fatalf("runAgentCommand: %v", err)
	}
	if string(out) != string(payload) {
		t.Fatalf("stdout = %q, want %q", out, payload)
	}
}

// TestRunAgentCommand_Timeout proves the runner honors context cancellation: a
// command that outlives the deadline is killed and returns an error, with the
// context reporting DeadlineExceeded so the handler can classify it as a timeout.
func TestRunAgentCommand_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runAgentCommand(ctx, []string{"sleep", "5"}, nil)
	if err == nil {
		t.Fatalf("runAgentCommand: want error from killed command")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("command was not killed promptly: took %s", elapsed)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
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
		cfg.Frontmatter.Schema = map[string]config.PropertySchema{
			"status": {Type: "string", Allowed: []string{"active", "done"}},
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
