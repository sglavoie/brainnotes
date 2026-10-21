package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/config"
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
