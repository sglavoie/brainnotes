package vaultimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// commit writes the revision, and is idempotent in every direction a crash can
// leave it.
//
// Three states are possible when this runs, and the recovery entry is what
// distinguishes them. A finalized entry means the commit already happened and
// only the snapshot may be missing. An entry that was started and not finalized
// is the wreckage of an interrupted commit, which unwind takes back out before
// the entry holding the only copy of what it replaced is discarded. No entry at
// all is the ordinary first attempt. In all three the steps the disk already
// satisfies are skipped, so a re-drive writes what is left rather than what was
// planned.
func (r *revision) commit() (string, error) {
	journalRoot := noteops.JournalRoot(r.root)
	finalized, err := journal.Finalized(journalRoot, r.record.JournalEntry)
	if err != nil {
		return "", err
	}
	if finalized {
		return r.record.JournalEntry, ensureSnapshotContext(r.opts.checkpointContext, r.root, r.record.JournalEntry, resolveToken)
	}
	if err := r.unwind(journalRoot); err != nil {
		return "", err
	}
	pending, err := pendingSteps(r.root, r.record.steps())
	if err != nil {
		return "", err
	}
	if len(pending) == 0 {
		return "", nil
	}
	return r.record.JournalEntry, r.write(pending)
}

// unwind puts back whatever an interrupted revision managed to write, so the
// re-drive plans against the state the decision was made in.
//
// Two facts about internal/vault make this necessary rather than tidy. A
// prepared transaction is rolled *forward* on the next lock acquisition, so a
// path that reads as untouched now may be one lock away from holding the
// revision's content — settleTransactions is what stops the re-drive judging a
// disk that is about to move. And a rolled-forward commit whose operation was
// never finalized leaves content with no undo manifest: re-driving cannot write
// it again, because the path already holds it, and abandoning it would leave
// the decision on the disk, out of the recovery history, out of the content
// history, and — for a step that deletes — with the only journal record that
// authorizes the deletion destroyed, which is what vaultgit latches over.
//
// So the content goes back first and the entry is discarded second, the order
// resolver.undo takes and for the same reason: the entry's snapshots are what
// the content is restored *from*, and removing them first would make a failure
// in between unrecoverable.
func (r *revision) unwind(journalRoot string) error {
	if err := settleTransactions(r.root); err != nil {
		return err
	}
	started, err := journal.Started(journalRoot, r.record.JournalEntry)
	if err != nil {
		return err
	}
	if started {
		if err := r.restore(journalRoot); err != nil {
			return err
		}
	}
	return journal.DiscardUnfinalized(journalRoot, r.record.JournalEntry)
}

// restore takes back the steps that landed, and only those.
//
// A step whose path holds neither what it wrote nor what it expected is left
// alone: somebody edited it after the interrupted commit, and their edit is
// newer than the decision. The re-drive then meets that path's precondition
// under its own lock, is refused, and the resolution is abandoned rather than
// written over the edit — see finishResolution.
//
// A commit refused *here* is deliberately not reported as ErrConflict. That
// sentinel means the vault has permanently moved past the decision, and
// finishResolution abandons the promise on it — which at this point would drop
// the promise, the staged content and the snapshots while leaving the
// partially-landed content and its manifest-less entry directory on the disk
// forever, the exact state this function exists to prevent. The refusal is
// instead a race between the survey above and the transaction's own check under
// the path's lock, and it is transient by construction: the next pass reads the
// disk again, finds the edited path holding neither version, and takes the
// leave-it-alone branch that does end in an abandonment.
func (r *revision) restore(journalRoot string) error {
	landed, err := r.landedSteps()
	if err != nil {
		return err
	}
	committed, err := committedPaths(r.root, removals(landed))
	if err != nil {
		return err
	}
	reverse := pendingReversals(landed, committed)
	if len(reverse) == 0 {
		return nil
	}
	txn, err := vault.NewFileTxn(r.root)
	if err != nil {
		return err
	}
	for _, step := range reverse {
		if err := r.putBack(txn, journalRoot, step); err != nil {
			return err
		}
	}
	if err := txn.Commit(nil); err != nil {
		return fmt.Errorf("vaultimport: unwind the interrupted revision of %s: %w", r.record.PackageID, err)
	}
	return nil
}

// landedSteps is the steps whose path holds exactly what the step wrote.
func (r *revision) landedSteps() ([]Step, error) {
	landed := make([]Step, 0, len(r.record.steps()))
	for _, step := range r.record.steps() {
		state, err := diskState(absPath(r.root, step.Path))
		if err != nil {
			return nil, err
		}
		if state == step.result() {
			landed = append(landed, step)
		}
	}
	return landed, nil
}

// removals is the paths a landed step created, and so the paths taking it back
// out would delete.
func removals(steps []Step) []string {
	paths := make([]string, 0, len(steps))
	for _, step := range steps {
		if !step.Expect.Exists {
			paths = append(paths, step.Path)
		}
	}
	return paths
}

// pendingReversals drops the steps the content history has already taken
// responsibility for. See committedPaths for what that means and why.
func pendingReversals(landed []Step, committed map[string]bool) []Step {
	reverse := make([]Step, 0, len(landed))
	for _, step := range landed {
		if !committed[step.Path] {
			reverse = append(reverse, step)
		}
	}
	return reverse
}

// committedPaths reports which of the named paths the content history already
// holds as a file at its tip.
//
// The unwind has to ask because of one asymmetry in what it takes back. Putting
// content back is a write, and a write needs no authorization. Removing a file
// the revision created is a deletion, and vaultgit commits a deletion only when
// an *uncommitted* journal record proves it was meant — and the revision's
// record proves a creation, not a removal, so it proves nothing here even in the
// window before it is discarded. While the path is untracked that costs nothing:
// Git has never seen it, so there is no deletion to explain. Once it is tracked
// it costs the vault its whole write path, because the preflight refuses *every*
// mutation while a tracked path is missing with nothing to account for it —
// starting with the re-drive on the next line, and going on to every import,
// resolution and note edit until a person intervenes.
//
// Only one thing can have tracked it: the autosave watcher in another process,
// committing an external change it found between the interruption and this pass.
// So a path that watcher adopted is left where it put it, and the re-drive finds
// that step already satisfied. What is lost is the path's place in this
// revision's undo manifest; what is kept is a vault that still accepts writes,
// with the file on the disk and in the content history the watcher wrote. That
// is the better of the two records, and it is the only choice available: there
// is no third state where the file is both gone and explained.
//
// A vault with no content history, or one whose branch has no commits yet, has
// nothing tracked and nothing to refuse — the same two cases snapshotNeeded
// answers for, and for the same reason.
func committedPaths(root string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	ctx := context.Background()
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{})
	if err != nil {
		if vaultgit.IsUnmanaged(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("vaultimport: open content history: %w", err)
	}
	tip, err := history.Tip(ctx)
	if err != nil {
		if errors.Is(err, vaultgit.ErrUnbornBranch) {
			return nil, nil
		}
		return nil, fmt.Errorf("vaultimport: resolve history tip: %w", err)
	}
	kinds, err := history.EntryKinds(ctx, tip, paths)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: look up the revision's paths in the content history: %w", err)
	}
	committed := make(map[string]bool, len(kinds))
	for rel, kind := range kinds {
		if kind == "blob" {
			committed[rel] = true
		}
	}
	return committed, nil
}

// putBack stages one landed step's reversal, pinned to what that step wrote so
// an edit racing the unwind refuses it rather than losing to it.
//
// Both refusals here are ErrStrandedImport, and deliberately not one of the
// sentinels unkeepable abandons on. They are raised *during* the unwind, with
// the revision's content already partly on the disk, so dropping the promise
// would delete the staged bytes and the record of the decision while leaving
// that content and its manifest-less entry directory behind with nothing left
// to bring anyone back for them. ErrStrandedImport is the answer this package
// already has for a rollback it cannot safely make: keep every artifact, mint
// nothing, and refuse every later import until a person has looked. Its
// documentation names this exact case — a journal entry whose snapshot does not
// hash to what was replaced is not the entry the record names, and restoring
// from it would install some other operation's idea of the path.
func (r *revision) putBack(txn *vault.FileTxn, journalRoot string, step Step) error {
	if !step.Expect.Exists {
		return txn.Remove(step.Path, step.landed())
	}
	data, err := journal.ReadSnapshot(journalRoot, r.record.JournalEntry, step.Path)
	if err != nil {
		if gone(err) {
			return fmt.Errorf("%w: entry %s holds no snapshot of %q, which the revision replaced: %v",
				ErrStrandedImport, r.record.JournalEntry, step.Path, err)
		}
		return err
	}
	// The snapshot was taken before the step ran and the step only ran because
	// the path held what the plan pinned, so these two must agree. They
	// disagreeing means this is not the entry the plan names, and writing the
	// bytes anyway would restore some other operation's idea of the path.
	if got := vaultsync.HashBytes(data); got != step.Expect.SHA256 {
		return fmt.Errorf("%w: entry %s holds %s for %q, the revision replaced %s",
			ErrStrandedImport, r.record.JournalEntry, got, step.Path, step.Expect.SHA256)
	}
	return txn.Write(step.Path, data, 0, step.landed())
}

// write is the revision as one journaled, snapshotted operation.
func (r *revision) write(pending []Step) error {
	operation, err := noteops.BeginOperationContext(r.opts.checkpointContext, r.root, resolveToken, r.opts.command(),
		r.targets(pending), r.opts.recorderOptions(r.record.JournalEntry)...)
	if err != nil {
		return err
	}
	defer operation.Release()
	txn, err := r.txn(pending)
	if err != nil {
		return err
	}
	cleanup, refusal := classifyCommit(r.record.PackageID, txn.Commit(operation))
	r.cleanup = cleanup
	if refusal != nil {
		// The entry directory the transaction's first capture created has no undo
		// manifest and never will — journal.ListEntries cannot see it and Prune will
		// never collect it — and unlike an import, a refused revision is not
		// re-driven: the decision it carried is abandoned. So this is the only
		// chance to take it back out, and it is the same in-process resolution a
		// crash here would get on the next pass.
		//
		// The two failures are deliberately not joined. finishResolution decides
		// what to do by asking errors.Is, and a join answers for whichever half
		// matches: a refusal of ErrConflict beside an unwind that returned
		// ErrStrandedImport would read as "abandon", which is precisely the
		// artifact-destroying answer the unwind's sentinel exists to refuse. So an
		// unwind that failed is the error, with the refusal that provoked it
		// carried as its text — it is the more consequential of the two anyway,
		// because a refused decision can be made again and a half-taken-back
		// revision cannot.
		if err := r.unwind(noteops.JournalRoot(r.root)); err != nil {
			return fmt.Errorf("%w (the revision was refused first: %v)", err, refusal)
		}
		return refusal
	}
	importCrash(crashRevised)
	entry, err := operation.Finalize()
	if err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("vaultimport: resolving %s recorded no revision", r.record.PackageID)
	}
	return nil
}

// txn builds the revision's transaction: one write or removal per step, each
// requiring the path to hold what applying the operation left there.
//
// The preconditions are the resolution's second guard and the load-bearing one.
// current refuses a plan the vault has moved past, but it reads without holding
// anything; these are verified by internal/vault under each path's own lock, so
// a note edited between the application and the revision refuses the revision
// with the edit intact rather than writing over it.
//
// The configuration layers are pinned for the same reason an import pins them:
// the declarations decide what the revised content means, and a resolution that
// re-governs the vault halfway through would leave notes behind under rules
// that never applied to them.
func (r *revision) txn(pending []Step) (*vault.FileTxn, error) {
	txn, err := vault.NewFileTxn(r.root)
	if err != nil {
		return nil, err
	}
	written := make(map[string]struct{}, len(pending))
	for _, step := range pending {
		written[vaultsync.CollisionKey(step.Path)] = struct{}{}
		if step.Action == vaultsync.ActionDelete {
			err = txn.Remove(step.Path, step.Expect.expectation())
		} else {
			data, readErr := stagedContent(r.root, r.record.PackageID, step.SHA256)
			if readErr != nil {
				return nil, readErr
			}
			// A zero mode preserves an existing file's permissions and derives a new
			// file's from this machine's umask, for the reason an import does the
			// same: a mode is a fact about the device holding the file.
			err = txn.Write(step.Path, data, 0, step.Expect.expectation())
		}
		if err != nil {
			return nil, fmt.Errorf("vaultimport: revise %q: %w", step.Path, err)
		}
	}
	if err := pinConfiguration(r.root, txn, written); err != nil {
		return nil, err
	}
	return txn, nil
}

// targets are the absolute paths the revision will change, handed to
// BeginOperation so the storage-location policy refuses before any artifact
// exists rather than underneath the first write.
func (r *revision) targets(pending []Step) []string {
	targets := make([]string, 0, len(pending))
	for _, step := range pending {
		targets = append(targets, absPath(r.root, step.Path))
	}
	return targets
}

// pendingSteps drops the steps this disk already satisfies.
//
// It is what makes re-driving an interrupted revision safe, and it is not an
// optimization: a step whose result is already there would be a write of the
// content the path holds, which captures a journal entry and produces a commit
// that says nothing, or a removal of a file that is already gone, which the
// transaction refuses outright. Both would turn a resolution that is nearly
// finished into one that cannot be finished at all.
func pendingSteps(root string, steps []Step) ([]Step, error) {
	pending := make([]Step, 0, len(steps))
	for _, step := range steps {
		state, err := diskState(absPath(root, step.Path))
		if err != nil {
			return nil, err
		}
		if state == step.result() {
			continue
		}
		pending = append(pending, step)
	}
	return pending, nil
}

// stepPaths are the vault-relative paths a set of steps names, in order.
func stepPaths(steps []Step) []string {
	paths := make([]string, 0, len(steps))
	for _, step := range steps {
		paths = append(paths, step.Path)
	}
	return paths
}
