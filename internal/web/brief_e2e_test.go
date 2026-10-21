package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/config"
)

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
