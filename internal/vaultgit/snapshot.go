package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// commitPlan is the exact set of index changes one snapshot will make.
type commitPlan struct {
	// at is the instant the commit object is stamped with, in both its author and
	// its committer date. Only an external autosave sets it deliberately, tying
	// the commit subject and object to its write-ahead marker. Explicit mutation
	// plans leave it zero and stageAndCommit chooses the commit-creation reading.
	//
	// That matters for exactly one plan: an external autosave uses the same instant
	// for its marker, generated subject, and commit metadata. It is not the final
	// scheduling boundary; the external floor is measured from the later observed
	// ref landing.
	at     time.Time
	add    []string
	remove []string
	// proofEntries are the journal entry IDs whose records authorize the
	// removals. If a removal is held back, its proof is held back too.
	proofEntries map[string]bool
	message      string
	// externalAdditions marks a plan whose additions were discovered by scanning
	// the working tree rather than named by a caller. Only such a plan may skip
	// an addition it cannot read.
	//
	// The distinction decides whether an unstaged path is tolerable. Nobody asked
	// for the external scan's paths in particular — it offers to record whatever
	// the vault happens to hold — so leaving one out costs nothing the history had
	// a moment ago. Every other entry point is a caller saying "these exact paths
	// are now the truth"; silently recording some of them would answer a question
	// it was not asked, and the caller would report a success the history does
	// not back. See stageAndCommit.
	externalAdditions bool
}

// withCAS builds and lands a commit, rebuilding it from scratch if the branch
// moved underneath.
//
// The rebuild is total on purpose. A lost compare-and-swap means some other
// process committed, so this snapshot's tree was computed against a parent that
// is no longer the tip; reusing it would silently revert whatever that other
// process recorded. Re-seeding the index from the new HEAD and re-planning is
// the only correct response, and it is cheap because the objects already
// written stay written.
func (m *Manager) withCAS(ctx context.Context, op string, build func(head string) (*commitPlan, error)) (CommitResult, error) {
	for attempt := 1; ; attempt++ {
		head, ref, err := m.headState(ctx, op)
		if err != nil {
			return CommitResult{}, err
		}
		if err := m.seedIndex(ctx, head, op); err != nil {
			return CommitResult{}, err
		}
		plan, err := build(head)
		if err != nil {
			return CommitResult{}, err
		}
		if plan == nil {
			return CommitResult{Branch: ref, Parent: head}, nil
		}
		result, err := m.stageAndCommit(ctx, op, head, ref, plan)
		if err == nil {
			return result, nil
		}
		var conflict *casConflict
		if !errors.As(err, &conflict) {
			return CommitResult{}, err
		}
		if attempt >= casAttempts {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindRefConflict,
				Op:     op,
				Reason: fmt.Sprintf("the branch %s kept moving while BrainNotes tried to record a snapshot, so the snapshot was abandoned", ref),
			})
		}
	}
}

// casConflict signals that update-ref refused because the branch moved. It is
// internal to the retry loop and never escapes withCAS.
type casConflict struct{ ref string }

func (c *casConflict) Error() string { return "vaultgit: " + c.ref + " moved during the snapshot" }

// stageAndCommit realizes one plan against the private index.
//
// The whole body is bracketed by the recovery intent: it is written and fsynced
// before the first index write and retired only once the branch has moved and
// the directory naming its ref is durable. Every failure in between either
// latches a block marker — which takes the record's place as the durable
// statement — or leaves the record standing, which is what makes an interrupted
// snapshot distinguishable from one that never started. See intent.go.
func (m *Manager) stageAndCommit(ctx context.Context, op, head, ref string, plan *commitPlan) (CommitResult, error) {
	intent, err := m.beginIntent(op, ref, head, plan)
	if err != nil {
		return CommitResult{}, err
	}

	staged, skipped, stageErr := m.stageAdditions(ctx, plan.add)
	if stageErr != nil {
		// Staging failed for a path the engine can read perfectly well, which
		// makes it a failure of the repository rather than of that path. It would
		// hit everything else in the pass too, so continuing would build a tree
		// missing content the working tree holds — the exact shape of loss this
		// package exists to refuse.
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the vault's content could not be staged, so nothing was committed",
			Err:    stageErr,
		})
	}
	if len(skipped) > 0 && !plan.externalAdditions {
		// The caller named these paths, so leaving one out is not a smaller
		// snapshot, it is a wrong one. Refusing here is what lets the caller say
		// "the change is on disk and the history does not cover it" instead of
		// reporting a success.
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "a path this snapshot was required to record could not be staged, so nothing was committed",
			Paths:  skippedPaths(skipped),
			Err:    skippedError(skipped),
		})
	}
	if !plan.externalAdditions {
		// An exact snapshot's promise is that the paths it names are in the tree
		// it builds *with the content they hold right now*, so it reads the index
		// back rather than trusting an exit status. Asking only whether the path
		// has an entry answers yes for every already-tracked path before anything
		// is staged, because the index is seeded from HEAD — so the comparison is
		// against the required object and mode, which is the only check that
		// survives a `git` reporting success without doing the work.
		wrong, err := m.misstagedAdditions(ctx, staged)
		if err != nil {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "the snapshot could not confirm what it had staged, so nothing was committed",
				Err:    errors.New(agent.SanitizedDiagnostic(err)),
			})
		}
		if len(wrong) > 0 {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "the private index does not hold the content this snapshot was required to record, so nothing was committed",
				Paths:  wrong,
			})
		}
	}

	removals := plan.remove
	if len(skipped) > 0 && len(removals) > 0 {
		// Something in this pass could not be read, and a rename's new side is
		// exactly the kind of thing that would be. Committing the delete side of a
		// pair whose add side was skipped would drop the content from the tip, so
		// every removal waits for a pass where nothing is skipped.
		//
		// The proof has to wait with it. A journal record authorizes a deletion
		// only while it is uncommitted, so committing the record now and the
		// deletion later would leave the later pass with an unexplained deletion —
		// the engine would block the vault over a removal it had already approved.
		// Unstaging the proving entry's files keeps the two together, which is the
		// same invariant that makes the proof meaningful in the first place.
		removals = nil
		var held []string
		for _, path := range staged {
			if id, _ := journalEntryID(path); id != "" && plan.proofEntries[id] {
				held = append(held, path)
			}
		}
		if len(held) > 0 {
			if err := m.git.stageRemove(ctx, m.indexPath, held); err != nil {
				return CommitResult{}, m.latch(&SafetyError{
					Kind:   KindCommitFailed,
					Op:     op,
					Reason: "a deferred deletion's journal record could not be held back, so nothing was committed",
					Err:    errors.New(agent.SanitizedDiagnostic(err)),
				})
			}
			staged = withoutPaths(staged, held)
		}
	}
	if err := m.git.stageRemove(ctx, m.indexPath, removals); err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "a proven deletion could not be staged, so nothing was committed",
			Paths:  removals,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}
	if !plan.externalAdditions && len(removals) > 0 {
		// The same readback for the removal half of an exact plan, and for the same
		// reason: the index is seeded from HEAD, so a `--force-remove` that reported
		// success without dropping the entry leaves the tip's copy in the tree. A
		// plan that removes without adding then writes a tree identical to HEAD and
		// returns a nil success naming deletions the history never recorded.
		remaining, err := m.unremovedDeletions(ctx, removals)
		if err != nil {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "the snapshot could not confirm the removals it had staged, so nothing was committed",
				Err:    errors.New(agent.SanitizedDiagnostic(err)),
			})
		}
		if len(remaining) > 0 {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "a path this snapshot was required to remove is still in the index, so nothing was committed",
				Paths:  remaining,
			})
		}
	}

	result := CommitResult{
		Branch:  ref,
		Parent:  head,
		Message: plan.message,
		Added:   staged,
		Deleted: removals,
		Skipped: skipped,
	}
	if len(skipped) > 0 {
		result.DeferredDeletions = plan.remove
	}
	if len(staged) == 0 && len(removals) == 0 {
		// Nothing to record and so nothing to recover: this pass ends before the
		// intent does any work, and clearing it keeps a held-back snapshot from
		// leaving a record that outlives the reason for it.
		_ = m.clearIntent()
		return result, nil
	}

	tree, err := m.git.writeTree(ctx, m.indexPath)
	if err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the snapshot tree could not be written, so nothing was committed",
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}
	// An identical tree means the plan's paths already match the tip. Committing
	// it would add an empty commit to a history a person has to read.
	if head != "" {
		if headTree, err := m.git.revParse(ctx, head+"^{tree}"); err == nil && headTree == tree {
			_ = m.clearIntent()
			return result, nil
		}
	}

	commitAt := plan.at
	if commitAt.IsZero() {
		// Explicit mutations retain the pre-floor behavior: their visible author
		// and committer dates are chosen immediately before commit creation, after
		// intent creation, staging, and tree writing have completed.
		commitAt = m.now()
	}
	commit, err := m.git.commitTree(ctx, tree, head, plan.message, m.who, commitAt)
	if err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the snapshot commit could not be written, so nothing was recorded",
			OldOID: head,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}

	// The commit object exists but nothing points at it yet. Recording its name
	// now is what lets a restart after the next line ask the only question that
	// settles the outcome: is this commit in the branch's history?
	intent, err = m.promoteIntent(op, intent, commit)
	if err != nil {
		return CommitResult{}, err
	}

	if err := m.git.updateRefCAS(ctx, ref, commit, head); err != nil {
		// Distinguish "someone else got there first" from "this repository is
		// broken" by asking where the branch is now. Only the second latches.
		if current, resolveErr := m.git.revParse(ctx, ref); resolveErr != nil || current != head {
			// A branch that provably moved to something other than head means this
			// update never applied, so the attempt is over and withCAS rebuilds from
			// the new tip with a fresh record. When the branch could not be read at
			// all, nothing is proven and the record stays.
			if resolveErr == nil {
				_ = m.clearIntent()
			}
			return CommitResult{}, &casConflict{ref: ref}
		}
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: fmt.Sprintf("the branch %s could not be advanced to the new snapshot", ref),
			OldOID: head,
			NewOID: commit,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}

	// The branch has moved. Git wrote the ref file, but the directory entry that
	// names it is what makes the move survive power loss, and "the ref landing is
	// durable" is exactly the condition under which the record may be retired.
	// Best effort on purpose: the commit has landed either way, and turning a
	// successful snapshot into a failure over a housekeeping fsync would be the
	// destructive answer to the harmless problem. A packed ref has no such
	// directory, and the failed sync is ignored for the same reason.
	_ = vault.SyncDir(filepath.Dir(filepath.Join(m.gitDir, filepath.FromSlash(ref))))
	result.landedAt = m.now().UTC()
	if plan.externalAdditions {
		// The ref is now observably durable. Persist that upper bound in the intent
		// before watch.json is asked to replace the write-ahead marker. If this
		// rewrite fails, the earlier committed-phase intent remains and recovery
		// conservatively anchors at the later instant when it proves landing.
		intent.LandedAt = &result.landedAt
		intent.ObservedAt = result.landedAt
		_ = m.state.SaveIntent(intent)
	} else {
		// Explicit commits have no external floor to complete, so their durable ref
		// update remains the point at which the recovery intent can be retired.
		_ = m.clearIntent()
	}

	result.Committed = true
	result.Commit = commit
	m.refreshUserIndex(ctx, head, commit, staged, removals)
	return result, nil
}

// withoutPaths returns list with every member of drop removed, preserving order.
func withoutPaths(list, drop []string) []string {
	dropped := make(map[string]bool, len(drop))
	for _, path := range drop {
		dropped[path] = true
	}
	out := list[:0:0]
	for _, path := range list {
		if !dropped[path] {
			out = append(out, path)
		}
	}
	return out
}
