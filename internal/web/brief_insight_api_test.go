package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestServer_BriefInsightAPI_DisabledWithoutTopLevelAgentCommand(t *testing.T) {
	now := time.Date(2026, 8, 4, 15, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	ran := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Cockpit.AgentCommand = "legacy-review-agent --daily"
		cfg.Agent.Command = nil
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefInsightRequest(t, ts.URL, string(cardID), "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBriefInsightResponse(t, body)
	if !payload.OK || payload.Enabled {
		t.Fatalf("payload = %#v, want ok + disabled", payload)
	}
	if payload.Message == "" {
		t.Fatalf("payload message = %q, want disabled explanation", payload.Message)
	}
	if ran {
		t.Fatal("insight agent command ran while top-level agent command was unset")
	}
}

func TestServer_BriefInsightAPI_RunsConfiguredAgentAndEscapesHTML(t *testing.T) {
	now := time.Date(2026, 8, 4, 16, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	var gotArgv []string
	var gotStdin []byte
	var gotDeadline bool
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody with [[Bridge]]\n"),
		"Bridge.md": []byte("---\ntitle: Bridge\n---\nBridge body\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"brief-agent", "--insight"}
		cfg.Agent.TimeoutSeconds = 20
		cfg.Cockpit.AgentCommand = "legacy-agent --daily"
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		RunAgent: func(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
			gotArgv = append([]string(nil), argv...)
			gotStdin = append([]byte(nil), stdin...)
			_, gotDeadline = ctx.Deadline()
			return []byte("**Summary**\n<script>alert(1)</script>"), nil
		},
	})
	if err := os.Chtimes(filepath.Join(root, "Source.md"), now, now); err != nil {
		t.Fatalf("chtimes source: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "Bridge.md"), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("chtimes bridge: %v", err)
	}

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefInsightRequestWithPrompt(t, ts.URL, string(cardID), "  Give me three concrete next steps.  ", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBriefInsightResponse(t, body)
	if !payload.OK || !payload.Enabled || payload.CardID != cardID || payload.Slot != brief.SlotContinue {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Output != "**Summary**\n<script>alert(1)</script>" {
		t.Fatalf("output = %q", payload.Output)
	}
	if strings.Contains(payload.RenderedHTML, "<script>") {
		t.Fatalf("rendered_html leaked raw script tag: %q", payload.RenderedHTML)
	}
	if !strings.Contains(payload.RenderedHTML, "raw HTML omitted") {
		t.Fatalf("rendered_html missing raw HTML suppression marker: %q", payload.RenderedHTML)
	}
	if !strings.Contains(payload.RenderedHTML, "<strong>Summary</strong>") {
		t.Fatalf("rendered_html missing markdown rendering: %q", payload.RenderedHTML)
	}
	if !strings.Contains(string(gotStdin), "Source.md") {
		t.Fatalf("agent stdin did not include context packet source path: %q", gotStdin)
	}
	if !strings.Contains(string(gotStdin), "User request (prioritize this when it conflicts with the default guidance):\nGive me three concrete next steps.") {
		t.Fatalf("agent stdin did not include trimmed user prompt: %q", gotStdin)
	}
	if !gotDeadline {
		t.Fatal("agent timeout did not set a request deadline")
	}
	if got, want := strings.Join(gotArgv, "\x00"), strings.Join([]string{"brief-agent", "--insight"}, "\x00"); got != want {
		t.Fatalf("argv = %#v, want [brief-agent --insight]", gotArgv)
	}

	stateBody := readNoteFile(t, filepath.Join(root, ".bnotes", "brief.json"))
	if strings.Contains(stateBody, payload.Output) {
		t.Fatalf("brief state persisted AI output: %q", stateBody)
	}
}

func TestServer_BriefInsightAPI_RejectsOversizedPrompt(t *testing.T) {
	now := time.Date(2026, 8, 4, 16, 15, 0, 0, time.FixedZone("mdt", -6*60*60))
	var ran bool
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"brief-agent"}
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	resp := postBriefInsightRequestWithPrompt(t, ts.URL, "card", strings.Repeat("x", agentPromptMaxRunes+1), "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
	}
	payload := decodeManagementError(t, body)
	if payload.Code != "invalid_request" || !strings.Contains(payload.Message, "2000 characters or fewer") {
		t.Fatalf("payload = %#v, want bounded prompt error", payload)
	}
	if ran {
		t.Fatal("agent ran for oversized prompt")
	}
}

func TestServer_BriefInsightAPI_ReturnsSanitizedAgentDiagnostic(t *testing.T) {
	now := time.Date(2026, 8, 4, 16, 30, 0, 0, time.FixedZone("mdt", -6*60*60))
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"brief-agent"}
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			return nil, errors.New("unsupported\n\x1b[31m model")
		},
	})

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefInsightRequest(t, ts.URL, string(cardID), "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusInternalServerError, body)
	}
	payload := decodeManagementError(t, body)
	if payload.Code != "internal_error" {
		t.Fatalf("payload = %#v, want internal_error", payload)
	}
	if got, want := payload.Message, "run brief insight command: unsupported [31m model"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestServer_BriefInsightAPI_ConnectPinsDisplayedCandidateFirst(t *testing.T) {
	now := time.Date(2026, 8, 4, 17, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	var gotStdin []byte
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":       []byte("---\ntitle: Source\n---\n# Source\nalpha beta [[Bridge]]\n"),
		"Bridge.md":       []byte("---\ntitle: Bridge\n---\n[[CandidateOne]] [[CandidateTwo]]\n"),
		"CandidateOne.md": []byte("---\ntitle: Candidate One\n---\nalpha beta gamma delta\n"),
		"CandidateTwo.md": []byte("---\ntitle: Candidate Two\n---\nalpha beta gamma\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"brief-agent"}
		cfg.Agent.TimeoutSeconds = 10
		cfg.Related.MetadataProperties = []string{"tags"}
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		RunAgent: func(_ context.Context, _ []string, stdin []byte) ([]byte, error) {
			gotStdin = append([]byte(nil), stdin...)
			return []byte("ok"), nil
		},
	})
	if err := os.Chtimes(filepath.Join(root, "Source.md"), now, now); err != nil {
		t.Fatalf("chtimes source: %v", err)
	}
	for _, rel := range []string{"Bridge.md", "CandidateOne.md", "CandidateTwo.md"} {
		if err := os.Chtimes(filepath.Join(root, rel), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}

	prime := postBriefItemRequest(t, ts.URL, "deadbeef", "open", "csrf-token", "")
	_ = readResponseBody(t, prime)
	if prime.StatusCode != http.StatusNotFound {
		t.Fatalf("prime status: got %d, want %d", prime.StatusCode, http.StatusNotFound)
	}

	selection := mustReadBriefDailySelection(t, root, now)
	initialConnect := mustSelectionCardForSlot(t, selection, brief.SlotConnect)
	if initialConnect.Candidate == nil {
		t.Fatalf("initial connect candidate missing: %#v", initialConnect)
	}
	initialCandidate := initialConnect.Candidate.RelPath

	replaceResp := postBriefItemRequest(t, ts.URL, string(initialConnect.ID), "another", "csrf-token", "")
	replaceBody := readResponseBody(t, replaceResp)
	if replaceResp.StatusCode != http.StatusOK {
		t.Fatalf("replace status: got %d, want %d; body=%q", replaceResp.StatusCode, http.StatusOK, replaceBody)
	}
	replacePayload := decodeBriefItemResponse(t, replaceBody)
	replacedConnect := mustDailyCardForSlot(t, replacePayload.Daily, brief.SlotConnect)
	if replacedConnect.Candidate == nil {
		t.Fatalf("replaced connect candidate missing: %#v", replacedConnect)
	}
	if replacedConnect.Candidate.RelPath == initialCandidate {
		t.Fatalf("connect replacement did not change candidate: %#v", replacePayload.Daily)
	}

	insightResp := postBriefInsightRequest(t, ts.URL, string(replacedConnect.ID), "csrf-token", "")
	insightBody := readResponseBody(t, insightResp)
	if insightResp.StatusCode != http.StatusOK {
		t.Fatalf("insight status: got %d, want %d; body=%q", insightResp.StatusCode, http.StatusOK, insightBody)
	}
	insightPayload := decodeBriefInsightResponse(t, insightBody)
	if !insightPayload.OK || !insightPayload.Enabled || insightPayload.Slot != brief.SlotConnect {
		t.Fatalf("insight payload = %#v", insightPayload)
	}

	packet := string(gotStdin)
	selectedPath := replacedConnect.Candidate.RelPath
	otherPath := "CandidateOne.md"
	if selectedPath == otherPath {
		otherPath = "CandidateTwo.md"
	}
	selectedIndex := strings.Index(packet, selectedPath)
	otherIndex := strings.Index(packet, otherPath)
	if selectedIndex < 0 || otherIndex < 0 {
		t.Fatalf("expected both connect candidates in packet; selected=%q other=%q packet=%q", selectedPath, otherPath, packet)
	}
	if selectedIndex > otherIndex {
		t.Fatalf("displayed connect candidate was not first in packet; selected=%q other=%q", selectedPath, otherPath)
	}
}

func TestServer_BriefInsightAPI_RejectsOversizedRequiredContext(t *testing.T) {
	now := time.Date(2026, 8, 4, 18, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	ran := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n" + strings.Repeat("x", 70*1024) + "\n"),
	}, func(cfg *config.Config) {
		cfg.Agent.Command = []string{"brief-agent"}
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return []byte("should not run"), nil
		},
	})

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefInsightRequest(t, ts.URL, string(cardID), "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict", payload)
	}
	if ran {
		t.Fatal("insight command ran even though context exceeded byte limit")
	}
}
