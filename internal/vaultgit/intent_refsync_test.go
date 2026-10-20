package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
)

// failRefDirSync makes the sync of the ref's directory fail with err while
// every other directory sync behaves normally.
func failRefDirSync(h *testHarness, mgr *Manager, err error) {
	refDir := filepath.Join(mgr.gitDir, "refs", "heads")
	mgr.syncDir = func(dir string) error {
		if dir == refDir {
			return err
		}
		return nil
	}
	h.t.Cleanup(func() { mgr.syncDir = nil })
}

// The intent may retire only once the ref's directory entry is durable. A
// failed sync keeps the snapshot a success — the commit landed — but leaves the
// record for recovery, which proves the landing, syncs again, and retires it.
func TestFailedRefDirSyncKeepsTheIntentForRecovery(t *testing.T) {
	commits := map[string]func(h *testHarness) CommitResult{
		"external": func(h *testHarness) CommitResult { return h.commitPending() },
		"explicit": func(h *testHarness) CommitResult {
			result, err := h.mgr.CommitPaths(context.Background(), "recall-state", []string{".bnotes/recall.json"})
			if err != nil {
				h.t.Fatalf("CommitPaths: %v", err)
			}
			return result
		},
	}
	for name, commit := range commits {
		t.Run(name, func(t *testing.T) { checkUnsyncedRefKeepsIntent(t, commit) })
	}
}

func checkUnsyncedRefKeepsIntent(t *testing.T, commit func(h *testHarness) CommitResult) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.write(".bnotes/recall.json", `{"generation":1}`)
	failRefDirSync(h, h.mgr, fmt.Errorf("vault: fsync: %w", errors.New("input/output error")))

	result := commit(h)
	if !result.Committed || h.head() != result.Commit {
		t.Fatalf("the snapshot did not land: %+v", result)
	}
	if _, present := h.mgr.Interrupted(); !present {
		t.Fatal("the intent retired although the ref directory was never made durable")
	}

	// While the sync keeps failing, recovery leaves the record but does not
	// refuse: the commit is proven reachable.
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight with a landed but unsynced ref: %v", err)
	}
	if _, present := h.mgr.Interrupted(); !present {
		t.Fatal("recovery retired the intent while the ref directory sync still failed")
	}

	restarted := h.open(Options{})
	if _, present := restarted.Interrupted(); present {
		t.Fatal("Open did not retire an intent whose commit landed once the sync succeeded")
	}
	if err := restarted.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight after recovery: %v", err)
	}
}

// A packed ref has no loose directory to sync; that is not a durability
// failure, and the intent retires as usual.
func TestMissingRefDirCountsAsDurable(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	failRefDirSync(h, h.mgr, fmt.Errorf("vault: open: %w", fs.ErrNotExist))
	if !h.commitPending().Committed {
		t.Fatal("nothing was committed")
	}
	if _, present := h.mgr.Interrupted(); present {
		t.Fatal("a missing ref directory kept the intent")
	}
}
