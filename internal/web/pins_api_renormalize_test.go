package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServer_PinsAPI_ReorderRenormalizesAListPastTheOldWriteCap(t *testing.T) {
	assertReorderRenormalizesWholeList(t, 300, 0)
}

// TestServer_PinsAPI_ReorderRenormalizesBeyondLockedPathCeiling runs the same
// drag on a list longer than the number of paths one transaction locks
// individually. That size used to be refused outright, so a pinned list past the
// boundary rendered and dragged in the browser and could never persist the order
// it showed.
func TestServer_PinsAPI_ReorderRenormalizesBeyondLockedPathCeiling(t *testing.T) {
	const lockEscalation = 4
	assertReorderRenormalizesWholeList(t, lockEscalation+8, lockEscalation)
}

// assertReorderRenormalizesWholeList pins every note to one rank, so no float
// fits between any two neighbours and a single drag renumbers the whole list.
func assertReorderRenormalizesWholeList(t *testing.T, count, lockEscalation int) {
	t.Helper()

	files := make(map[string][]byte, count)
	rels := make([]string, 0, count)
	for i := 0; i < count; i++ {
		rel := fmt.Sprintf("P%05d.md", i)
		rels = append(rels, rel)
		files[rel] = []byte(fmt.Sprintf("---\npinned: 1.0\ntitle: P%05d\n---\nBody %d.\n", i, i))
	}
	ts, srv, root := newPinnedNotesServer(t, files)
	srv.pinnedNotesLockEscalation = lockEscalation

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
	assertEscalatedTransactionLock(t, root, lockEscalation)
}

func assertEscalatedTransactionLock(t *testing.T, root string, lockEscalation int) {
	t.Helper()
	if lockEscalation <= 0 {
		return
	}
	locks, err := transactionLockNames(root)
	if err != nil {
		t.Fatalf("read transaction locks: %v", err)
	}
	if len(locks) != 1 || locks[0] != "vault.lock" {
		t.Fatalf("transaction locks = %v, want only escalated vault.lock", locks)
	}
}

// transactionLockNames lists the lock files a transaction created, which is what
// this assertion is about: one escalated vault.lock rather than a file per path.
//
// The checkpoint barrier keeps its two flocks in the same directory — every
// flock this program takes lives in one place — and they are not transaction
// locks: they are created by the join every journaled mutation passes through,
// once per vault, however many paths the transaction names. Counting them here
// would make this test fail for a reason it is not asking about.
func transactionLockNames(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, ".bnotes", "locks"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "checkpoint.") {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}
