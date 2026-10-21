package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

func TestServer_BulkAPI_TrashCollisionPreviewAndApplyKeepPlannedDestination(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"A.md":       []byte("body\n"),
		"Trash/A.md": []byte("existing trash\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	previewResp := postBulkPreviewRequest(t, ts.URL, "trash", []string{"a"}, nil, "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusOK, previewBody)
	}
	previewPayload := decodeBulkPreviewResult(t, previewBody)
	if !previewPayload.OK || previewPayload.Action != "trash" || previewPayload.Changed != 1 || previewPayload.Failed != 0 {
		t.Fatalf("preview payload = %#v, want one trashed preview", previewPayload)
	}
	if len(previewPayload.Results) != 1 || previewPayload.Results[0].Destination == nil {
		t.Fatalf("preview results = %#v, want one trash destination", previewPayload.Results)
	}
	previewResult := previewPayload.Results[0]
	previewDest := previewResult.Destination.RelPath
	if matched := regexp.MustCompile(`^Trash/A\.deleted-\d{8}T\d{6}Z-[A-Za-z0-9]+\.md$`).MatchString(previewDest); !matched {
		t.Fatalf("preview destination = %q, want deleted timestamp suffix", previewDest)
	}

	resp := postBulkRequest(t, ts.URL, "trash", []string{"a"},
		bulkFieldsWithPreviewID(nil, previewPayload.PreviewID),
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBulkManagementResult(t, body)
	if !payload.OK || payload.Changed != 1 || payload.Unchanged != 0 || payload.Failed != 0 || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("apply payload = %#v, want one changed trash result and a journal", payload)
	}
	if len(payload.Results) != 1 || payload.Results[0].RelPath != previewDest {
		t.Fatalf("apply results = %#v, want previewed destination %q", payload.Results, previewDest)
	}
	if _, err := os.Stat(filepath.Join(root, "A.md")); !os.IsNotExist(err) {
		t.Fatalf("source still exists after apply: %v", err)
	}
	if got := readNoteFile(t, filepath.Join(root, "Trash", "A.md")); got != "existing trash\n" {
		t.Fatalf("existing trash note changed:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, filepath.FromSlash(previewDest))); got != "body\n" {
		t.Fatalf("planned trash destination missing content:\n%s", got)
	}

	manifestPath := filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}
	var manifest struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v; body=%q", err, string(manifestData))
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("journal files = %#v, want source and planned trash destination", manifest.Files)
	}
	if manifest.Files[0].Path != "A.md" || manifest.Files[1].Path != previewDest {
		t.Fatalf("journal files = %#v, want A.md then %q", manifest.Files, previewDest)
	}
}
