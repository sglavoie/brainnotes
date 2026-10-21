package web

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultimport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// TestSyncPartialResolutionErrorSaysWhatIsDurable is the rest of the failure
// answer's contract: a resolution whose decision *was* recorded and something
// after it failed, an earlier promise the recovery pass dropped on the way in, and
// the plain failure that applied nothing at all. Each has to name what is durable,
// keep the engine's own sentence, and be classified rather than called internal.
func TestSyncPartialResolutionErrorSaysWhatIsDurable(t *testing.T) {
	const root = "/vaults/under-test"
	dropped := map[string]error{"pkg-earlier": errors.New("the application it revises never landed")}
	runSyncPartialAnswerCases(t, root, []syncPartialAnswerCase{
		{
			name:       "both commits landed and the peer was never told",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: &vaultimport.Result{}},
			plan:       revisingTestPlan(),
			err:        errors.New("the acknowledgment could not be published"),
			wantCode:   noteops.CodeInternal,
			says: []string{
				"the change was applied and your decision was recorded, and both are undoable",
				"The other replica has not been told, so it will keep offering the change",
			},
			silentOn: []string{"What failed came afterwards"},
		},
		{
			name:       "everything landed and the tail failed",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			// The nested conflict is what makes this an assertion about the
			// *ordering* rather than about one code: *vault.TxnRecoveryError
			// unwraps to the commit failure it compensated for, IsTxnConflict is
			// errors.As, and a rollback failure in production almost always nests
			// one — so with a plain error inside, a syncWriteCode that asked about
			// conflicts first, or one with the rollback case deleted outright,
			// would still pass this case. The *pairing* is this table's own: the
			// engine raises these two predicates from the revision's own
			// txn.Commit, which leaves no journal entry, and the state writes
			// after it are ExpectAnyContent replacements that cannot displace
			// anything — so the entry beside it is here to keep the assertion on
			// the classifier rather than to describe a state production reaches.
			err: &vault.TxnRecoveryError{
				Path:         root + "/Alpha.md",
				RecoveryPath: root + "/.bnotes/tmp/alpha",
				Err: &vault.TxnConflictError{
					Path:     root + "/Alpha.md",
					Expected: "file(sha256=beforebefore)",
					Actual:   "file(sha256=afterafter)",
				},
			},
			// Internal rather than safety, and pinned here because the temptation
			// runs the other way: this is the gravest failure on the surface and
			// CodeSafety is the gravest-sounding code, but CodeSafety is documented
			// as the engine refusing before anything changed, which is the one thing
			// this is not. It is the same answer internal/noteops and the property
			// class give the same two predicates, and what recovers the content is
			// the message naming the file rather than the code.
			wantCode: noteops.CodeInternal,
			says:     []string{"What failed came afterwards", "<vault>/Alpha.md"},
			silentOn: []string{root},
		},
		{
			name: "an earlier decision was dropped on the way in",
			resolution: &vaultimport.Resolution{
				JournalEntry:         "entry-1",
				Applied:              acknowledgedApplication(),
				AbandonedResolutions: dropped,
			},
			plan:     revisingTestPlan(),
			err:      errors.New("the acknowledgment could not be published"),
			wantCode: noteops.CodeInternal,
			says: []string{
				"Dropped on the way in, from an earlier attempt: " +
					"pkg-earlier (the application it revises never landed)",
			},
		},
		{
			name:       "nothing landed at all",
			resolution: nil,
			err:        errStaleTestPreview(),
			wantCode:   noteops.CodePreviewStale,
			says:       []string{"sync: "},
			// With no Resolution there is nothing durable to report, and a message
			// saying a change was applied would be the worst available answer.
			silentOn: []string{"was applied", "undoable"},
		},
	})
}

// TestSyncBlockedVaultIsSafetyOnlyWhenNothingLanded pins the one asymmetry between
// the two answers, from both sides.
//
// A latched vault is the same engine failure either way, and what it means is not:
// asked before anything was applied it is the textbook CodeSafety — the engine
// refused and nothing was written — while the same error beside a Resolution means
// the other replica's version is on disk and the second commit never started. Only
// the empty-handed side may say safety, so asserting one side without the other
// leaves syncPartialResolutionCode deletable with the suite still green.
func TestSyncBlockedVaultIsSafetyOnlyWhenNothingLanded(t *testing.T) {
	const root = "/vaults/under-test"
	blocked := &vaultgit.SafetyError{
		Kind:   vaultgit.KindBlocked,
		Op:     "preflight",
		Reason: "a previous failure recorded blocked.json",
	}
	if got := noteops.CodeOf(syncResolveError(blocked, root)); got != noteops.CodeSafety {
		t.Fatalf("a refusal that applied nothing: code = %q, want %q", got, noteops.CodeSafety)
	}
	partial := syncPartialResolutionError(
		&vaultimport.Resolution{Applied: acknowledgedApplication()}, revisingTestPlan(), blocked, root)
	if got := noteops.CodeOf(partial); got != noteops.CodeInternal {
		t.Fatalf("the same refusal after the application landed: code = %q, want %q",
			got, noteops.CodeInternal)
	}
}

// assertAnswerSaysAndIsSilent holds one answer to both halves of its contract: the
// sentences it owes a person, and the ones that would be false of this outcome.
//
// It is out here rather than inside the table's subtest because what it costs there
// is nesting — a table, a subtest closure and two fragment loops in one function —
// and the two halves are the same assertion whichever failure is being described.
func assertAnswerSaysAndIsSilent(t *testing.T, err error, says, silentOn []string) {
	t.Helper()

	for _, fragment := range says {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("the answer does not say %q: %v", fragment, err)
		}
	}
	for _, fragment := range silentOn {
		if strings.Contains(err.Error(), fragment) {
			t.Fatalf("the answer says %q, which is not true of this outcome: %v", fragment, err)
		}
	}
}

// revisingTestPlan is a plan that has a second commit to make — one step, which is
// all Plan.Revises() asks — so an empty JournalEntry beside it is a decision that
// has not been recorded rather than one there was never anything to record.
func revisingTestPlan() *vaultimport.Plan {
	return &vaultimport.Plan{
		PackageID: "pkg-1",
		Paths: []vaultimport.PathPlan{{
			Path:   "Alpha.md",
			Kind:   vaultimport.ConflictConcurrentEdit,
			Choice: vaultimport.KeepCurrent,
			Steps:  []vaultimport.Step{{Path: "Alpha.md", Action: vaultsync.ActionWrite, SHA256: "sha-local"}},
		}},
	}
}

// errStaleTestPreview is a staleness refusal as the engine raises one, which is
// the failure that comes back with no resolution at all.
func errStaleTestPreview() error {
	return fmt.Errorf("%w: pkg-1 was decided against a version this vault has moved past",
		vaultimport.ErrStalePreview)
}
