package vaultgit

import (
	"context"
	"errors"
	"testing"
)

// branchLookupShim stands in for a git whose branch lookup fails while every
// other command works. exitCode 128 is a lookup that could not run at all; exit
// 1 is git answering "does not resolve" for a branch that does, which is what a
// ref read racing another writer looks like from outside.
func branchLookupShim(t *testing.T, exitCode string) string {
	t.Helper()
	return gitShim(t, `
if [ "$1" = "rev-parse" ] && [ "$2" = "--verify" ] && [ "$3" = "--quiet" ]; then
  case "$4" in
    refs/heads/*) exit `+exitCode+` ;;
  esac
fi`)
}

// A branch lookup that failed is not an unborn branch. Reading it as one seeded
// the private index empty, so every tracked path looked staged for deletion and
// the guard latched a block naming the whole vault — on a working tree that had
// not changed at all.
func TestTransientBranchLookupFailureIsNotAnUnbornBranch(t *testing.T) {
	for _, tc := range []struct{ name, exitCode string }{
		{"lookup could not run", "128"},
		{"branch with a reflog did not resolve", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.write("Inbox/note.md", "one\n")
			h.write("Inbox/other.md", "two\n")
			h.commitPending()
			before := h.head()

			broken := h.open(Options{GitExecutable: branchLookupShim(t, tc.exitCode)})
			if _, err := broken.ScanPending(context.Background()); err == nil {
				t.Fatal("ScanPending succeeded without being able to resolve the branch")
			}
			if result, err := broken.CommitPending(context.Background()); err == nil {
				t.Fatalf("CommitPending succeeded without being able to resolve the branch: %+v", result)
			}
			if record, blocked := h.blockRecord(); blocked {
				t.Fatalf("a failed branch lookup latched a block: %+v", record)
			}
			if h.head() != before {
				t.Fatal("the branch moved although its tip could not be read")
			}

			summary, err := h.mgr.ScanPending(context.Background())
			if err != nil {
				t.Fatalf("ScanPending after the lookup recovered: %v", err)
			}
			if len(summary.Added)+len(summary.Modified)+len(summary.Deleted) != 0 {
				t.Fatalf("an unchanged tree reported pending changes: %+v", summary)
			}
		})
	}
}

// The history view resolves the tip the same way, and its callers act on
// ErrUnbornBranch: the importer reads it as "nothing is committed yet".
func TestTipDoesNotReportAFailedLookupAsUnborn(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	for _, exitCode := range []string{"1", "128"} {
		view, err := OpenHistory(context.Background(), h.root, HistoryOptions{GitExecutable: branchLookupShim(t, exitCode)})
		if err != nil {
			t.Fatalf("OpenHistory: %v", err)
		}
		_, err = view.Tip(context.Background())
		if err == nil || errors.Is(err, ErrUnbornBranch) {
			t.Fatalf("Tip with a failed lookup (exit %s) = %v, want a resolution failure", exitCode, err)
		}
	}
}
