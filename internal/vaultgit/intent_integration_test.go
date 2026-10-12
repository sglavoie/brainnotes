package vaultgit

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// secretBody is the note content every test here writes. Nothing the engine
// persists is allowed to contain it, so the assertions can be literal.
const secretBody = "irreplaceable-and-unmistakable\n"

// safetyDir reports the state directory, and registers a cleanup that puts its
// permissions back. Several tests here take write permission away to provoke a
// state-write failure, and a directory left at 0500 would defeat t.TempDir's
// own cleanup.
func (h *testHarness) safetyDir() string {
	h.t.Helper()
	dir := h.mgr.state.Dir()
	h.t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	return dir
}

func (h *testHarness) intentBytes() []byte {
	h.t.Helper()
	data, err := os.ReadFile(h.mgr.state.path(intentFileName))
	if err != nil {
		h.t.Fatalf("read intent record: %v", err)
	}
	return data
}

// workTreeIsClean asserts the vault holds exactly what the tip holds. It is what
// lets a later assertion attribute a refusal to the intent record rather than to
// a pending change the engine would have refused anyway.
func (h *testHarness) requireCleanWorkTree() {
	h.t.Helper()
	if out := rawGit(h.t, h.root, "status", "--porcelain"); out != "" {
		h.t.Fatalf("the working tree is not clean, so a refusal cannot be attributed to the intent record:\n%s", out)
	}
}

// A local commit failure and a blocked.json write failure at the same instant is
// the case the block marker cannot cover: the engine knows it failed and cannot
// write down that it failed. The intent record is already durable by then, and
// it is what keeps the vault refused afterwards — including across a restart,
// with a spotless working tree, and with no marker on disk at all.
func TestSimultaneousCommitAndMarkerFailureLeavesADurableIntent(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()
	safe := h.head()

	dir := h.safetyDir()
	// The shim takes write permission away from the state directory in the same
	// breath as it fails the commit, so the marker write that follows cannot
	// create its temp file. The intent record was written before any of this and
	// is unaffected.
	shim := gitShim(t, `
if [ "$1" = "commit-tree" ]; then
  chmod 0500 `+shellQuote(dir)+`
  echo "simulated object write failure" >&2
  exit 128
fi`)
	broken := h.open(Options{GitExecutable: shim})
	h.advance(time.Minute)
	h.write("Inbox/note.md", "edited "+secretBody)

	_, err := broken.CommitPending(context.Background())
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPending = %v, want a commit failure", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved despite a failed commit")
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("restore state directory: %v", err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("this test is meaningless unless the marker write actually failed")
	}

	record, present := broken.Interrupted()
	if !present {
		t.Fatal("the durable intent did not survive the failure that could not be marked")
	}
	if record.Phase != IntentPhasePlanned || record.NewOID != "" {
		t.Fatalf("intent phase = %q new_oid = %q, want a planned record with no commit", record.Phase, record.NewOID)
	}
	if record.ID == "" || record.Ref != "refs/heads/main" || record.OldOID != safe {
		t.Fatalf("intent record is missing its identity: %+v", record)
	}
	if record.ObservedAt.IsZero() || len(record.Recovery) == 0 {
		t.Fatalf("intent record is missing its observation time or recovery steps: %+v", record)
	}
	if record.PathCount != 1 || !contains(record.Paths, "Inbox/note.md") {
		t.Fatalf("intent record paths = %v (count %d)", record.Paths, record.PathCount)
	}
	if body := string(h.intentBytes()); strings.Contains(body, strings.TrimSpace(secretBody)) {
		t.Fatalf("the intent record leaked note content:\n%s", body)
	}

	// Put the vault back exactly as the tip holds it. From here on, nothing about
	// the working tree explains a refusal — only the record does.
	h.write("Inbox/note.md", secretBody)
	h.requireCleanWorkTree()

	restarted := h.open(Options{})
	if _, stillPresent := restarted.Interrupted(); !stillPresent {
		t.Fatal("opening the vault retired a record it could not prove had landed")
	}
	preflightErr := restarted.Preflight(context.Background())
	if !IsBlocked(preflightErr) {
		t.Fatalf("Preflight after an unproven snapshot = %v, want a blocked failure", preflightErr)
	}
	if strings.Contains(preflightErr.Error(), strings.TrimSpace(secretBody)) {
		t.Fatalf("the refusal leaked note content: %v", preflightErr)
	}
	if _, err := restarted.CommitPending(context.Background()); !IsBlocked(err) {
		t.Fatalf("CommitPending after an unproven snapshot = %v, want a blocked failure", err)
	}
	if _, blocked := restarted.Blocked(); blocked {
		t.Fatal("the refusal came from a marker, not from the intent record this test is about")
	}

	// Clearing is the person's decision, and it is the same act as clearing a
	// marker.
	if err := restarted.ClearBlock(); err != nil {
		t.Fatalf("ClearBlock: %v", err)
	}
	if _, stillPresent := restarted.Interrupted(); stillPresent {
		t.Fatal("ClearBlock left the intent record behind")
	}
	if err := restarted.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight after recovery: %v", err)
	}

	h.advance(time.Minute)
	h.write("Inbox/note.md", "edited "+secretBody)
	if !mustCommit(t, restarted).Committed {
		t.Fatal("the vault did not recover after the record was cleared")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "edited "+strings.TrimSpace(secretBody) {
		t.Fatalf("history holds %q after recovery", got)
	}
}

// A snapshot that cannot record its intent must not be attempted at all. The
// failure is not a block: nothing was written, nothing is in an unknown state,
// and the next mutation is free to retry.
func TestIntentCreationFailurePreventsTheCommitAttempt(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()
	safe := h.head()

	dir := h.safetyDir()
	// `status` is the last thing the engine runs before it stages anything, so
	// revoking write permission there lands squarely between a healthy scan and
	// the intent write.
	shim := gitShim(t, `
if [ "$1" = "status" ]; then
  chmod 0500 `+shellQuote(dir)+`
fi`)
	broken := h.open(Options{GitExecutable: shim})
	h.advance(time.Minute)
	h.write("Inbox/note.md", "edited "+secretBody)

	_, err := broken.CommitPending(context.Background())
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("CommitPending = %v, want a repository-state refusal", err)
	}
	if Blocks(err) {
		t.Fatalf("a snapshot that never started must not latch: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("restore state directory: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved despite a snapshot that never started")
	}
	if _, present := broken.Interrupted(); present {
		t.Fatal("a record that could not be written is somehow present")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a snapshot that never started latched a block")
	}

	healthy := h.open(Options{})
	if !mustCommit(t, healthy).Committed {
		t.Fatal("the next snapshot did not pick up the change")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "edited "+strings.TrimSpace(secretBody) {
		t.Fatalf("history holds %q", got)
	}
}

// Recovery retires a record by proving the commit reached the branch, and
// reachability is the right test: a later commit by another writer does not
// un-land the snapshot underneath it.
func TestRecoveryRetiresAnIntentWhoseCommitLanded(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()
	landed := h.head()

	// A later writer moves the branch on, so the recorded commit is an ancestor
	// of the tip rather than the tip itself.
	rawGit(t, h.root, "commit", "--allow-empty", "-q", "-m", "someone else")
	if h.head() == landed {
		t.Fatal("the branch did not advance")
	}

	saveIntent(t, h, IntentRecord{
		Operation: "commit-pending",
		ID:        "0123456789abcdef",
		Phase:     IntentPhaseCommitted,
		Ref:       "refs/heads/main",
		NewOID:    landed,
		Paths:     []string{"Inbox/note.md"},
		PathCount: 1,
	})

	restarted := h.open(Options{})
	if _, present := restarted.Interrupted(); present {
		t.Fatal("opening the vault did not retire a record whose commit is in the history")
	}
	if got := restarted.WatchStatus().LastCommitAt; !got.Equal(h.clock) {
		t.Fatalf("recovery without a stored landing bound anchored at %s, want conservative observation %s", got, h.clock)
	}
	if err := restarted.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight after a proven snapshot: %v", err)
	}
}
