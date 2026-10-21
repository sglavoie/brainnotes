package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/config"
)

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
		cfg.Agent.Command = []string{"shared-agent", "--ignored"}
		cfg.Agent.TimeoutSeconds = 30
		cfg.Cockpit.SensitiveScan = true
		cfg.Cockpit.AgentCommand = "review-agent --daily"
		cfg.Cockpit.AgentTimeoutSeconds = 10
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

func TestServer_DailyReviewAgent_FallsBackToSharedConfig(t *testing.T) {
	var gotArgv []string
	var gotDeadline bool
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"shared-agent", "--daily", "literal argument"}
		cfg.Agent.TimeoutSeconds = 15
	}, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(ctx context.Context, argv []string, _ []byte) ([]byte, error) {
			gotArgv = append([]string(nil), argv...)
			_, gotDeadline = ctx.Deadline()
			return []byte("shared output"), nil
		},
	})

	resp := doFormPost(t, ts.URL+"/api/daily-review/agent", agentForm("csrf-token"), "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%q", resp.StatusCode, body)
	}
	payload := decodeAgentResult(t, body)
	if !payload.OK || !payload.Enabled || payload.Output != "shared output" {
		t.Fatalf("payload = %#v, want shared agent output", payload)
	}
	wantArgv := []string{"shared-agent", "--daily", "literal argument"}
	if strings.Join(gotArgv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("argv = %#v, want %#v", gotArgv, wantArgv)
	}
	if !gotDeadline {
		t.Fatal("shared agent timeout did not produce a context deadline")
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
