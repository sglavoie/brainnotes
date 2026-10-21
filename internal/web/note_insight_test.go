package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

func TestServer_NoteInsightAPI_RunsConfiguredAgentWithPromptAndContext(t *testing.T) {
	var gotArgv []string
	var gotStdin []byte
	var gotDeadline bool
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":  []byte("---\ntitle: Source\n---\nTiny [[Related]] note.\n"),
		"Related.md": []byte("---\ntitle: Related\n---\nSupporting evidence for the source.\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"note-agent", "--ask"}
		cfg.Agent.TimeoutSeconds = 20
	}, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
			gotArgv = append([]string(nil), argv...)
			gotStdin = append([]byte(nil), stdin...)
			_, gotDeadline = ctx.Deadline()
			return []byte("**Answer**\n<script>alert(1)</script>"), nil
		},
	})

	resp := postNoteInsightRequest(t, ts.URL, "source", "  Give me three concrete next steps.  ", "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeNoteInsightResponse(t, body)
	if !payload.OK || !payload.Enabled || payload.Key != "source" {
		t.Fatalf("payload = %#v", payload)
	}
	if strings.Contains(payload.RenderedHTML, "<script>") || !strings.Contains(payload.RenderedHTML, "<strong>Answer</strong>") {
		t.Fatalf("rendered HTML was not sanitized Markdown: %q", payload.RenderedHTML)
	}
	input := string(gotStdin)
	for _, want := range []string{
		`- Source: "Source.md"`,
		"Tiny [[Related]] note.",
		"Supporting evidence for the source.",
		"User request (prioritize this when it conflicts with the default guidance):\nGive me three concrete next steps.",
	} {
		if !strings.Contains(input, want) {
			t.Fatalf("agent stdin missing %q: %q", want, input)
		}
	}
	if strings.Join(gotArgv, "\x00") != strings.Join([]string{"note-agent", "--ask"}, "\x00") {
		t.Fatalf("argv = %#v", gotArgv)
	}
	if !gotDeadline {
		t.Fatal("agent timeout did not set a request deadline")
	}
	sourceBody := readNoteFile(t, root+"/Source.md")
	if strings.Contains(sourceBody, payload.Output) || strings.Contains(sourceBody, "Give me three concrete next steps") {
		t.Fatalf("note insight request was persisted: %q", sourceBody)
	}
}

func TestServer_NoteInsightAPI_RejectsOversizedPromptWithoutRunningAgent(t *testing.T) {
	var ran bool
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"note-agent"}
	}, Options{
		CSRFToken: "csrf-token",
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	resp := postNoteInsightRequest(t, ts.URL, "source", strings.Repeat("x", agentPromptMaxRunes+1), "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
	}
	if ran {
		t.Fatal("agent ran for oversized prompt")
	}
}

func postNoteInsightRequest(t *testing.T, baseURL, key, prompt, csrfToken string) *http.Response {
	t.Helper()
	form := url.Values{}
	form.Set("key", key)
	form.Set("prompt", prompt)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/note/insight", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeaderName, csrfToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/note/insight: %v", err)
	}
	return resp
}

func decodeNoteInsightResponse(t *testing.T, body string) noteInsightResponse {
	t.Helper()
	var payload noteInsightResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode note insight response: %v; body=%q", err, body)
	}
	return payload
}
