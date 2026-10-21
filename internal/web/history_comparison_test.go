package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestHistoryComparisonShowsCurrentAndRestoredBytes(t *testing.T) {
	baseURL, root, entry := newRestorableVault(t)
	driftSourceNote(t, root, "newer-work")
	preview := previewHistoryRestore(t, baseURL, entry, "csrf-token")
	if len(preview.Changes) != 1 {
		t.Fatalf("changes: %+v", preview.Changes)
	}
	comparison := preview.Changes[0].Comparison
	if comparison == nil || !strings.Contains(comparison.Current, "status: newer-work") || !strings.Contains(comparison.Restored, "status: draft") {
		t.Fatalf("comparison: %+v", comparison)
	}
	// Consent to these displayed bytes does not authorize replacing a later edit.
	driftSourceNote(t, root, "even-newer")
	response := postUndoRequestWithPreview(t, baseURL, entry, "csrf-token", "", preview.PreviewID, true, true)
	body := readResponseBody(t, response)
	if response.StatusCode != 409 {
		t.Fatalf("stale comparison accepted: %d %s", response.StatusCode, body)
	}
}

func TestHistoryComparisonOmitsPrivateAndDriftedContent(t *testing.T) {
	_, root, entry := newRestorableVault(t)
	server := &Server{vault: &vault.Vault{Root: root}}
	response := historyPreviewResponse{EntryID: entry, Changes: []historyPreviewChange{
		{Path: "Source.md", Class: "state"},
		{Path: "Source.md", Class: "configuration"},
		{Path: "Source.md", Class: "note"},
	}}
	server.addHistoryComparisons(httptest.NewRecorder(), &response, []propertyPreviewFileState{{RelPath: "Source.md", Digest: "stale-digest"}})
	for _, change := range response.Changes {
		if change.Comparison != nil || change.ComparisonUnavailable == "" {
			t.Fatalf("unexpected disclosure: %+v", change)
		}
	}
}

func TestHistoryComparisonBoundsTheNumberOfFiles(t *testing.T) {
	_, root, entry := newRestorableVault(t)
	server := &Server{vault: &vault.Vault{Root: root}}
	digest, err := historyRestoreDigest(filepath.Join(root, "Source.md"))
	if err != nil {
		t.Fatal(err)
	}
	response := historyPreviewResponse{EntryID: entry}
	for range historyComparisonCountLimit + 1 {
		response.Changes = append(response.Changes, historyPreviewChange{Path: "Source.md", Class: "note"})
	}
	server.addHistoryComparisons(httptest.NewRecorder(), &response, []propertyPreviewFileState{{RelPath: "Source.md", Digest: digest}})
	if response.Changes[historyComparisonCountLimit-1].Comparison == nil {
		t.Fatal("last allowed comparison omitted")
	}
	if response.Changes[historyComparisonCountLimit].Comparison != nil {
		t.Fatal("comparison count is unbounded")
	}
	if !strings.Contains(response.Changes[historyComparisonCountLimit].ComparisonUnavailable, "limit") {
		t.Fatal("limit not explained")
	}
}
