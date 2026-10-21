package web

import (
	"errors"
	"strings"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultimport"
)

// syncPartialResolutionError is the honest answer to a resolution that failed.
//
// A non-nil Resolution beside the error says something landed, and there are two
// such states rather than one — Resolve returns the resolution it built for both
// the application's own tail failure and the revision's — so the answer reads
// Resolution.JournalEntry to tell them apart. A revision that never committed is
// the state where this vault holds a version somebody decided against, and it says
// so. A revision that did commit is a vault that holds exactly what was asked for,
// and telling that person "the resolution failed" would send them looking for
// damage there is none of; what failed is afterwards, and the usual afterwards is
// the acknowledgment the peer is owed.
//
// The engine's own sentence is kept in either case, because what failed is not
// derivable from either state, and the code is the same syncResolutionCode the
// ordinary refusal uses: a sentinel means the same thing to a client whether or not
// a resolution came back beside it, and a refused decision in particular is a
// conflict either way.
func syncPartialResolutionError(
	resolution *vaultimport.Resolution,
	plan *vaultimport.Plan,
	err error,
	root string,
) error {
	if resolution == nil {
		return syncResolveError(err, root)
	}
	message := "sync: " + syncPartialOutcome(resolution, plan, err) + " What failed: " +
		syncFailureSentence(err, root) +
		syncAbandonedClause(resolution.AbandonedResolutions, root)
	return noteops.WrapError(syncPartialResolutionCode(err), message, err)
}

// syncPartialResolutionCode is syncResolutionCode with the one answer a partial
// outcome cannot give.
//
// CodeSafety is documented as the engine refusing the operation *before it changed
// anything*, and a resolution that came back at all applied the other replica's
// version — so on this path that code is false whatever raised it. It is reachable:
// the revision's own noteops.BeginOperation preflight runs after the application
// committed, and a vault another process latched in between refuses it there as a
// blocked safety failure rather than as a snapshot one.
//
// Everything else stays shared, which is the point of syncResolutionCode: this
// narrows one code whose meaning is a claim about the vault rather than about the
// decision, and CodeInternal is what the other half-applied failure on this surface
// answers for the same reason — the code says how much a client may assume, and what
// gets a person to the repair is syncPartialOutcome's sentence.
func syncPartialResolutionCode(err error) noteops.ErrorCode {
	if code := syncResolutionCode(err); code != noteops.CodeSafety {
		return code
	}
	return noteops.CodeInternal
}

// syncPartialOutcome is what is durable, for a resolution that came back beside an
// error. It is a sentence about this vault and never about what to do, which is
// syncPartialResolutionError's business.
//
// An empty JournalEntry is five states rather than one — six answers, because one of
// the five is answered two ways — and only the plan and the failure together say
// which. A plan that revises nothing has no second commit to
// make — accepting the other replica's version is carried out by applying it — so
// there is nothing outstanding and no promise anybody is keeping. A plan that does
// revise something and got no entry has a durable promise the next pass finishes,
// unless the revision failed on one of the three sentinels that abandon it, in
// which case no pass will ever make that commit and this vault is left holding the
// version somebody decided against. Telling those apart is the difference between a
// person waiting for a commit that is coming, waiting for one that is not, and
// waiting for one that was never owed. The promise that survives is the state with
// two answers, and what splits it is the failure rather than the plan: a vault that
// latched a block refuses the very write the next pass would make, so the promise is
// outstanding there and coming to nothing until somebody clears it. That is a second
// sentence for one state, not a sixth state — the vault is holding exactly what it
// holds for the promise that is coming.
//
// Two failures are neither, and both are asked first because they are answers about
// the vault that no journal entry is ever written for. A snapshot failure means the
// decision's bytes and its undo manifest are both durable (noteops.Finalize writes
// the manifest before it snapshots) and the content history is not — and whether it
// latched the block is the error's own answer rather than a property of the type:
// noteops fills Blocked in from vaultgit.Blocks, which is false for a snapshot a
// lock somebody else was holding merely refused.
// The two need opposite sentences. A latched block refuses the next write, so the
// promise the state file still holds cannot be kept and "it will be finished
// automatically" is exactly wrong; an unlatched one leaves a finalized entry the
// next pass re-snapshots — revision.commit's own first case — so saying this vault
// refuses further changes would send somebody to clear a block that is not there.
// The unlatched sentence still owes the stall, because an unlatched kind is not
// necessarily a transient one: a busy lock clears itself, a repository mid-rebase
// and a git nobody can run do not, and finishResolutions runs ahead of every
// import, drain and resolution, so until that snapshot succeeds this vault applies
// nothing at all.
// A stranded import means a half-made revision could not be taken back out, and that
// is the one refusal that stops every later import until a person looks.
func syncPartialOutcome(
	resolution *vaultimport.Resolution,
	plan *vaultimport.Plan,
	err error,
) string {
	var snapshot *noteops.SnapshotError
	if errors.As(err, &snapshot) {
		if snapshot.Blocked {
			return "your decision is on disk and is undoable, and the content history was not " +
				"updated: this vault refuses further changes until that is cleared, so nothing is " +
				"finished automatically while it stands." + syncPeerClause(resolution)
		}
		return "your decision is on disk and is undoable, and the content history was not updated: " +
			"nothing is blocked, so the next synchronization pass commits it — and until it does, " +
			"this vault applies nothing else." + syncPeerClause(resolution)
	}
	if errors.Is(err, vaultimport.ErrStrandedImport) {
		return "the other replica's version was applied, your decision was left half recorded, and " +
			"this vault could not put that half back: synchronization is stopped until somebody " +
			"resolves it, and no pass will finish it in the meantime."
	}
	// A record with no plan is not a state this surface can produce — a receipt
	// carries the plan it was minted for — and an absent one is read as a promise
	// still outstanding, which is the answer that sends somebody to look rather
	// than the answer that tells them to stop looking.
	if plan != nil && !plan.Revises() {
		return "the change was applied and there was nothing to record on top of it, because what " +
			"it carries is what you chose. It is undoable." + syncPartialTail(resolution)
	}
	if resolution.JournalEntry == "" {
		if vaultimport.IsUnrecordableDecision(err) {
			return "the other replica's version was applied and your decision could not be recorded " +
				"on top of it, and it will not be tried again: this vault is holding the version you " +
				"decided against, and the change is undoable."
		}
		// A latched block is the other way the promise cannot be kept, and it is the
		// snapshot answer above one step earlier: the revision's own preflight is
		// refused, so there is no entry and no snapshot failure to read it off — the
		// application landed and the second commit never started. The promise does
		// survive, which is why this is not an abandonment, and saying a later pass
		// finishes it would still be wrong: the pass is refused the same write, and
		// finishResolutions running ahead of every import means nothing else is
		// applied either while it stands.
		if vaultgit.IsBlocked(err) {
			return "the other replica's version was applied and your decision has not been recorded " +
				"yet: this vault refuses further changes until that is cleared, so no pass records it " +
				"while it stands, and nothing else is applied either." + syncPeerClause(resolution)
		}
		return "the other replica's version was applied and your decision has not been recorded yet. " +
			"It is saved and will be finished automatically on the next synchronization pass."
	}
	return "the change was applied and your decision was recorded, and both are undoable." +
		syncPartialTail(resolution)
}

// syncPartialTail is what is still owed after the commits that did land: the
// acknowledgment the peer has not been given, or nothing this sentence can name.
func syncPartialTail(resolution *vaultimport.Resolution) string {
	if clause := syncPeerClause(resolution); clause != "" {
		return clause
	}
	return " What failed came afterwards."
}

// syncPeerClause is the acknowledgment half of that tail on its own, for the
// answers that already name what failed and must not say it came afterwards.
//
// It is reachable there: resolveLocked attempts the revision even when the
// application's own tail failed, so a receipt that could not be published and a
// snapshot that then failed arrive together as one joined error, and the redelivery
// is the part of it nobody would otherwise be told about.
func syncPeerClause(resolution *vaultimport.Resolution) string {
	if resolution.Applied != nil && resolution.Applied.Receipt == nil {
		return " The other replica has not been told, so it will keep offering the change until it is."
	}
	return ""
}

// syncAbandonedClause carries earlier decisions this call's recovery pass dropped
// into a failure message.
//
// A successful resolution reports them on its view. A failed one has nowhere else
// to put them: the pass has already run, so the state file no longer holds the
// promise it dropped, and the person reading this failure is usually the one whose
// earlier attempt was the promise.
func syncAbandonedClause(abandoned map[string]error, root string) string {
	views := syncAbandonedViewsOf(abandoned, root)
	if len(views) == 0 {
		return ""
	}
	dropped := make([]string, 0, len(views))
	for _, view := range views {
		dropped = append(dropped, view.PackageID+" ("+view.Reason+")")
	}
	return ". Dropped on the way in, from an earlier attempt: " + strings.Join(dropped, "; ")
}

// syncResolveError maps an engine failure onto the code a client acts on.
//
// The sentinels are asked first and separately from the write failures, because
// they are answers about the *decision* — it was planned against a state this
// vault has moved past, the conflict is gone, another import is running — and a
// client retries or re-reads for those. A write failure is about the vault.
func syncResolveError(err error, root string) error {
	message := "sync: " + syncFailureSentence(err, root)
	return noteops.WrapError(syncResolutionCode(err), message, err)
}

// syncFailureSentence is the engine's own words, with this vault's root taken out
// and a joined failure put on one line.
//
// Both of the engine's answers here can be an errors.Join — an application tail
// failure beside the revision's, and a pass that dropped an earlier promise beside
// whatever it failed on — and Join separates with a newline. These messages are
// read out of a JSON field and put in a sentence on a page, where a raw newline is
// one statement that looks like two, with the second one missing its subject.
func syncFailureSentence(err error, root string) string {
	return strings.ReplaceAll(withoutVaultRoot(err.Error(), root), "\n", "; ")
}

// syncResolutionCode is the one classification both resolution failures use, so a
// sentinel cannot mean one thing when the call came back empty-handed and another
// when a resolution came back beside it. ErrConflict is the reason it has to be one
// function: it is what a client retries or re-reads for, and syncWriteCode alone
// would call a decision the vault refused an internal error.
func syncResolutionCode(err error) noteops.ErrorCode {
	if code, named := syncDecisionCode(err); named {
		return code
	}
	return syncWriteCode(err)
}

func syncDecisionCode(err error) (noteops.ErrorCode, bool) {
	switch {
	case errors.Is(err, vaultimport.ErrStalePreview):
		return noteops.CodePreviewStale, true
	case errors.Is(err, vaultimport.ErrNoConflict), errors.Is(err, vaultimport.ErrImportBusy),
		errors.Is(err, vaultimport.ErrConflict):
		return noteops.CodeConflict, true
	case errors.Is(err, vaultimport.ErrResolutionUnavailable):
		return noteops.CodeInvalidRequest, true
	// Named here rather than left to syncWriteCode so the answer cannot drift onto
	// something nested inside them, and CodeInternal rather than CodeSafety for the
	// reason syncWriteCode gives: a stranded import is a *half-applied* change —
	// the package is on disk, the peer has been told to drop its copy, and the
	// revision is half recorded — which is the negation of what CodeSafety is
	// documented to mean. What gets a person to the repair is the sentence
	// syncPartialOutcome writes, not the code.
	case errors.Is(err, vaultimport.ErrStrandedImport), errors.Is(err, vaultimport.ErrCorruptState):
		return noteops.CodeInternal, true
	}
	return "", false
}

// syncWriteCode classifies the failure of the writes a resolution makes. The
// order is the one internal/vaultimport states: a snapshot failure unwraps to a
// busy Git lock, so it has to be asked about first, and a rollback failure is
// asked before the conflict nested inside it.
//
// A transaction that could not put content back is in that same group and for the
// same reason: it unwraps to the mutation that failed — often a conflict — and
// answering "retry" would bury the one sentence saying which file somebody's
// content is sitting in. internal/noteops and the property class both ask it
// before the conflict too, and both answer it with CodeInternal rather than
// CodeSafety — which this follows, because CodeSafety is documented as the engine
// refusing *before* anything changed, and a rollback that failed is the one state
// where content is provably somewhere nobody asked for it. The distinction that
// matters to a client here is carried by the message, which names the recovery
// file, and by CodeSnapshotFailed for the other post-write failure.
func syncWriteCode(err error) noteops.ErrorCode {
	switch {
	case vaultgit.IsSnapshotFailure(err):
		return noteops.CodeSnapshotFailed
	case vaultgit.IsBlocked(err):
		return noteops.CodeSafety
	case vault.IsTxnRollbackFailure(err), vault.IsTxnRecoveryNeeded(err):
		return noteops.CodeInternal
	case vaultgit.IsBusy(err), vault.IsTxnLockBusy(err), vault.IsTxnConflict(err):
		return noteops.CodeConflict
	case vault.IsForbiddenMutation(err):
		return noteops.CodeForbidden
	}
	return noteops.CodeInternal
}
