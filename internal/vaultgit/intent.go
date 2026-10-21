package vaultgit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/agent"
)

// The recovery-intent protocol.
//
// Every other failure this package handles is one it observed and classified,
// so it can write blocked.json and explain itself. A process that is killed
// mid-snapshot observes nothing and explains nothing, and the state it leaves
// behind is indistinguishable — from the outside — from a vault where no
// snapshot was ever attempted. The intent record removes that ambiguity: it is
// written and fsynced before the snapshot starts and removed only after the
// result is durable, so its presence at startup means exactly "an operation
// began here and never finished".
//
// The protocol has three rules and they are all one-directional:
//
//  1. No snapshot is attempted without a durable intent. A failure to write one
//     stops the operation before the first index write, because a snapshot whose
//     outcome could not be recorded is one nothing can reason about afterwards.
//  2. The record is upgraded to IntentPhaseCommitted, carrying the new commit's
//     object name, and fsynced *before* update-ref is attempted. This is the
//     load-bearing ordering: it is what makes the object name available to
//     recovery in the only window where the ref might move without this process
//     surviving to see it.
//  3. A surviving record blocks mutation until recovery proves the snapshot
//     landed, or a person clears it with Manager.ClearBlock.
//
// Rule 3 is the strict one and it is strict on purpose. Recovery proves landing
// by asking Git whether the recorded commit is reachable from the branch; there
// is no second way to prove it, and a record that cannot be proved is a record
// that might be describing content the history does not hold. The cost of being
// wrong in the safe direction is that a person runs one command; the cost of
// being wrong in the other direction is a note that exists in no history and
// that nothing will ever notice is missing.

// newOperationID mints an identifier for one snapshot attempt, so an intent
// record and the block marker a later failure writes can be tied together in a
// log without either of them carrying anything about the notes involved.
func newOperationID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// beginIntent records that a snapshot is about to be attempted.
//
// It runs before the first index write rather than before the first *object*
// write, so the record brackets the whole attempt. Staging writes loose blobs
// into `.git/objects`, and while those are unreferenced and harmless, bracketing
// from the first mutation of any kind is one fewer boundary to reason about.
func (m *Manager) beginIntent(op, ref, head string, plan *commitPlan) (IntentRecord, error) {
	id, err := newOperationID()
	if err != nil {
		return IntentRecord{}, safetyErrorf(KindRepositoryState, op,
			"a snapshot identifier could not be generated, so no snapshot was attempted: %s", agent.SanitizedDiagnostic(err))
	}
	paths := make([]string, 0, len(plan.add)+len(plan.remove))
	paths = append(paths, plan.add...)
	paths = append(paths, plan.remove...)
	sort.Strings(paths)

	record := IntentRecord{
		Operation:  op,
		ID:         id,
		Phase:      IntentPhasePlanned,
		ObservedAt: m.now(),
		Ref:        ref,
		OldOID:     head,
		Paths:      paths,
		PathCount:  len(paths),
		Message:    plan.message,
		Recovery:   intentRecovery(IntentPhasePlanned, m.state.path(intentFileName)),
	}
	if err := m.state.SaveIntent(record); err != nil {
		// Deliberately not a blocking kind. Nothing has been written to the
		// repository, nothing is in an unknown state, and the next mutation may
		// retry — the write simply refused to start, which is the whole point of
		// doing it first.
		return IntentRecord{}, safetyErrorf(KindRepositoryState, op,
			"the snapshot intent could not be recorded, so no snapshot was attempted: %s", agent.SanitizedDiagnostic(err))
	}
	return record, nil
}

// promoteIntent rewrites the record with the commit that is about to be landed.
//
// This is the step that makes recovery possible at all, so its failure stops the
// operation before update-ref: landing a commit whose object name is written
// nowhere durable would produce precisely the state this protocol exists to
// prevent — a ref that may or may not have moved, and no way to find out which.
func (m *Manager) promoteIntent(op string, record IntentRecord, commit string) (IntentRecord, error) {
	record.Phase = IntentPhaseCommitted
	record.NewOID = commit
	record.ObservedAt = m.now()
	// The stored procedure is rewritten with the phase, not left as the planned
	// one: from here on there is an object to prove reachable, and the planned
	// text says there is not.
	record.Recovery = intentRecovery(IntentPhaseCommitted, m.state.path(intentFileName))
	if err := m.state.SaveIntent(record); err != nil {
		return IntentRecord{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the snapshot could not be recorded as in progress, so the branch was not advanced",
			OldOID: record.OldOID,
			NewOID: commit,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}
	return record, nil
}

// clearIntent retires the record after a durable outcome, or after a verdict
// that replaces it. On the success path its failure is ignored rather than
// returned: a snapshot that landed has landed, and reporting it as a failure
// would send the caller looking for content that is safely in the history. The
// cost of a record that outlives its operation is one recovery pass that proves
// the commit landed and removes it.
func (m *Manager) clearIntent() error { return m.state.ClearIntent() }

// recoverIntentLocked resolves a surviving intent record. The safety lock must
// already be held, because a record belonging to a live operation in another
// process would otherwise be read as an abandoned one.
//
// The only exit that clears the record is a positive proof of landing:
// reachability of the recorded commit from the branch tip. Reachability rather
// than equality is required — another writer committing on top does not un-land
// the snapshot underneath, and demanding equality would block every vault whose
// watcher committed once more before the recovery pass ran.
func (m *Manager) recoverIntentLocked(ctx context.Context, op string) (bool, error) {
	record, present, loadErr := m.state.LoadIntent()
	if !present {
		return false, nil
	}
	if loadErr == nil && record.NewOID != "" && record.Ref != "" {
		if landed, err := m.intentLanded(ctx, record); err == nil && landed {
			return m.retireLandedIntent(op, record)
		}
	}
	return false, &SafetyError{
		Kind:   KindBlocked,
		Op:     op,
		Reason: intentBlockReason(record),
		Paths:  record.Paths,
		OldOID: record.OldOID,
		NewOID: record.NewOID,
		Err:    loadErr,
	}
}

// retireLandedIntent finishes a record whose commit is proven reachable. The
// ref's directory is synced again first, because a record can survive exactly
// because that sync failed in the process that landed it: reachability proves
// the ref moved, not that the move survives power loss. While the sync still
// fails the record stays for a later pass. The verdict is unchanged — the
// commit landed, so nothing is blocked — and a new snapshot replaces the record
// with its own, whose landing makes this commit durable as its ancestor.
func (m *Manager) retireLandedIntent(op string, record IntentRecord) (bool, error) {
	durable := m.refDirDurable(record.Ref)
	external := record.Operation == "commit-pending"
	if external {
		// An external intent outlives its ref update until the floor anchor is
		// durable. Prefer the post-ref bound the original process recorded;
		// an older intent has no such field, so the instant recovery observes
		// the landed commit is the conservative replacement.
		landedAt := m.now().UTC()
		if record.LandedAt != nil {
			landedAt = record.LandedAt.UTC()
		}
		if _, noteErr := m.state.NoteCommit(m.policy(), landedAt); noteErr != nil {
			return false, safetyErrorf(KindRepositoryState, op,
				"the recovered external snapshot's floor could not be anchored: %s", agent.SanitizedDiagnostic(noteErr))
		}
	}
	if !durable {
		return external, nil
	}
	if clearErr := m.clearIntent(); clearErr != nil {
		return false, safetyErrorf(KindRepositoryState, op,
			"the finished snapshot's intent record could not be removed: %s", agent.SanitizedDiagnostic(clearErr))
	}
	return external, nil
}

// intentLanded asks the repository whether the recorded commit is in the
// branch's history. Every error answers "not proven", never "no": the caller
// treats an unproven record as blocking, and collapsing "I could not ask" into
// "it is not there" would be the same answer by accident rather than by
// decision.
func (m *Manager) intentLanded(ctx context.Context, record IntentRecord) (bool, error) {
	tip, err := m.git.revParse(ctx, record.Ref)
	if err != nil {
		return false, err
	}
	return m.git.isAncestor(ctx, record.NewOID, tip)
}

// recoverIntentOnOpen runs the recovery pass at startup.
//
// It reads the record before taking anything, so the overwhelmingly common case
// — no interrupted operation — costs one failed open and leaves Open's "no
// mutation, no lock" behavior intact. A busy lock means another process is
// inside the operation this record belongs to, which is the one case where the
// record is not abandoned and must be left alone.
//
// The verdict is discarded. Open's contract is that a vault which cannot be
// mutated still opens, so that a caller can report why; Preflight and the commit
// entry points run the same pass and return its refusal.
func (m *Manager) recoverIntentOnOpen(ctx context.Context) {
	if _, present, _ := m.state.LoadIntent(); !present {
		return
	}
	release, err := m.acquire(safetyLockName, "open", ctx)
	if err != nil {
		return
	}
	defer release()
	_, _ = m.recoverIntentLocked(ctx, "open")
}

// intentBlockReason explains a surviving record in one clause, naming counts and
// object identifiers but never a note's contents.
func intentBlockReason(record IntentRecord) string {
	switch {
	case record.NewOID != "":
		return fmt.Sprintf(
			"a snapshot of %s was interrupted after commit %s was built and it cannot be proven to have reached %s, so mutations stay refused",
			pathCountPhrase(record.PathCount), record.NewOID, refName(record.Ref))
	case record.Phase == IntentPhasePlanned && record.Ref != "":
		return fmt.Sprintf(
			"a snapshot of %s was interrupted before it produced a commit on %s, so mutations stay refused until it is confirmed by hand",
			pathCountPhrase(record.PathCount), refName(record.Ref))
	default:
		return "an interrupted snapshot left a record this build cannot interpret, so mutations stay refused"
	}
}

func pathCountPhrase(count int) string {
	switch count {
	case 0:
		return "the vault"
	case 1:
		return "1 path"
	default:
		return fmt.Sprintf("%d paths", count)
	}
}

// refName trims the ref prefix for a human-readable message. The full ref is
// still in the record.
func refName(ref string) string {
	if short := strings.TrimPrefix(ref, "refs/heads/"); short != "" {
		return short
	}
	return ref
}

// intentRecovery is the manual procedure stored in the record. Like every other
// recovery text in this package it names paths and object identifiers only, so
// the record stays safe to read aloud, mail, or paste into an issue.
//
// It is written per phase because the two phases are recovered differently, and
// telling a person the wrong one wastes the only thing they have: a planned
// record names no commit, so nothing can ever prove it landed and it will never
// self-retire, while a committed record names an object that is already in the
// repository and self-retires the moment that object is reachable. A single text
// covering both would have to promise the self-retirement to a person holding a
// record that cannot get it.
func intentRecovery(phase IntentPhase, path string) []string {
	steps := []string{
		"Nothing was pushed, and no file in the vault was deleted by BrainNotes; the working tree still holds every change.",
	}
	if phase == IntentPhaseCommitted {
		steps = append(steps,
			"This record names a commit that was built before the branch was asked to move, so that commit object is already in the repository.",
			"Run `git -C <vault> merge-base --is-ancestor <new_oid> HEAD` to settle whether it reached the branch; BrainNotes retires this record on its own once it did.",
			"If it did not, `git -C <vault> show --stat <new_oid>` shows what it holds and `git -C <vault> merge --ff-only <new_oid>` moves it forward without rewriting anything.",
		)
	} else {
		steps = append(steps,
			"This record names no commit, which proves the branch was never asked to move: nothing landed, and there is no object to look for.",
			"Nothing will retire this record on its own, because there is nothing it could prove; it is cleared by hand once you have looked.",
			"Run `git -C <vault> status` and `git -C <vault> log --oneline -5` to see what the history is still missing.",
			"Commit anything you recognize by hand, or let the next BrainNotes snapshot pick it up after you clear this record.",
		)
	}
	return append(steps,
		"Once you have looked, remove "+path+" together with any blocked.json beside it while a checkpoint holds the vault, to allow mutation snapshots again: `bn checkpoint run --reason \"clear Git safety block\" -- rm -f <state dir>/blocked.json <state dir>/intent.json` (docs/operations/vault-safety-recovery.md, \"Clearing, in order\").")
}
