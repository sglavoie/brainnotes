package vaultgit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mirror image: a commit that exists as an object but that nothing points at
// is a snapshot that never landed, and it keeps the vault refused.
func TestRecoveryRefusesAnIntentWhoseCommitNeverLanded(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()
	tip := h.head()

	// A real commit object on a real tree, reachable from no ref — exactly what a
	// process killed between commit-tree and update-ref leaves behind.
	orphan := rawGit(t, h.root, "commit-tree", tip+"^{tree}", "-m", "orphan")

	saveIntent(t, h, IntentRecord{
		Operation: "commit-pending",
		ID:        "fedcba9876543210",
		Phase:     IntentPhaseCommitted,
		Ref:       "refs/heads/main",
		OldOID:    tip,
		NewOID:    orphan,
		Paths:     []string{"Inbox/note.md"},
		PathCount: 1,
	})
	h.requireCleanWorkTree()

	restarted := h.open(Options{})
	if _, present := restarted.Interrupted(); !present {
		t.Fatal("a record that cannot be proven landed was retired anyway")
	}
	err := restarted.Preflight(context.Background())
	if !IsBlocked(err) {
		t.Fatalf("Preflight = %v, want a blocked failure", err)
	}
	if !strings.Contains(err.Error(), orphan) {
		t.Fatalf("the refusal must name the commit a person has to recover: %v", err)
	}
	if _, blocked := restarted.Blocked(); blocked {
		t.Fatal("the refusal must come from the record, not from a marker")
	}
	if h.head() != tip {
		t.Fatal("the branch moved during a recovery pass")
	}
}

// A record this build cannot read is a record it cannot use to prove anything,
// so it refuses — the same fail-closed reading the block marker gets, and for a
// sharper reason: proving a snapshot landed requires the object name the
// unreadable bytes were supposed to hold.
func TestUnreadableIntentRecordRefusesMutations(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"truncated", "{ \"version\": 1, \"phase\""},
		{"unknown version", `{"version":999,"phase":"committed","ref":"refs/heads/main"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.write("Inbox/note.md", secretBody)
			h.commitPending()
			tip := h.head()

			if err := h.mgr.state.ensureDir(); err != nil {
				t.Fatalf("ensureDir: %v", err)
			}
			if err := os.WriteFile(h.mgr.state.path(intentFileName), []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write record: %v", err)
			}

			restarted := h.open(Options{})
			if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
				t.Fatalf("Preflight = %v, want a blocked failure", err)
			}
			if _, err := restarted.CommitPending(context.Background()); !IsBlocked(err) {
				t.Fatalf("CommitPending = %v, want a blocked failure", err)
			}
			if _, present := restarted.Interrupted(); !present {
				t.Fatal("an unreadable record was retired")
			}
			if h.head() != tip {
				t.Fatal("the branch moved while the vault was refused")
			}
		})
	}
}

// A record belonging to a live operation in another process is not abandoned,
// and Open must leave it alone rather than adjudicate it.
func TestOpenLeavesALiveOperationsIntentAlone(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()

	saveIntent(t, h, IntentRecord{
		Operation: "commit-pending",
		ID:        "aaaabbbbccccdddd",
		Phase:     IntentPhasePlanned,
		Ref:       "refs/heads/main",
		OldOID:    h.head(),
	})

	// Hold the safety lock the way a live operation would.
	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	other := h.open(Options{})
	if _, present := other.Interrupted(); !present {
		t.Fatal("Open touched a record another process was still using")
	}
	if err := other.Preflight(context.Background()); !IsBusy(err) {
		t.Fatalf("Preflight against a held lock = %v, want a busy failure", err)
	}
}

// An ordinary successful snapshot leaves nothing behind, so the recovery pass on
// the next open is a no-op and costs one failed file open.
func TestSuccessfulSnapshotLeavesNoIntent(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	if !h.commitPending().Committed {
		t.Fatal("nothing was committed")
	}
	if _, present := h.mgr.Interrupted(); present {
		t.Fatal("a completed snapshot left an intent record behind")
	}
	if _, err := os.Lstat(h.mgr.state.path(intentFileName)); !os.IsNotExist(err) {
		t.Fatalf("Lstat intent record = %v, want it absent", err)
	}

	// A snapshot with nothing to record must not leave one either.
	if h.commitPending().Committed {
		t.Fatal("a clean vault produced a commit")
	}
	if _, present := h.mgr.Interrupted(); present {
		t.Fatal("a no-op snapshot left an intent record behind")
	}
}

// The state directory lives inside .git, so nothing it holds can become a
// pending change the engine would then try to commit.
func TestIntentRecordIsInvisibleToGit(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()

	saveIntent(t, h, IntentRecord{
		Operation: "commit-pending",
		ID:        "1111222233334444",
		Phase:     IntentPhasePlanned,
		Ref:       "refs/heads/main",
	})
	h.requireCleanWorkTree()
	// Compared against the git directory the engine resolved rather than against
	// the harness root: on macOS the two spell the same directory differently.
	if !strings.HasPrefix(h.mgr.state.path(intentFileName), h.mgr.gitDir+string(filepath.Separator)) {
		t.Fatalf("the intent record is not inside %s: %s", h.mgr.gitDir, h.mgr.state.path(intentFileName))
	}
}

// The record carries the procedure for recovering it, and the two phases are
// recovered by different means. A planned record names no commit, so nothing can
// ever prove it landed and it will never retire itself: it is adjudicated by
// hand against the working tree. A committed record names an object that is
// already in the repository, so there is a reachability question to ask and the
// engine answers it on its own. Handing a person the wrong one of those texts
// promises a self-retirement they cannot get, or sends them looking for a commit
// that was never built.
func TestIntentRecoveryStepsAreWrittenForThePhase(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", secretBody)
	h.commitPending()
	head := h.head()

	mgr := h.open(Options{})
	plan := &commitPlan{add: []string{"Inbox/note.md"}, message: "record 1 path"}
	planned, err := mgr.beginIntent("commit-pending", "refs/heads/main", head, plan)
	if err != nil {
		t.Fatalf("beginIntent: %v", err)
	}

	stored, present, err := mgr.state.LoadIntent()
	if err != nil || !present {
		t.Fatalf("LoadIntent after beginIntent = present %v, err %v", present, err)
	}
	stepsAre(t, "planned", stored,
		[]string{"names no commit", "Nothing will retire this record on its own"},
		[]string{"merge-base", "--ff-only"})

	if _, err := mgr.promoteIntent("commit-pending", planned, head); err != nil {
		t.Fatalf("promoteIntent: %v", err)
	}
	stored, present, err = mgr.state.LoadIntent()
	if err != nil || !present {
		t.Fatalf("LoadIntent after promoteIntent = present %v, err %v", present, err)
	}
	if stored.Phase != IntentPhaseCommitted || stored.NewOID != head {
		t.Fatalf("promoted record = phase %q new_oid %q", stored.Phase, stored.NewOID)
	}
	// The promoted record must not still be carrying the planned text: from here
	// on there is an object to prove reachable, and the planned text says there is
	// not.
	stepsAre(t, "committed", stored,
		[]string{"merge-base --is-ancestor", "retires this record on its own", "--ff-only"},
		[]string{"names no commit", "Nothing will retire this record on its own"})

	if body := string(h.intentBytes()); strings.Contains(body, strings.TrimSpace(secretBody)) {
		t.Fatalf("the intent record leaked note content:\n%s", body)
	}
	if err := mgr.clearIntent(); err != nil {
		t.Fatalf("clearIntent: %v", err)
	}
}

// stepsAre asserts the stored recovery text says the things this phase's reader
// needs and none of the things that belong to the other phase.
func stepsAre(t *testing.T, phase string, record IntentRecord, want, unwanted []string) {
	t.Helper()
	steps := strings.Join(record.Recovery, "\n")
	if steps == "" {
		t.Fatalf("the %s record carries no recovery steps", phase)
	}
	for _, fragment := range want {
		if !strings.Contains(steps, fragment) {
			t.Fatalf("the %s recovery steps do not mention %q:\n%s", phase, fragment, steps)
		}
	}
	for _, fragment := range unwanted {
		if strings.Contains(steps, fragment) {
			t.Fatalf("the %s recovery steps carry the other phase's %q:\n%s", phase, fragment, steps)
		}
	}
}

// saveIntent writes a record straight to disk, standing in for the durable
// record a process that died mid-snapshot would have left.
func saveIntent(t *testing.T, h *testHarness, record IntentRecord) {
	t.Helper()
	if record.ObservedAt.IsZero() {
		record.ObservedAt = h.clock
	}
	if record.Recovery == nil {
		record.Recovery = intentRecovery(record.Phase, h.mgr.state.path(intentFileName))
	}
	if err := h.mgr.state.SaveIntent(record); err != nil {
		t.Fatalf("SaveIntent: %v", err)
	}
	// Round-trip it, so a test that hand-builds a record still proves the record
	// survives the encoding the engine actually uses.
	var stored IntentRecord
	if err := json.Unmarshal(h.intentBytes(), &stored); err != nil {
		t.Fatalf("the record this test wrote does not decode: %v", err)
	}
}
