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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestServer_BriefViewAPI_IdempotentVisitByDate(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
	})

	resp := postBriefViewRequest(t, ts.URL, "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	first := decodeBriefViewResponse(t, body)
	if !first.OK {
		t.Fatalf("first payload not ok: %#v", first)
	}
	if first.Activity.Last7Days.VisitDays != 1 || first.Activity.Last7Days.OpenCount != 0 {
		t.Fatalf("first 7-day activity = %#v, want visit_days=1 and open_count=0", first.Activity.Last7Days)
	}

	resp = postBriefViewRequest(t, ts.URL, "csrf-token", "")
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	second := decodeBriefViewResponse(t, body)
	if second.Activity.Last7Days.VisitDays != 1 {
		t.Fatalf("second visit days = %d, want 1", second.Activity.Last7Days.VisitDays)
	}
}

func TestServer_BriefItemAPI_OpenRecordsAfterEditorStart(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	startCalls := 0
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		StartEditor: func(bin string, argv []string) error {
			startCalls++
			return nil
		},
	})

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefItemRequest(t, ts.URL, string(cardID), "open", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBriefItemResponse(t, body)
	if !payload.OK || payload.Action != "open" || payload.CardID != cardID || payload.Slot != brief.SlotContinue {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Activity.Last7Days.OpenCount != 1 {
		t.Fatalf("open count = %d, want 1", payload.Activity.Last7Days.OpenCount)
	}
	if startCalls != 1 {
		t.Fatalf("start editor calls = %d, want 1", startCalls)
	}
}

func TestServer_BriefItemAPI_OpenFailureDoesNotRecordOpen(t *testing.T) {
	now := time.Date(2026, 8, 4, 11, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	startCalls := 0
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		StartEditor: func(bin string, argv []string) error {
			startCalls++
			return errors.New("start failed")
		},
	})

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefItemRequest(t, ts.URL, string(cardID), "open", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusInternalServerError, body)
	}
	if startCalls != 1 {
		t.Fatalf("start editor calls = %d, want 1", startCalls)
	}

	activityResp := postBriefViewRequest(t, ts.URL, "csrf-token", "")
	activityBody := readResponseBody(t, activityResp)
	if activityResp.StatusCode != http.StatusOK {
		t.Fatalf("view status: got %d, want %d; body=%q", activityResp.StatusCode, http.StatusOK, activityBody)
	}
	activity := decodeBriefViewResponse(t, activityBody)
	if activity.Activity.Last7Days.OpenCount != 0 {
		t.Fatalf("open count = %d, want 0", activity.Activity.Last7Days.OpenCount)
	}
}

func TestServer_BriefItemAPI_AnotherFromContinueReturnsContinueAndConnect(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Recent.md": []byte("---\ntitle: Recent\n---\nRecent note\n"),
		"Older.md":  []byte("---\ntitle: Older\n---\nOlder note\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
	})
	if err := os.Chtimes(filepath.Join(root, "Older.md"), now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("chtimes older: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "Recent.md"), now, now); err != nil {
		t.Fatalf("chtimes recent: %v", err)
	}

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Recent.md", "")
	resp := postBriefItemRequest(t, ts.URL, string(cardID), "another", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBriefItemResponse(t, body)
	if !payload.OK || payload.Action != "another" || payload.Slot != brief.SlotContinue {
		t.Fatalf("payload = %#v", payload)
	}

	slots := map[brief.SlotKind]bool{}
	for _, affected := range payload.Affected {
		slots[affected.Slot] = true
	}
	if !slots[brief.SlotContinue] || !slots[brief.SlotConnect] {
		t.Fatalf("affected slots = %#v, want continue and connect", payload.Affected)
	}
}

func TestServer_BriefItemAPI_RejectsUnknownCardID(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	called := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		StartEditor: func(bin string, argv []string) error {
			called = true
			return nil
		},
	})

	resp := postBriefItemRequest(t, ts.URL, "deadbeef", "open", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
	payload := decodeManagementError(t, body)
	if payload.Code != "not_found" {
		t.Fatalf("payload = %#v, want not_found", payload)
	}
	if called {
		t.Fatal("start editor should not be called for unknown card id")
	}
}

func TestServer_BriefItemAPI_CSRFRequired(t *testing.T) {
	now := time.Date(2026, 8, 4, 14, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
	})
	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefItemRequest(t, ts.URL, string(cardID), "open", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
}

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
	resp := postBriefInsightRequest(t, ts.URL, string(cardID), "csrf-token", "")
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

func TestServer_BriefItemAPI_OpenWithoutEditorConfigured(t *testing.T) {
	t.Setenv("EDITOR", "")
	now := time.Date(2026, 8, 4, 18, 30, 0, 0, time.FixedZone("mdt", -6*60*60))
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
	})

	cardID := mustBriefCardID(t, now, brief.SlotContinue, "Source.md", "")
	resp := postBriefItemRequest(t, ts.URL, string(cardID), "open", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusPreconditionFailed, body)
	}
	payload := decodeManagementError(t, body)
	if payload.Code != "invalid_request" || !strings.Contains(payload.Message, "editor not configured") {
		t.Fatalf("payload = %#v, want invalid_request editor-not-configured", payload)
	}
}

func TestServer_BriefSelectionHonorsExcludedFoldersAndTypes(t *testing.T) {
	now := time.Date(2026, 8, 4, 19, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Keep.md":              []byte("---\ntitle: Keep\nstatus: active\n---\nUseful note body\n"),
		"Archive/Skip.md":      []byte("---\ntitle: Skip Folder\nstatus: active\n---\nShould not be selected\n"),
		"JournalEntry.md":      []byte("---\ntitle: Journal\nstatus: active\ntype: journal\n---\nShould not be selected\n"),
		"References/Bridge.md": []byte("---\ntitle: Bridge\nstatus: active\n---\nContext note\n"),
	}, func(cfg *config.Config) {
		cfg.Brief.ExcludeFolders = []string{"Archive"}
		cfg.Brief.ExcludeTypes = []string{"journal"}
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
	})
	if err := os.Chtimes(filepath.Join(root, "Archive", "Skip.md"), now, now); err != nil {
		t.Fatalf("chtimes archive skip: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "JournalEntry.md"), now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("chtimes journal: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "Keep.md"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("chtimes keep: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "References", "Bridge.md"), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("chtimes bridge: %v", err)
	}

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}

	selection := mustReadBriefDailySelection(t, root, now)
	continueCard := mustSelectionCardForSlot(t, selection, brief.SlotContinue)
	if continueCard.Source.RelPath != "Keep.md" {
		t.Fatalf("continue source = %q, want Keep.md after exclusions", continueCard.Source.RelPath)
	}
	for _, card := range selection.Cards {
		if strings.HasPrefix(card.Source.RelPath, "Archive/") || card.Source.RelPath == "JournalEntry.md" {
			t.Fatalf("excluded source path appeared in selection: %#v", card)
		}
		if card.Candidate != nil && (strings.HasPrefix(card.Candidate.RelPath, "Archive/") || card.Candidate.RelPath == "JournalEntry.md") {
			t.Fatalf("excluded candidate path appeared in selection: %#v", card)
		}
	}
}

func TestServer_BriefEndToEnd_PersistsAcrossRestartWithoutJournalWrites(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.FixedZone("mdt", -6*60*60))
	editorCalls := make([][]string, 0, 2)
	agentCalls := 0
	var agentInput []byte
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":       []byte("---\ntitle: Source\n---\n# Source\nalpha beta [[Bridge]]\n"),
		"Bridge.md":       []byte("---\ntitle: Bridge\n---\n[[CandidateOne]] [[CandidateTwo]]\n"),
		"CandidateOne.md": []byte("---\ntitle: Candidate One\n---\nalpha beta gamma delta\n"),
		"CandidateTwo.md": []byte("---\ntitle: Candidate Two\n---\nalpha beta gamma\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
		cfg.Agent.Command = []string{"brief-agent", "--insight"}
		cfg.Agent.TimeoutSeconds = 15
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		StartEditor: func(bin string, argv []string) error {
			editorCalls = append(editorCalls, append([]string(nil), argv...))
			return nil
		},
		RunAgent: func(_ context.Context, _ []string, stdin []byte) ([]byte, error) {
			agentCalls++
			agentInput = append([]byte(nil), stdin...)
			return []byte("insight-summary"), nil
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
	originalSourceBody := readNoteFile(t, filepath.Join(root, "Source.md"))

	assertGetContains := func(baseURL, path, needle string) {
		t.Helper()
		resp, err := http.Get(baseURL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status: got %d, want %d; body=%q", path, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, needle) {
			t.Fatalf("GET %s body missing %q: %q", path, needle, body)
		}
	}

	assertGetContains(ts.URL, "/", `data-brief-root`)
	assertGetContains(ts.URL, "/library", `data-overview-search-results`)
	assertGetContains(ts.URL, "/health", `data-fixes-panel`)

	viewResp := postBriefViewRequest(t, ts.URL, "csrf-token", "")
	viewBody := readResponseBody(t, viewResp)
	if viewResp.StatusCode != http.StatusOK {
		t.Fatalf("view status: got %d, want %d; body=%q", viewResp.StatusCode, http.StatusOK, viewBody)
	}
	viewPayload := decodeBriefViewResponse(t, viewBody)
	if viewPayload.Activity.Last7Days.VisitDays != 1 {
		t.Fatalf("visit days = %d, want 1", viewPayload.Activity.Last7Days.VisitDays)
	}

	selection := mustReadBriefDailySelection(t, root, now)
	continueCard := mustSelectionCardForSlot(t, selection, brief.SlotContinue)
	connectCard := mustSelectionCardForSlot(t, selection, brief.SlotConnect)
	if connectCard.Candidate == nil {
		t.Fatalf("connect card candidate missing: %#v", connectCard)
	}

	openResp := postBriefItemRequest(t, ts.URL, string(connectCard.ID), "open", "csrf-token", "")
	openBody := readResponseBody(t, openResp)
	if openResp.StatusCode != http.StatusOK {
		t.Fatalf("open status: got %d, want %d; body=%q", openResp.StatusCode, http.StatusOK, openBody)
	}
	if len(editorCalls) != 1 {
		t.Fatalf("editor call count = %d, want 1", len(editorCalls))
	}
	lastArgv := editorCalls[len(editorCalls)-1]
	if len(lastArgv) == 0 {
		t.Fatalf("editor argv missing: %#v", editorCalls)
	}
	gotTarget := lastArgv[len(lastArgv)-1]
	wantTarget := filepath.Join(root, filepath.FromSlash(connectCard.Candidate.RelPath))
	gotResolved, err := filepath.EvalSymlinks(gotTarget)
	if err != nil {
		t.Fatalf("resolve opened target %q: %v", gotTarget, err)
	}
	wantResolved, err := filepath.EvalSymlinks(wantTarget)
	if err != nil {
		t.Fatalf("resolve expected target %q: %v", wantTarget, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("connect open target = %q (%q), want candidate path %q (%q)", gotTarget, gotResolved, wantTarget, wantResolved)
	}

	insightResp := postBriefInsightRequest(t, ts.URL, string(connectCard.ID), "csrf-token", "")
	insightBody := readResponseBody(t, insightResp)
	if insightResp.StatusCode != http.StatusOK {
		t.Fatalf("insight status: got %d, want %d; body=%q", insightResp.StatusCode, http.StatusOK, insightBody)
	}
	insightPayload := decodeBriefInsightResponse(t, insightBody)
	if !insightPayload.OK || !insightPayload.Enabled || insightPayload.Output != "insight-summary" {
		t.Fatalf("insight payload = %#v", insightPayload)
	}
	if agentCalls != 1 {
		t.Fatalf("agent calls = %d, want 1", agentCalls)
	}
	if !strings.Contains(string(agentInput), connectCard.Candidate.RelPath) {
		t.Fatalf("insight input missing connect candidate path %q: %q", connectCard.Candidate.RelPath, agentInput)
	}

	anotherResp := postBriefItemRequest(t, ts.URL, string(continueCard.ID), "another", "csrf-token", "")
	anotherBody := readResponseBody(t, anotherResp)
	if anotherResp.StatusCode != http.StatusOK {
		t.Fatalf("another status: got %d, want %d; body=%q", anotherResp.StatusCode, http.StatusOK, anotherBody)
	}
	anotherPayload := decodeBriefItemResponse(t, anotherBody)
	slots := map[brief.SlotKind]bool{}
	for _, affected := range anotherPayload.Affected {
		slots[affected.Slot] = true
	}
	if !slots[brief.SlotContinue] || !slots[brief.SlotConnect] {
		t.Fatalf("another affected slots = %#v, want continue and connect", anotherPayload.Affected)
	}

	historyResp, err := http.Get(ts.URL + "/api/history") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/history: %v", err)
	}
	historyBody := readResponseBody(t, historyResp)
	if historyResp.StatusCode != http.StatusOK {
		t.Fatalf("history status: got %d, want %d; body=%q", historyResp.StatusCode, http.StatusOK, historyBody)
	}
	historyPayload := decodeHistoryResult(t, historyBody)
	if !historyPayload.OK || len(historyPayload.Entries) != 0 {
		t.Fatalf("history payload = %#v, want no journal entries", historyPayload)
	}

	stateBody := readNoteFile(t, filepath.Join(root, ".bnotes", "brief.json"))
	if strings.Contains(stateBody, "insight-summary") {
		t.Fatalf("brief sidecar persisted AI output: %q", stateBody)
	}
	if gotSourceBody := readNoteFile(t, filepath.Join(root, "Source.md")); gotSourceBody != originalSourceBody {
		t.Fatalf("brief APIs unexpectedly changed note body:\n--- before ---\n%s\n--- after ---\n%s", originalSourceBody, gotSourceBody)
	}
	beforeRestart := mustReadBriefDailySelection(t, root, now)

	ts.Close()

	ts2 := newHTTPServerForExistingRootWithOptions(t, root, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
		cfg.Agent.Command = []string{"brief-agent", "--insight"}
		cfg.Agent.TimeoutSeconds = 15
	}, Options{
		CSRFToken: "csrf-token",
		BriefNow:  func() time.Time { return now },
		StartEditor: func(bin string, argv []string) error {
			return nil
		},
		RunAgent: func(_ context.Context, _ []string, _ []byte) ([]byte, error) {
			return []byte("insight-summary"), nil
		},
	})

	assertGetContains(ts2.URL, "/", `data-brief-root`)
	assertGetContains(ts2.URL, "/library", `data-overview-search-results`)
	assertGetContains(ts2.URL, "/health", `data-fixes-panel`)

	viewResp2 := postBriefViewRequest(t, ts2.URL, "csrf-token", "")
	viewBody2 := readResponseBody(t, viewResp2)
	if viewResp2.StatusCode != http.StatusOK {
		t.Fatalf("restart view status: got %d, want %d; body=%q", viewResp2.StatusCode, http.StatusOK, viewBody2)
	}
	viewPayload2 := decodeBriefViewResponse(t, viewBody2)
	if viewPayload2.Activity.Last7Days.VisitDays != 1 || viewPayload2.Activity.Last7Days.OpenCount != 1 {
		t.Fatalf("restart activity = %#v, want visit_days=1 and open_count=1", viewPayload2.Activity.Last7Days)
	}
	afterRestart := mustReadBriefDailySelection(t, root, now)
	if !reflect.DeepEqual(beforeRestart, afterRestart) {
		t.Fatalf("daily selection changed across restart:\nbefore=%#v\nafter=%#v", beforeRestart, afterRestart)
	}
}

func newHTTPServerForExistingRootWithOptions(
	t *testing.T,
	root string,
	configure func(cfg *config.Config),
	opts Options,
) *httptest.Server {
	t.Helper()
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	if configure != nil {
		configure(cfg)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postBriefViewRequest(t *testing.T, baseURL, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/brief/view", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/brief/view: %v", err)
	}
	return resp
}

func postBriefItemRequest(t *testing.T, baseURL, cardID, action, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if cardID != "" {
		form.Set("card_id", cardID)
	}
	if action != "" {
		form.Set("action", action)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/brief/item", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/brief/item: %v", err)
	}
	return resp
}

func postBriefInsightRequest(t *testing.T, baseURL, cardID, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if cardID != "" {
		form.Set("card_id", cardID)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/brief/insight", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/brief/insight: %v", err)
	}
	return resp
}

func decodeBriefViewResponse(t *testing.T, body string) briefViewResponse {
	t.Helper()
	var payload briefViewResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode brief view response: %v; body=%q", err, body)
	}
	return payload
}

func decodeBriefItemResponse(t *testing.T, body string) briefItemResponse {
	t.Helper()
	var payload briefItemResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode brief item response: %v; body=%q", err, body)
	}
	return payload
}

func decodeBriefInsightResponse(t *testing.T, body string) briefInsightResponse {
	t.Helper()
	var payload briefInsightResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode brief insight response: %v; body=%q", err, body)
	}
	return payload
}

func mustBriefCardID(t *testing.T, now time.Time, slot brief.SlotKind, sourcePath, candidatePath string) brief.CardID {
	t.Helper()
	cardID, err := brief.CardIDFor(brief.LocalDate(now.Format("2006-01-02")), slot, sourcePath, candidatePath)
	if err != nil {
		t.Fatalf("card id: %v", err)
	}
	return cardID
}

func mustReadBriefDailySelection(t *testing.T, root string, now time.Time) brief.DailySelection {
	t.Helper()
	store := brief.NewFileStateStore(root, brief.WithClock(func() time.Time { return now }))
	date := brief.LocalDate(now.Format("2006-01-02"))
	selection, ok, err := store.ReadDailySelection(date)
	if err != nil {
		t.Fatalf("ReadDailySelection(%s): %v", date, err)
	}
	if !ok {
		t.Fatalf("ReadDailySelection(%s): no selection", date)
	}
	return selection
}

func mustSelectionCardForSlot(t *testing.T, selection brief.DailySelection, slot brief.SlotKind) brief.Card {
	t.Helper()
	for _, card := range selection.Cards {
		if card.Slot == slot {
			return card
		}
	}
	t.Fatalf("selection missing slot %q: %#v", slot, selection)
	return brief.Card{}
}

func mustDailyCardForSlot(t *testing.T, daily brief.DailyBrief, slot brief.SlotKind) brief.Card {
	t.Helper()
	for _, result := range daily.Slots {
		if result.Slot == slot && result.Card != nil {
			return *result.Card
		}
	}
	t.Fatalf("daily brief missing card slot %q: %#v", slot, daily)
	return brief.Card{}
}
