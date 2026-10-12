package web

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// acknowledgedApplication is an application whose receipt exists, which is the
// only evidence this vault can have that the peer will stop offering the package.
func acknowledgedApplication() *vaultimport.Result {
	return &vaultimport.Result{Receipt: &vaultsync.Receipt{}}
}

// TestSyncResolvedSummaryClaimsOnlyTheCommitsThereWere drives the success sentence
// over the states a finished resolution has, the defensive one included.
//
// Every half is a claim a person acts on rather than wording. "Your decision was
// recorded" sends somebody to the history to find an entry, and an accepted
// incoming change has none there: the application was the whole of the decision.
// "It needed nothing revised" says the opposite, and saying it over a plan that
// *did* revise something denies the revision this vault is holding. Saying nothing
// about the peer says the peer knows, which an unacknowledged resolution has not
// established.
func TestSyncResolvedSummaryClaimsOnlyTheCommitsThereWere(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resolution *vaultimport.Resolution
		plan       *vaultimport.Plan
		want       string
	}{
		{
			name:       "a decision recorded on top of the application",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			want:       "The change was applied and your decision was recorded.",
		},
		{
			name:       "an acceptance with nothing left to revise",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       &vaultimport.Plan{PackageID: "pkg-1"},
			want: "The change was applied, and it needed nothing revised on top of it: " +
				"what it carries is what you chose.",
		},
		{
			// revision.commit answers ("", nil) when every step of the revision is
			// already satisfied, which is what a re-drive of a promise a crash rolled
			// forward finds. The decision is on disk; there was simply no second commit
			// to make this time, and "it needed nothing revised" would be a lie about a
			// plan that named a step.
			name:       "a revision that was already on disk",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			want: "The change was applied, and the revision your decision asked for was already on " +
				"disk, so no second commit was made: this vault holds what you chose.",
		},
		{
			// Not reachable from the route — a receipt carries the plan it was minted
			// for — and pinned because both of the sentences above are claims about a
			// plan: without one, the only honest answer is what the resolution itself
			// reports.
			name:       "a success whose plan is not there to be read",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       nil,
			want:       "The change was applied.",
		},
		{
			name:       "finished here and unfinished between the replicas",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: &vaultimport.Result{}},
			plan:       revisingTestPlan(),
			want: "The change was applied and your decision was recorded. " +
				"The other replica has not been told yet and will keep offering it until it is.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := syncResolvedViewOf(tc.resolution, tc.plan, "/vaults/under-test")
			if view.Summary != tc.want {
				t.Fatalf("summary = %q, want %q", view.Summary, tc.want)
			}
		})
	}
}

// syncPartialAnswerCase is one failed resolution and the answer it is owed.
type syncPartialAnswerCase struct {
	name       string
	resolution *vaultimport.Resolution
	plan       *vaultimport.Plan
	err        error
	wantCode   noteops.ErrorCode
	says       []string
	silentOn   []string
}

// runSyncPartialAnswerCases holds syncPartialResolutionError to the four halves of
// its contract on every case: the state it names, the engine's own sentence kept
// verbatim, the classification, and the failure still being reachable — the last
// because a caller that logs the cause reads the engine's error and not this one.
func runSyncPartialAnswerCases(t *testing.T, root string, cases []syncPartialAnswerCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := syncPartialResolutionError(tc.resolution, tc.plan, tc.err, root)
			if err == nil {
				t.Fatal("a failed resolution answered with no error")
			}
			if got := noteops.CodeOf(err); got != tc.wantCode {
				t.Fatalf("code = %q, want %q: %v", got, tc.wantCode, err)
			}
			assertAnswerSaysAndIsSilent(t, err, tc.says, tc.silentOn)
			if !errors.Is(err, tc.err) {
				t.Fatalf("the engine's failure is not reachable from the answer: %v", err)
			}
		})
	}
}

// TestSyncPartialResolutionErrorTellsTheUnrecordedStatesApart is the reason the
// plan is an argument at all.
//
// An empty Resolution.JournalEntry is five states, not one, and each is a different
// instruction to the person reading the answer: wait for a commit that is coming,
// stop waiting for one that is not, or nothing was ever owed — the two that no pass
// will ever finish are in the test below, along with the second answer the first of
// these three owes when the vault has latched a block, which is a sentence about one
// of the five rather than a sixth state. Only the plan and the failure together say
// which, so a surface holding the error alone can give most of these answers wrong
// while looking entirely reasonable.
func TestSyncPartialResolutionErrorTellsTheUnrecordedStatesApart(t *testing.T) {
	const root = "/vaults/under-test"
	runSyncPartialAnswerCases(t, root, []syncPartialAnswerCase{
		{
			name:       "the peer's version is here and the decision is not recorded",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err:        errors.New("the revision could not be committed"),
			wantCode:   noteops.CodeInternal,
			says: []string{
				"the other replica's version was applied and your decision has not been recorded yet",
				"will be finished automatically on the next synchronization pass",
				"What failed: the revision could not be committed",
			},
			silentOn: []string{"undoable"},
		},
		{
			name:       "the decision is not recorded and no pass will record it",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			// One of the three sentinels internal/vaultimport abandons a promise on,
			// which is the whole difference from the case above: the next pass drops
			// the promise rather than keeping it, so there is nothing that will ever
			// finish and this vault is sitting on the version the person rejected.
			// The promise is still in the state file at the moment this is answered,
			// which is why the sentence says it will not be tried again rather than
			// that it was dropped.
			err: fmt.Errorf("%w: Alpha.md moved under the revision", vaultimport.ErrConflict),
			// The same sentinel is a conflict when the call came back empty-handed, so
			// it is a conflict here too: both answers go through syncResolutionCode.
			wantCode: noteops.CodeConflict,
			says: []string{
				"your decision could not be recorded on top of it, and it will not be tried again",
				"this vault is holding the version you decided against",
			},
			silentOn: []string{"will be finished automatically"},
		},
		{
			name:       "an acceptance that owed no second commit",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			// A plan that revises nothing is an accept-incoming decision, carried out
			// by the application alone. Reading its empty JournalEntry as an
			// outstanding promise would send somebody to wait for a commit that was
			// never owed, and hide the failure that actually happened.
			plan:     &vaultimport.Plan{PackageID: "pkg-1"},
			err:      errors.New("the acknowledgment could not be published"),
			wantCode: noteops.CodeInternal,
			says: []string{
				"the change was applied and there was nothing to record on top of it",
				"what it carries is what you chose",
				"It is undoable.",
			},
			silentOn: []string{"has not been recorded yet", "will not be tried again"},
		},
	})
}

// TestSyncPartialResolutionErrorAnswersTheFailuresNoEntryIsWrittenFor covers the
// empty-JournalEntry states that the plan cannot explain, because what happened to
// the vault is in the failure rather than in the promise.
//
// Each arrives with a revising plan and no entry, which is exactly the shape of a
// promise the next pass finishes. noteops.Finalize writes the undo manifest before
// it snapshots, so a snapshot failure is a decision that is durable and undoable
// with no content history behind it — and the snapshot cases are here together
// because what separates their answers is the failure's own Blocked field and not
// the type: a latched block refuses the write that would keep the promise, while an
// unlatched one leaves it to the next pass much as an ordinary interruption would.
// The third of them carries an unacknowledged application, because the two failures
// arrive together as one joined error and the peer's redelivery is the half nothing
// else in the message names. A stranded import is the refusal that stops every
// later import until a person resolves it, so the pass that would have finished the
// revision will not run at all.
func TestSyncPartialResolutionErrorAnswersTheFailuresNoEntryIsWrittenFor(t *testing.T) {
	const root = "/vaults/under-test"
	runSyncPartialAnswerCases(t, root, []syncPartialAnswerCase{
		{
			name:       "the content history was not updated and the vault is blocked",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err: &noteops.SnapshotError{
				Operation: syncResolveCommand,
				EntryID:   "entry-1",
				Blocked:   true,
				Err:       errors.New("the branch could not be moved"),
			},
			wantCode: noteops.CodeSnapshotFailed,
			says: []string{
				"your decision is on disk and is undoable",
				"the content history was not updated",
				"refuses further changes until that is cleared",
			},
			// The application was acknowledged, so the tail this answer owes is
			// nothing at all: an answer that had already named the failure and then
			// said it came afterwards would be describing its own sentence. That is
			// what separates syncPeerClause from syncPartialTail, and only an
			// acknowledged case can pin it.
			silentOn: []string{
				"will be finished automatically", "has not been recorded yet",
				"What failed came afterwards",
			},
		},
		{
			// The same failure that did not latch anything, which is a whole snapshot
			// kind rather than an edge: vaultgit.Blocks is false for a refusal by a
			// lock somebody else was holding, which is what this drives (the other
			// unblocked construction — noteops raising one over a snapshot that
			// reported a path left out — is a defensive branch the engine refuses
			// ahead of, for caller-named paths). The promise is still on disk and nothing
			// refuses the write that keeps it, so the next pass re-snapshots the
			// finalized entry — telling this person to go and clear a block would
			// send them looking for a blocked.json that is not there.
			name:       "the content history was not updated and nothing is blocked",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err: &noteops.SnapshotError{
				Operation: syncResolveCommand,
				EntryID:   "entry-1",
				Err:       errors.New("another process is holding the safety lock"),
			},
			wantCode: noteops.CodeSnapshotFailed,
			says: []string{
				"your decision is on disk and is undoable",
				"the content history was not updated",
				"nothing is blocked, so the next synchronization pass commits it",
				// An unlatched kind is not the same fact as a transient one — a
				// repository mid-rebase and a git nobody can run are both unlatched
				// and neither clears itself — and until that snapshot succeeds
				// finishResolutions refuses every import, drain and resolution. The
				// sentence that promised a later pass owes that.
				"until it does, this vault applies nothing else",
			},
			silentOn: []string{
				"refuses further changes", "has not been recorded yet",
				"What failed came afterwards",
			},
		},
		{
			// The same snapshot failure over an application that was never
			// acknowledged, which is reachable as one joined error because
			// resolveLocked attempts the revision even when the application's own
			// tail failed. The redelivery is nothing the failure sentence names, and
			// a person told their decision is durable and the history is behind has
			// no way to guess that the peer is still offering the change.
			name:       "the content history was not updated and the peer was never told",
			resolution: &vaultimport.Resolution{Applied: &vaultimport.Result{}},
			plan:       revisingTestPlan(),
			err: &noteops.SnapshotError{
				Operation: syncResolveCommand,
				EntryID:   "entry-1",
				Blocked:   true,
				Err:       errors.New("the branch could not be moved"),
			},
			wantCode: noteops.CodeSnapshotFailed,
			says: []string{
				"refuses further changes until that is cleared",
				"The other replica has not been told, so it will keep offering the change",
			},
			// The acknowledgment is named as outstanding, never as the thing that
			// failed: what failed is in the engine's own sentence after it.
			silentOn: []string{"What failed came afterwards"},
		},
		{
			// The same answer one step earlier, and the reason the block is asked
			// about under its own name as well as under the snapshot's Blocked field:
			// the revision's noteops.BeginOperation preflight runs after the
			// application committed, so a vault something else latched in between
			// refuses the second commit before it starts. There is no snapshot failure
			// to read the latch off, and the promise survives — so the state looks
			// exactly like a promise the next pass keeps, and is not one.
			name:       "the vault was blocked before the second commit started",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err: &vaultgit.SafetyError{
				Kind:   vaultgit.KindBlocked,
				Op:     "preflight",
				Reason: "a previous failure recorded blocked.json",
			},
			// Internal and deliberately not safety, even though the engine's own
			// answer to this error everywhere else on the surface is safety:
			// CodeSafety promises nothing was written, and the other replica's
			// version is on disk.
			wantCode: noteops.CodeInternal,
			says: []string{
				"the other replica's version was applied and your decision has not been recorded yet",
				"refuses further changes until that is cleared",
				"no pass records it while it stands",
				"nothing else is applied either",
			},
			silentOn: []string{
				"will be finished automatically on the next synchronization pass",
				"What failed came afterwards",
			},
		},
		{
			name:       "the half-made revision could not be taken back out",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err: fmt.Errorf("%w: Alpha.md was edited under the rollback",
				vaultimport.ErrStrandedImport),
			// Internal and deliberately *not* safety, which is the tempting answer
			// for the gravest failure this surface has: CodeSafety is documented as
			// the engine refusing before anything changed, and a stranded import is
			// the opposite of that in every part — the package is applied, the peer
			// has been told to drop its copy, and the revision is half recorded. The
			// code says how much a client may assume; the sentences below are what
			// get somebody to the repair.
			wantCode: noteops.CodeInternal,
			says: []string{
				"your decision was left half recorded",
				"synchronization is stopped until somebody resolves it",
				"no pass will finish it in the meantime",
			},
			silentOn: []string{"will be finished automatically"},
		},
	})
}

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
