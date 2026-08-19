package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestServer_PinsAPI_ReorderRenormalizesAListPastTheOldWriteCap(t *testing.T) {
	assertReorderRenormalizesWholeList(t, 300)
}

// TestServer_PinsAPI_ReorderRenormalizesBeyondLockedPathCeiling runs the same
// drag on a list longer than the number of paths one transaction locks
// individually. That size used to be refused outright, so a pinned list past the
// boundary rendered and dragged in the browser and could never persist the order
// it showed.
func TestServer_PinsAPI_ReorderRenormalizesBeyondLockedPathCeiling(t *testing.T) {
	assertReorderRenormalizesWholeList(t, vault.MaxLockedPaths()+8)
}

// assertReorderRenormalizesWholeList pins every note to one rank, so no float
// fits between any two neighbours and a single drag renumbers the whole list.
func assertReorderRenormalizesWholeList(t *testing.T, count int) {
	t.Helper()

	files := make(map[string][]byte, count)
	rels := make([]string, 0, count)
	for i := 0; i < count; i++ {
		rel := fmt.Sprintf("P%05d.md", i)
		rels = append(rels, rel)
		files[rel] = []byte(fmt.Sprintf("---\npinned: 1.0\ntitle: P%05d\n---\nBody %d.\n", i, i))
	}
	ts, root := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: "csrf-token"})

	last := rels[count-1]
	resp := postPinRequest(t, ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key":    managementRouteKeyForRelPath(last),
		"before": managementRouteKeyForRelPath(rels[0]),
		"after":  managementRouteKeyForRelPath(rels[1]),
	}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Reordered pins." || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("payload = %#v, want reorder success with journal", payload)
	}

	// The dragged note lands second and every note after it shifts down one.
	want := map[string]string{
		rels[0]:       "pinned: 1.0\n",
		last:          "pinned: 2.0\n",
		rels[1]:       "pinned: 3.0\n",
		rels[count-2]: fmt.Sprintf("pinned: %d.0\n", count),
	}
	for rel, rank := range want {
		if got := readNoteFile(t, filepath.Join(root, rel)); !strings.Contains(got, rank) {
			t.Fatalf("%s: want %q, got:\n%s", rel, rank, got)
		}
	}
	if n := countJournalEntries(t, root); n != 1 {
		t.Fatalf("journal entries = %d, want 1 undoable action", n)
	}
}
