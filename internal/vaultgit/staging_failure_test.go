package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// failingStageShim stands in for a git whose `update-index --add` refuses. The
// nth such call is let through, so one test can provoke "every path failed" and
// another "some paths failed" with the same seam; pass 0 to fail every call.
//
// It is the only way to reach this failure from outside. A repository whose
// index cannot be written is what produced it in the field, and arranging that
// by hand means making the engine's own state directory unwritable at the exact
// instant staging runs — which breaks the block marker the assertions need to
// read afterwards.
func failingStageShim(t *testing.T, letThrough int) string {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "stage-calls")
	return gitShim(t, `
if [ "$1" = "update-index" ] && [ "$2" = "--add" ]; then
  count=$(cat `+shellQuote(counter)+` 2>/dev/null || echo 0)
  count=$((count + 1))
  printf '%s' "$count" > `+shellQuote(counter)+`
  if [ "$count" -ne `+shellQuote(strconv.Itoa(letThrough))+` ]; then
    cat > /dev/null
    echo "simulated index write failure" >&2
    exit 128
  fi
fi`)
}

// A repository that will not stage anything is not an empty vault. Turning every
// `update-index` failure into a skip made this exact case — an unwritable index,
// hit on the first call and therefore on every path — look like a snapshot with
// nothing to record: no commit, no error, and a `bn` command that printed
// "updated" and exited 0 while the history stayed where it was.
func TestCommitJournalEntryStagingFailureOfEveryPathLatchesABlock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	entry := h.record("bn set", []string{"Inbox/note.md"}, func() { h.write("Inbox/note.md", "two\n") })

	broken := h.open(Options{GitExecutable: failingStageShim(t, 0)})
	result, err := broken.CommitJournalEntry(context.Background(), entry.ID, "set")
	if err == nil {
		t.Fatalf("a repository that staged nothing reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitJournalEntry = %v, want a commit failure", err)
	}
	if result.Committed {
		t.Fatalf("a failed snapshot reported a commit: %+v", result)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although nothing could be staged")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "one" {
		t.Fatalf("history holds %q, want the pre-mutation content", got)
	}

	record, blocked := h.blockRecord()
	if !blocked || record.Kind != KindCommitFailed {
		t.Fatalf("a staging failure must latch an explained block: %+v", record)
	}
	// Fail-closed means the refusal outlives the process that saw it.
	restarted := h.open(Options{})
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight after a staging failure = %v, want a blocked failure", err)
	}
}

// Staging some of an exact snapshot's paths is worse than staging none: the tree
// would hold the journal record without the note it describes. Nothing is
// committed, and the vault is blocked rather than left looking healthy.
func TestCommitJournalEntryPartialStagingFailureLatchesABlock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	entry := h.record("bn set", []string{"Inbox/note.md"}, func() { h.write("Inbox/note.md", "two\n") })

	// Call 1 is the whole-plan batch and call 2 is the first path of the per-path
	// fallback, which is a journal record file: the plan is sorted and `.bnotes/`
	// sorts before `Inbox/`. So part of the record reaches the index and the note
	// it describes never does — the one shape a snapshot must never commit.
	broken := h.open(Options{GitExecutable: failingStageShim(t, 2)})
	result, err := broken.CommitJournalEntry(context.Background(), entry.ID, "set")
	if err == nil {
		t.Fatalf("a partially staged snapshot reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitJournalEntry = %v, want a commit failure", err)
	}
	if !strings.Contains(err.Error(), "could not be staged") {
		t.Fatalf("the failure must say which path stopped it: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved on a partially staged snapshot")
	}
	tracked := h.trackedSet()
	for path := range tracked {
		if strings.HasPrefix(path, ".bnotes/journal/") {
			t.Fatalf("a journal record was committed without the note it describes: %q", path)
		}
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "one" {
		t.Fatalf("history holds %q, want the pre-mutation content", got)
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("a partial staging failure must latch a block")
	}
}

// The external-change path is allowed to skip a note it cannot read. It is not
// allowed to skip a note it can read perfectly well, because a git that refuses
// one of those is failing for a reason that applies to the whole vault.
func TestCommitPendingRepositoryWideStagingFailureIsFatal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	h.write("Inbox/second.md", "two\n")

	broken := h.open(Options{GitExecutable: failingStageShim(t, 0)})
	result, err := broken.CommitPending(context.Background())
	if err == nil {
		t.Fatalf("a repository that staged nothing reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPending = %v, want a commit failure", err)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("a repository-wide failure must not be reported as skipped paths: %+v", result.Skipped)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although nothing could be staged")
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("a repository-wide staging failure must latch a block")
	}
}

// A caller that names a path is asking for that path. When the vault will not
// hand its content over, the honest answer is a refusal — not a success that
// omits it. The same unreadable file is a tolerated skip on the external path,
// and TestUnreadableNoteIsSkippedNotFatal is what pins that half down.
func TestCommitPathsFailsClosedOnAnUnreadableRequiredPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0000 file, so this case cannot be provoked")
	}
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	state := filepath.Join(h.root, ".bnotes", "recall.json")
	h.write(".bnotes/recall.json", "{}\n")
	if err := os.Chmod(state, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0o644) })

	h.advance(time.Minute)
	result, err := h.mgr.CommitPaths(context.Background(), "recall-state", []string{state})
	if err == nil {
		t.Fatalf("an unrecordable named path reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPaths = %v, want a commit failure", err)
	}
	if !contains(result.Unrecorded(), ".bnotes/recall.json") && !strings.Contains(err.Error(), ".bnotes/recall.json") {
		t.Fatalf("the failure must name the path it could not record: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although the named path was never staged")
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("an unrecordable named path must latch a block")
	}
}

// --- Staging that succeeds without doing anything ----------------------------

// noopStagingShim stands in for a git whose `update-index` exits 0 without
// touching the index.
//
// It is a different failure from failingStageShim and a nastier one. A refusal
// is reported and can be reacted to; a successful no-op leaves the private index
// exactly as `read-tree` seeded it — which is to say holding HEAD's entry for
// every already-tracked path — so a readback that only asks whether the path has
// an entry is satisfied by the very content the snapshot was supposed to replace.
// The tree then matches the tip, the engine takes its identical-tree branch, and
// the caller is handed a success for a change the history does not hold.
func noopStagingShim(t *testing.T, subcommand string) string {
	t.Helper()
	return gitShim(t, `
if [ "$1" = "update-index" ] && [ "$2" = `+shellQuote(subcommand)+` ]; then
  cat > /dev/null
  exit 0
fi`)
}

// An exact snapshot of a path already in the history is the case where staging
// can fail silently: the seeded index already answers for the path, so nothing
// short of comparing the staged object against the file's current content can
// tell a recorded modification from an unrecorded one.
func TestExactSnapshotSuccessfulNoOpAdditionStagingFailsClosed(t *testing.T) {
	h := newHarness(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0000 file, so the external half of this test cannot be provoked")
	}
	state := filepath.Join(h.root, ".bnotes", "recall.json")
	h.write(".bnotes/recall.json", "{\"generation\":1}\n")
	h.commitPending()
	safe := h.head()
	if safe == "" {
		t.Fatal("the fixture never committed the state file")
	}

	h.advance(time.Minute)
	h.write(".bnotes/recall.json", "{\"generation\":2}\n")

	broken := h.open(Options{GitExecutable: noopStagingShim(t, "--add")})
	result, err := broken.CommitPaths(context.Background(), "recall-state", []string{state})
	if err == nil {
		t.Fatalf("a snapshot whose staging did nothing reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPaths = %v, want a commit failure", err)
	}
	if result.Committed {
		t.Fatalf("a failed snapshot reported a commit: %+v", result)
	}
	if !strings.Contains(err.Error(), ".bnotes/recall.json") {
		t.Fatalf("the failure must name the path the index does not hold: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although nothing reached the index")
	}
	if got := rawGit(t, h.root, "show", "HEAD:.bnotes/recall.json"); got != `{"generation":1}` {
		t.Fatalf("history holds %q, want the pre-mutation content", got)
	}

	record, blocked := h.blockRecord()
	if !blocked || record.Kind != KindCommitFailed {
		t.Fatalf("a silently unstaged required path must latch an explained block: %+v", record)
	}
	// Fail-closed means the refusal outlives the process that saw it.
	restarted := h.open(Options{})
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight after a silent staging no-op = %v, want a blocked failure", err)
	}

	// The tightened readback is exact-plan only. An external scan may still leave
	// out an addition the vault will not hand over, which is the one skip this
	// engine tolerates and the one a stricter check must not have swallowed.
	if err := h.mgr.ClearBlock(); err != nil {
		t.Fatalf("ClearBlock: %v", err)
	}
	h.write("Inbox/locked.md", "unreadable\n")
	locked := filepath.Join(h.root, "Inbox", "locked.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	h.advance(time.Minute)
	external := h.commitPending()
	if !external.Committed {
		t.Fatalf("one unreadable note stopped the whole external snapshot: %+v", external)
	}
	if len(external.Skipped) != 1 || external.Skipped[0].Path != "Inbox/locked.md" {
		t.Fatalf("skipped = %+v", external.Skipped)
	}
	if got := rawGit(t, h.root, "show", "HEAD:.bnotes/recall.json"); got != `{"generation":2}` {
		t.Fatalf("the external pass did not record the state file: %q", got)
	}
}

// The removal half of the same hole. A restore that only drops files has no
// additions to change the tree, so a `--force-remove` that quietly did nothing
// produces a tree identical to the tip — and the engine's identical-tree branch
// turns that into a nil success naming deletions the history never recorded.
func TestExactSnapshotSuccessfulNoOpRemovalStagingFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/kept.md", "kept\n")
	h.write("Inbox/dropped.md", "dropped\n")
	h.commitPending()
	safe := h.head()
	if safe == "" {
		t.Fatal("the fixture never committed the notes")
	}

	// A restore made the vault look like an archive that does not hold this note.
	h.advance(time.Minute)
	h.remove("Inbox/dropped.md")

	broken := h.open(Options{GitExecutable: noopStagingShim(t, "--force-remove")})
	result, err := broken.CommitRestore(context.Background(), "restore")
	if err == nil {
		t.Fatalf("a snapshot whose removal did nothing reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitRestore = %v, want a commit failure", err)
	}
	if result.Committed {
		t.Fatalf("a failed snapshot reported a commit: %+v", result)
	}
	if !strings.Contains(err.Error(), "Inbox/dropped.md") {
		t.Fatalf("the failure must name the path still in the index: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although the removal never reached the index")
	}
	// Nothing was committed, so the note is still recoverable from the tip.
	if !h.trackedSet()["Inbox/dropped.md"] {
		t.Fatal("the note left the history on a refused removal")
	}

	record, blocked := h.blockRecord()
	if !blocked || record.Kind != KindCommitFailed {
		t.Fatalf("a silently unstaged removal must latch an explained block: %+v", record)
	}
	if !contains(record.Paths, "Inbox/dropped.md") {
		t.Fatalf("the block record must name the path: %+v", record.Paths)
	}
	restarted := h.open(Options{})
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight after a silent removal no-op = %v, want a blocked failure", err)
	}
}

// --- Preflight ---------------------------------------------------------------
