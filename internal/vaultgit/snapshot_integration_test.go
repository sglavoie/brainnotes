package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreflightCleanVault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight on an unborn branch: %v", err)
	}
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight on a clean vault: %v", err)
	}
}

func TestPreflightAllowsExternalDeletion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one")
	h.commitPending()
	h.remove("Inbox/note.md")
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("external deletion latched a block")
	}
}

func TestPreflightRefusesInProgressGitOperation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	if err := os.WriteFile(filepath.Join(h.root, ".git", "MERGE_HEAD"), []byte(h.head()+"\n"), 0o644); err != nil {
		t.Fatalf("write MERGE_HEAD: %v", err)
	}

	err := h.mgr.Preflight(context.Background())
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Preflight during a merge = %v, want a repository-state failure", err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("an in-progress merge is the user's to finish and must not latch a block")
	}
}

// Two BrainNotes processes on one vault must not interleave inside a snapshot.
// The loser is told it is busy, which is retryable and says nothing about the
// vault's health.
func TestConcurrentManagersSerialize(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	other := h.open(Options{})
	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if err := other.Preflight(context.Background()); !IsBusy(err) {
		release()
		t.Fatalf("Preflight while another process holds the lock = %v, want busy", err)
	}
	if _, err := other.CommitPending(context.Background()); !IsBusy(err) {
		release()
		t.Fatalf("CommitPending while another process holds the lock = %v, want busy", err)
	}
	if Blocks(err) {
		release()
		t.Fatal("a busy vault must not latch a block")
	}

	release()
	if err := other.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight after the lock was released: %v", err)
	}
}

// The mutation lock and the push lock are independent, so a push stuck on an
// unreachable network cannot delay a note save.
func TestPushLockDoesNotBlockSnapshots(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	release, err := h.mgr.acquire(pushLockName, "test")
	if err != nil {
		t.Fatalf("acquire push lock: %v", err)
	}
	defer release()

	h.write("Inbox/note.md", "one\n")
	if _, err := h.mgr.CommitPending(context.Background()); err != nil {
		t.Fatalf("CommitPending while a push is in flight: %v", err)
	}
}

// --- Journal-entry commits ---------------------------------------------------

func TestCommitJournalEntryStagesOnlyItsOwnPaths(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/tracked.md", "one\n")
	h.commitPending()

	h.advance(time.Minute)
	// The user is editing an unrelated note in another window while `bn` runs.
	h.write("Inbox/unrelated.md", "work in progress\n")
	entry := h.record("bn set", []string{"Inbox/tracked.md"}, func() {
		h.write("Inbox/tracked.md", "two\n")
	})

	result, err := h.mgr.CommitJournalEntry(context.Background(), entry.ID, "set")
	if err != nil {
		t.Fatalf("CommitJournalEntry: %v", err)
	}
	if !result.Committed {
		t.Fatalf("nothing was committed: %+v", result)
	}
	if result.Message != "brainnotes: set ["+entry.ID+"]" {
		t.Fatalf("message = %q", result.Message)
	}
	tracked := h.trackedSet()
	if !tracked["Inbox/tracked.md"] {
		t.Fatal("the mutated note is not tracked")
	}
	if tracked["Inbox/unrelated.md"] {
		t.Fatal("an unrelated in-progress note was swept into the commit")
	}
	if !tracked[".bnotes/journal/"+entry.ID+"/manifest.json"] {
		t.Fatalf("the journal record was not committed; tracked = %v", h.tracked())
	}
}

func TestCommitJournalEntryDeletion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/gone.md", "goodbye\n")
	h.commitPending()

	h.advance(time.Minute)
	entry := h.record("bn rm", []string{"Inbox/gone.md"}, func() { h.remove("Inbox/gone.md") })

	result, err := h.mgr.CommitJournalEntry(context.Background(), entry.ID, "remove")
	if err != nil {
		t.Fatalf("CommitJournalEntry: %v", err)
	}
	if !contains(result.Deleted, "Inbox/gone.md") {
		t.Fatalf("deleted = %v", result.Deleted)
	}
	if h.trackedSet()["Inbox/gone.md"] {
		t.Fatal("the deleted note is still tracked")
	}
}

// If the world moved between the mutation and the snapshot, the request is
// stale rather than dangerous: nothing is committed, nothing is blocked, and the
// next pending-change scan picks up whatever the vault actually holds.
func TestCommitJournalEntryRefusesStaleRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	entry := h.record("bn set", []string{"Inbox/note.md"}, func() { h.write("Inbox/note.md", "two\n") })
	// Someone edited the note again before the snapshot ran.
	h.write("Inbox/note.md", "three\n")

	_, err := h.mgr.CommitJournalEntry(context.Background(), entry.ID, "set")
	if KindOf(err) != KindStaleRequest {
		t.Fatalf("CommitJournalEntry = %v, want a stale-request failure", err)
	}
	if h.head() != safe {
		t.Fatal("a stale request moved the branch")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a stale request must not latch a block")
	}

	// The ordinary pending-change path still records the current content.
	if !h.commitPending().Committed {
		t.Fatal("the following snapshot recorded nothing")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "three" {
		t.Fatalf("history holds %q, want the current content", got)
	}
}

func TestCommitJournalEntryRejectsBadEntryID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, id := range []string{"", "../escape", "entry/1"} {
		if _, err := h.mgr.CommitJournalEntry(context.Background(), id, "set"); KindOf(err) != KindStaleRequest {
			t.Fatalf("CommitJournalEntry(%q) = %v, want a stale-request failure", id, err)
		}
	}
}

// --- The user's index --------------------------------------------------------

// An engine commit must leave `git status` clean, or the user sees what looks
// like a staged revert and is invited to "fix" it with a checkout that would
// discard the snapshot.
func TestSnapshotLeavesUserStatusClean(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.write("Inbox/other.md", "two\n")
	h.commitPending()
	if out := rawGit(t, h.root, "status", "--porcelain"); out != "" {
		t.Fatalf("status after the first snapshot is not clean:\n%s", out)
	}

	h.advance(time.Minute)
	h.write("Inbox/note.md", "one, edited\n")
	h.commitPending()
	if out := rawGit(t, h.root, "status", "--porcelain"); out != "" {
		t.Fatalf("status after a later snapshot is not clean:\n%s", out)
	}

	h.advance(time.Minute)
	h.record("bn rm", []string{"Inbox/other.md"}, func() { h.remove("Inbox/other.md") })
	h.commitPending()
	if out := rawGit(t, h.root, "status", "--porcelain"); out != "" {
		t.Fatalf("status after an authorized deletion is not clean:\n%s", out)
	}
}

// Whatever the user deliberately staged is theirs. The engine commits the
// working tree through a private index and never touches an entry the user has
// moved away from HEAD.
func TestUserStagedWorkSurvivesASnapshot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.write("Inbox/staged.md", "v1\n")
	h.commitPending()

	// The user stages a version of their own, then keeps editing on disk.
	h.write("Inbox/staged.md", "v2 staged by hand\n")
	rawGit(t, h.root, "add", "Inbox/staged.md")
	stagedOID := rawGit(t, h.root, "rev-parse", ":Inbox/staged.md")
	h.write("Inbox/staged.md", "v3 on disk\n")

	h.advance(time.Minute)
	h.write("Inbox/note.md", "one, edited\n")
	if !h.commitPending().Committed {
		t.Fatal("the snapshot recorded nothing")
	}

	if got := rawGit(t, h.root, "rev-parse", ":Inbox/staged.md"); got != stagedOID {
		t.Fatalf("the user's staged entry changed: %s -> %s", stagedOID, got)
	}
	if got := rawGit(t, h.root, "cat-file", "blob", stagedOID); got != "v2 staged by hand" {
		t.Fatalf("the user's staged content is %q", got)
	}
	// The engine records what is on disk, which is the newest content.
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/staged.md"); got != "v3 on disk" {
		t.Fatalf("history holds %q, want the working-tree content", got)
	}
}

// --- Concurrency and failure -------------------------------------------------

// Losing a compare-and-swap means another writer committed first. The snapshot
// is rebuilt against the new tip rather than forced onto the old one, so the
// other writer's commit survives.
func TestRefConflictIsRetriedNotForced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	first := h.head()

	marker := filepath.Join(t.TempDir(), "fired")
	shim := gitShim(t, `
if [ "$1" = "update-ref" ] && [ ! -e `+shellQuote(marker)+` ]; then
  : > `+shellQuote(marker)+`
  tree=$("$REAL_GIT" rev-parse "HEAD^{tree}")
  commit=$(GIT_AUTHOR_NAME=Interloper GIT_AUTHOR_EMAIL=i@example.invalid \
           GIT_COMMITTER_NAME=Interloper GIT_COMMITTER_EMAIL=i@example.invalid \
           "$REAL_GIT" commit-tree "$tree" -p HEAD -m interloper)
  "$REAL_GIT" update-ref refs/heads/main "$commit"
fi`)

	racing := h.open(Options{GitExecutable: shim})
	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")

	result, err := racing.CommitPending(context.Background())
	if err != nil {
		t.Fatalf("CommitPending under a losing race: %v", err)
	}
	if !result.Committed {
		t.Fatalf("nothing was committed: %+v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the test did not actually provoke a conflict")
	}
	subjects := h.subjects()
	if !contains(subjects, "interloper") {
		t.Fatalf("the other writer's commit was discarded: %v", subjects)
	}
	if h.head() == first {
		t.Fatal("the branch did not advance")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "two" {
		t.Fatalf("history holds %q after the retry", got)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a conflict that was retried successfully must not latch a block")
	}
}

// A local commit that cannot be written is the failure this engine cannot
// paper over: the working tree holds content the history does not, so it stops
// and says so durably.
func TestLocalCommitFailureLatchesABlock(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	shim := gitShim(t, `
if [ "$1" = "commit-tree" ]; then
  echo "simulated object write failure" >&2
  exit 128
fi`)
	broken := h.open(Options{GitExecutable: shim})
	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")

	_, err := broken.CommitPending(context.Background())
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPending = %v, want a commit failure", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved despite a failed commit")
	}
	record, blocked := h.blockRecord()
	if !blocked || record.Kind != KindCommitFailed || len(record.Recovery) == 0 {
		t.Fatalf("a commit failure must latch an explained block: %+v", record)
	}

	// Restart recovery: a fresh Manager on a healthy git still refuses until a
	// person clears the marker, then works normally.
	restarted := h.open(Options{})
	if _, err := restarted.CommitPending(context.Background()); !IsBlocked(err) {
		t.Fatalf("a restarted manager = %v, want a blocked failure", err)
	}
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight on a blocked vault = %v, want a blocked failure", err)
	}
	if err := restarted.ClearBlock(); err != nil {
		t.Fatalf("ClearBlock: %v", err)
	}
	if !mustCommit(t, restarted).Committed {
		t.Fatal("the vault did not recover after the marker was cleared")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "two" {
		t.Fatalf("history holds %q after recovery", got)
	}
}

// A block marker written by an older or newer build, or truncated by a crash,
// keeps refusing rather than silently re-enabling mutations.
func TestCorruptBlockMarkerRefusesMutations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	if err := h.mgr.state.ensureDir(); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	if err := os.WriteFile(h.mgr.state.path(blockedFileName), []byte("{ truncated"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	restarted := h.open(Options{})
	h.write("Inbox/note.md", "two\n")
	if _, err := restarted.CommitPending(context.Background()); !IsBlocked(err) {
		t.Fatalf("CommitPending with a corrupt marker = %v, want a blocked failure", err)
	}
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight with a corrupt marker = %v, want a blocked failure", err)
	}
}

func mustCommit(t *testing.T, m *Manager) CommitResult {
	t.Helper()
	result, err := m.CommitPending(context.Background())
	if err != nil {
		t.Fatalf("CommitPending: %v", err)
	}
	return result
}

// --- Push --------------------------------------------------------------------
