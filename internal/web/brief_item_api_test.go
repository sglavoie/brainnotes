package web

import (
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
