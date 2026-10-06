package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyDeletionBlockRecovery(t *testing.T) {
	for _, kind := range []Kind{KindUnexplainedDeletion, KindCommitFailed, KindAmbiguousChange} {
		t.Run(string(kind), func(t *testing.T) {
			h := newHarness(t)
			h.write("gone.md", "recoverable")
			h.commitPending()
			h.remove("gone.md")
			if err := h.mgr.state.SaveBlock(BlockRecord{Kind: kind, Paths: []string{"gone.md"}}); err != nil {
				t.Fatal(err)
			}
			restarted := h.open(Options{})
			err := restarted.Preflight(context.Background())
			_, blocked := restarted.Blocked()
			if kind == KindUnexplainedDeletion {
				if err != nil || blocked {
					t.Fatalf("legacy deletion block survived: %v, %v", err, blocked)
				}
				if result := h.commitPending(); !result.Committed {
					t.Fatal("deletion not recorded after migration")
				}
			} else if !IsBlocked(err) || !blocked {
				t.Fatalf("unrelated block cleared: %v, %v", err, blocked)
			}
		})
	}
}

func TestLegacyDeletionBlockDoesNotBypassRepositoryChecks(t *testing.T) {
	h := newHarness(t)
	h.write("gone.md", "recoverable")
	h.commitPending()
	h.remove("gone.md")
	if err := h.mgr.state.SaveBlock(BlockRecord{Kind: KindUnexplainedDeletion}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(h.root, ".git", "MERGE_HEAD")
	if err := os.WriteFile(marker, []byte(h.head()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.Preflight(context.Background()); KindOf(err) != KindRepositoryState {
		t.Fatal(err)
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("legacy block cleared before repository checks passed")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyDeletionMigrationPreservesDamagedState(t *testing.T) {
	for _, data := range []string{
		`{"version":`,
		`{"version":999,"kind":"unexplained_deletion"}`,
	} {
		t.Run(data, func(t *testing.T) {
			h := newHarness(t)
			if err := h.mgr.state.ensureDir(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(h.mgr.state.path(blockedFileName), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := h.mgr.Preflight(context.Background()); !IsBlocked(err) {
				t.Fatalf("damaged block was ignored: %v", err)
			}
		})
	}
}

func TestLegacyDeletionMigrationPreservesInterruptedSnapshot(t *testing.T) {
	h := newHarness(t)
	h.write("gone.md", "recoverable")
	h.commitPending()
	h.remove("gone.md")
	if err := h.mgr.state.SaveBlock(BlockRecord{Kind: KindUnexplainedDeletion}); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.state.SaveIntent(IntentRecord{Operation: "commit-pending", Phase: IntentPhasePlanned, Ref: "refs/heads/main", OldOID: h.head()}); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.Preflight(context.Background()); err == nil {
		t.Fatal("legacy migration bypassed an unresolved snapshot")
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("unresolved snapshot lost its block")
	}
}

func TestExternalDirectoryRemovalAndRename(t *testing.T) {
	h := newHarness(t)
	h.write("Folder/a.md", "a")
	h.write("Folder/b.md", "b")
	h.write("old.md", "moved")
	h.commitPending()
	if err := os.RemoveAll(filepath.Join(h.root, "Folder")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(h.root, "old.md"), filepath.Join(h.root, "new.md")); err != nil {
		t.Fatal(err)
	}
	result := h.commitPending()
	if len(result.Deleted) != 3 || !h.trackedSet()["new.md"] {
		t.Fatalf("external changes not recorded: %+v", result)
	}
}

func TestRecreatedRemovalRetriesWithoutBlockOrIntent(t *testing.T) {
	h := newHarness(t)
	h.write("note.md", "original")
	h.commitPending()
	head := h.head()
	h.remove("note.md")
	h.write("other.md", "new content")
	// Recreate the path after scanning, while additions are being staged.
	shim := gitShim(t, `
if [ "$1" = "update-index" ] && [ "$2" = "--add" ]; then
 printf recreated > `+shellQuote(filepath.Join(h.root, "note.md"))+`
fi`)
	racing := h.open(Options{GitExecutable: shim})
	_, err := racing.CommitPending(context.Background())
	if KindOf(err) != KindStaleRequest || h.head() != head {
		t.Fatalf("recreation was not retryable: %v", err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("recreation blocked the vault")
	}
	if _, present := h.mgr.Interrupted(); present {
		t.Fatal("retryable recreation left a recovery intent")
	}
	if state := h.mgr.WatchStatus(); state.ExternalCommitStartedAt != nil {
		t.Fatal("aborted snapshot consumed an autosave interval")
	}
	h.commitPending()
	if got := rawGit(t, h.root, "show", "HEAD:note.md"); got != "recreated" {
		t.Fatal(got)
	}
}

func TestExternalRemovalStagingNoOpFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.write("note.md", "original")
	h.commitPending()
	head := h.head()
	h.remove("note.md")
	broken := h.open(Options{GitExecutable: noopStagingShim(t, "--force-remove")})
	if _, err := broken.CommitPending(context.Background()); KindOf(err) != KindCommitFailed {
		t.Fatal(err)
	}
	if h.head() != head {
		t.Fatal("ineffective removal advanced HEAD")
	}
}
