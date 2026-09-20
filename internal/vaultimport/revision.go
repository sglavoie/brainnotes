package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// resolveToken names a conflict resolution in this vault's own content history.
// Like every other token in internal/noteops it is a fixed word chosen by the
// code, and it is deliberately not operationToken: the revision is this
// device's own work and is published to the peer, where the application it sits
// on top of is the peer's work coming back and is not.
const resolveToken = "sync-resolve"

// defaultResolveCommand is what the undo journal records when a caller names no
// invocation.
const defaultResolveCommand = "bn sync resolve"

// A resolution is two commits, and the order between them is the whole design.
//
// The first is the peer's operation applied exactly as any other import: the
// same transaction, the same provenance ledger, the same recovery entry, the
// same receipt. That is not a formality. A receipt promises the peer that every
// path it named holds what the package installed, and internal/vaultsync
// verifies that promise against the disk before it will mint one — so the only
// disk state an honest acknowledgment can describe is the one where the package
// was applied. A resolution that wrote the person's choice instead could never
// acknowledge anything, and the peer would redeliver the package forever.
//
// The second is the choice itself, recorded as a revision of the first. It is a
// commit this device owns and publishes, and it is what makes the peer converge
// on the decision rather than merely learning that its package arrived. The
// causality falls out of machinery that already exists: applying the package
// records the peer's operation against the local entry, so the export ledger
// names that operation as what this vault's paths last descended from, and the
// revision publishes with it as the predecessor and with the peer's own content
// as the version it expects to find. The peer holds exactly that, so the
// revision applies there with nothing to decide. A third device that has not
// seen the operation yet holds the revision on its predecessor until it does.
//
// Between the two commits the vault holds the peer's version, and that is
// deliberate rather than a window to be minimised. It is what puts the rejected
// version in the content history and in the recovery history, where undoing the
// resolution restores it. A resolution that never wrote it would be a decision
// with no record of what was decided against.
//
// The cost is that an interruption between them leaves the vault agreeing with
// the peer about a note somebody had decided to keep. That is why the plan is
// made durable — with its content staged and its journal entry named — before
// the application starts, and why every entry point resolves it before doing
// anything else. See finishResolutions.

// Resolution is what resolving one held package left behind.
type Resolution struct {
	// PackageID is the package that was resolved.
	PackageID string
	// OperationID is the peer operation it carried.
	OperationID string
	// OriginDevice is the peer that produced it.
	OriginDevice string
	// Applied is the application the resolution sits on top of, with whatever came
	// of the receipt this device owed for it. It is always set, including when the
	// revision afterwards failed: a Resolution is not built at all until the
	// application has answered, and the package is on disk and undoable either way.
	//
	// Applied.Receipt is a separate question and can be nil here. An application
	// that committed but could not mint or publish its acknowledgment returns a
	// Result beside its error, and a resolution carries on from it on purpose —
	// the decision is worth recording whether or not the peer has been told yet,
	// and the receipt is owed by the first commit rather than by this one. So read
	// Applied.Receipt before assuming the peer will stop redelivering.
	Applied *Result
	// JournalEntry is the recovery entry of the revision commit. It is empty when
	// the revision wrote nothing — a resolution that accepted everything incoming
	// and so had nothing to revise, or a re-drive that found the disk already
	// holding every step's result and therefore had no commit left to make.
	JournalEntry string
	// Paths are the vault-relative paths the revision names, which includes the
	// ones a choice derived and so are not in the hold.
	Paths []string
	// Cleanup is the revision's own housekeeping failure — staged content that
	// could not be removed after the resolution was over. The application's is on
	// Applied. See Result.Cleanup.
	Cleanup error
	// AbandonedResolutions names the conflict decisions this call's recovery pass
	// dropped on the way in, with the reason for each. It is the same map as
	// Recovery.AbandonedBecause and is here for the same reason it is on Result:
	// the pass runs inside Resolve, and once it has run, the state file no longer
	// remembers the promise it dropped.
	//
	// It can name this call's own package, and that is the usual case rather than
	// a surprise: an earlier decision about the same conflict, interrupted before
	// the application it revises landed, is dropped on the way in and the plan
	// being carried out now is what replaces it.
	AbandonedResolutions map[string]error
}

// Resolve applies a held package and records the plan's decisions as a revision
// of it.
//
// The plan must be one Preview produced against the state the vault is still
// in; a plan describing a conflict that has moved is refused as ErrStalePreview
// with nothing written. Every branch the plan names is also pinned as a
// transaction precondition, so an edit that lands after that check is refused
// under the path's own lock rather than overwritten.
//
// A non-nil Resolution returned with a non-nil error means the package is
// applied and the revision is not, which is the one partial outcome this can
// produce. It is recoverable and is recovered automatically: the plan is
// durable, so the next import, drain, or resolution in this vault finishes the
// revision before it does anything else.
//
// A *nil* Resolution beside an error is not the opposite claim. It says this
// call cannot describe what landed, not that nothing did: an application that
// failed after committing runs its own in-process recovery on the way out,
// which can carry this very resolution all the way through, and what comes back
// is then the original failure with no Result to hang a Resolution on. Ask the
// vault — LoadState and Recover — rather than reading the nil as an outcome.
//
// An *earlier* decision this call's recovery pass had to drop is reported on
// Resolution.AbandonedResolutions rather than refused, since dropping one is not
// a failure of the decision being made now. That one exception to the advice
// above is worth stating: an abandonment is the fact the vault stops holding, so
// asking it afterwards finds nothing. So whenever there is no Resolution to carry
// the news — the pass itself failed, or it succeeded and the resolution after it
// refused — it is folded into the error instead, see withAbandonments. A refusal
// is the likelier of the two: deciding again over a plan this vault has since
// moved past is exactly the sequence that abandons the first attempt.
func Resolve(root, packageDir string, plan *Plan, opts Options) (*Resolution, error) {
	if plan == nil {
		return nil, fmt.Errorf("%w: nothing was previewed", ErrStalePreview)
	}
	if opts.Command == "" {
		opts.Command = defaultResolveCommand
	}
	ctx, release, acquired, err := lockImports(root)
	if err != nil {
		return nil, err
	}
	defer release()
	if !acquired {
		return nil, fmt.Errorf("%w: %s was not resolved", ErrImportBusy, plan.PackageID)
	}
	opts.checkpointContext = ctx
	recovery, err := recoverLocked(root, opts)
	if err != nil {
		return nil, err
	}
	resolution, err := resolveLocked(root, packageDir, plan, opts)
	if resolution != nil {
		resolution.AbandonedResolutions = recovery.AbandonedBecause
		return resolution, err
	}
	return nil, withAbandonments(err, recovery)
}

// resolveLocked is the resolution itself, with the import guard held and any
// earlier attempt already resolved.
func resolveLocked(root, packageDir string, plan *Plan, opts Options) (*Resolution, error) {
	if err := plan.describes(packageDir); err != nil {
		return nil, err
	}
	held, state, err := plan.current(root)
	if err != nil {
		return nil, err
	}
	record, err := plan.commitment(opts)
	if err != nil {
		return nil, err
	}
	if err := declareResolution(root, state, held, record); err != nil {
		return nil, err
	}
	applied, applyErr := applyLocked(root, packageDir, opts.forResolution(record))
	// No Result is not the same fact as no application. A failure after the commit
	// — provenance, the snapshot, the receipt — leaves the change durable and the
	// attempt pending, and the next recovery pass rolls it forward. So the promise
	// is dropped only when this vault's own record says there is nothing for a
	// revision to revise, and left alone otherwise for finishResolutions to decide
	// once that question has an answer. The abandonment is joined onto the refusal
	// rather than replacing it: the caller asked about the resolution, and a hold
	// that is still a hold is the answer.
	if applied == nil {
		return nil, errors.Join(applyErr, abandonUnlessLanded(root, record))
	}
	resolution := &Resolution{
		PackageID:    record.PackageID,
		OperationID:  record.OperationID,
		OriginDevice: record.OriginDevice,
		Applied:      applied,
	}
	importCrash(crashResolved)
	if err := reviseResolution(root, record, opts, resolution); err != nil {
		return resolution, errors.Join(applyErr, err)
	}
	return resolution, applyErr
}

// describes refuses a package directory that is not the one the plan decided.
//
// Nothing else compares the two: the promise is keyed on the plan's package and
// the application reads its manifest out of the directory. A mismatched pair
// would apply one package under another package's decision — and because a
// resolution replaces the producer's expectations with the branches its plan
// pinned, that is an overwrite authorized by a decision about different
// content. It is asked before anything durable happens, where a caller's
// mistake is still only a refusal.
func (p *Plan) describes(packageDir string) error {
	manifest, err := vaultsync.ReadManifest(packageDir)
	if err != nil {
		return err
	}
	if manifest.PackageID != p.PackageID {
		return fmt.Errorf("%w: the plan resolves %s and %q carries %s",
			ErrResolutionUnavailable, p.PackageID, packageDir, manifest.PackageID)
	}
	return nil
}

// current refuses a plan the vault has moved past, and returns the hold it is
// still about.
//
// Every fact the plan was built on is asked again: the package is still held,
// it is still the same operation, the same paths are still contested for the
// same reasons, the peer's version is still the one that was previewed, and —
// the one that actually moves — this vault's version of each path is still what
// the person was looking at when they chose. A plan that named fewer paths than
// the hold does is refused as well, since resolving half an operation would
// leave the rest to be decided against a vault that has already changed.
func (p *Plan) current(root string) (*ConflictRecord, *State, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, nil, err
	}
	held, holding := state.Conflicts[p.PackageID]
	switch {
	case !holding:
		return nil, nil, fmt.Errorf("%w: %s is no longer held here", ErrStalePreview, p.PackageID)
	case held.OperationID != p.OperationID:
		return nil, nil, fmt.Errorf("%w: %s now holds operation %s rather than %s",
			ErrStalePreview, p.PackageID, held.OperationID, p.OperationID)
	case len(held.Paths) != len(p.Paths):
		return nil, nil, fmt.Errorf("%w: %s now contests %d paths rather than %d",
			ErrStalePreview, p.PackageID, len(held.Paths), len(p.Paths))
	}
	if err := p.matches(root, held); err != nil {
		return nil, nil, err
	}
	return held, state, nil
}

// matches checks the plan against the hold and the disk, path by path.
func (p *Plan) matches(root string, held *ConflictRecord) error {
	contested := make(map[string]PathConflict, len(held.Paths))
	for _, one := range held.Paths {
		contested[one.Path] = one
	}
	for _, decided := range p.Paths {
		one, still := contested[decided.Path]
		switch {
		case !still:
			return fmt.Errorf("%w: %s is no longer contested", ErrStalePreview, decided.Path)
		case one.Kind != decided.Kind:
			return fmt.Errorf("%w: %s is now a %s rather than a %s",
				ErrStalePreview, decided.Path, one.Kind, decided.Kind)
		case !one.Remote.same(decided.Remote):
			return fmt.Errorf("%w: the incoming version of %s is not the one that was previewed",
				ErrStalePreview, decided.Path)
		}
		state, err := diskState(absPath(root, decided.Path))
		if err != nil {
			return err
		}
		if !localBranch(state).same(decided.Local) {
			return fmt.Errorf("%w: %s has changed here since it was previewed",
				ErrStalePreview, decided.Path)
		}
	}
	return nil
}

// commitment is the plan as it will be recorded: the same decisions, with the
// identity of the revision's recovery entry minted and pinned.
//
// The identity is decided here, before anything happens, for the reason
// applier.apply mints its own before the first byte moves. It is what an
// interrupted resolution is finished under, and a revision that chose its own
// name afterwards could not be told from one that never ran.
func (p *Plan) commitment(opts Options) (*Plan, error) {
	record := *p
	record.Paths = slices.Clone(p.Paths)
	if !record.Revises() {
		return &record, nil
	}
	entryID, err := journal.NewEntryID(opts.now())
	if err != nil {
		return nil, fmt.Errorf("vaultimport: mint revision entry identity for %s: %w", p.PackageID, err)
	}
	record.JournalEntry = entryID
	return &record, nil
}

// declareResolution makes the revision durable before the application it will
// revise.
//
// Content first, then the record that names it, and that is the same ordering
// the retained branches are written under: a crash in between leaves staged
// bytes nothing points at, which the next resolution of this package overwrites
// and this one's own pruning removes. The other order would leave a durable
// promise to write content that was never saved.
//
// A resolution that revises nothing declares nothing. It has no second commit,
// so an interruption anywhere in it leaves either a package that was not
// applied — which is still held, and still resolvable — or one that was, which
// is the complete and correct outcome of accepting it.
func declareResolution(root string, state *State, held *ConflictRecord, record *Plan) error {
	if !record.Revises() {
		return nil
	}
	if err := stageRevision(root, held, record); err != nil {
		return err
	}
	next := state.clone()
	next.Resolutions[record.PackageID] = record
	if err := saveState(root, next); err != nil {
		return err
	}
	return pruneStaged(root, record)
}

// reviseResolution makes the second commit and clears the promise to make it.
func reviseResolution(root string, record *Plan, opts Options, resolution *Resolution) error {
	if !record.Revises() {
		return nil
	}
	rev := &revision{root: root, record: record, opts: opts}
	entryID, err := rev.commit()
	resolution.Cleanup = errors.Join(resolution.Cleanup, rev.cleanup)
	if err != nil {
		return err
	}
	resolution.JournalEntry = entryID
	resolution.Paths = stepPaths(record.steps())
	if err := clearResolution(root, record.PackageID); err != nil {
		return err
	}
	resolution.Cleanup = errors.Join(resolution.Cleanup, discardStaged(root, record.PackageID))
	return nil
}

// revision is one resolution's second commit being made.
type revision struct {
	root   string
	record *Plan
	opts   Options
	// cleanup is a transaction scratch file that could not be unlinked after its
	// destination was durably written. See Result.Cleanup.
	cleanup error
}

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

// finishResolutions carries every resolution this vault was interrupted inside
// to its end, and is the first thing every entry point does.
//
// One question decides each of them, and it is not "how far did it get" but
// "did the operation it resolves land". A package that is not in the applied
// ledger was rolled back or never started, so the resolution is abandoned and
// the package stays held — a person re-previews against whatever the vault
// holds now, which is the only honest thing to offer when the reading their
// choice was made against is gone. A package that is in the ledger is one this
// vault currently agrees with the peer about, against somebody's decision, so
// the revision is made: that is the state the whole arrangement exists to get
// out of, and leaving it would silently resolve the conflict the other way.
func finishResolutions(root string, opts Options) (revised, abandoned []string, why map[string]error, err error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, nil, nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, nil, nil, err
	}
	why = map[string]error{}
	for _, packageID := range slices.Sorted(maps.Keys(state.Resolutions)) {
		done, reason, err := finishResolution(root, state, state.Resolutions[packageID], opts)
		// Recorded before the error is acted on, and that order is the point. A
		// reason means the promise is already out of the state file, so an error
		// raised by the same call — staged bytes that would not delete — has no
		// later pass to hand the news to: returning early without recording it
		// would be the one way somebody's decision disappears silently.
		if reason != nil {
			abandoned = append(abandoned, packageID)
			why[packageID] = reason
		}
		if err != nil {
			return revised, abandoned, why, err
		}
		if done {
			revised = append(revised, packageID)
		}
	}
	// A scratch directory that could not be collected is not a reason to refuse
	// every import in the vault, and a recovery pass has no result to carry the
	// failure on — the same judgement finishResolution makes about its own
	// housekeeping. It is attempted again on the next pass.
	_ = collectStaged(root)
	return revised, abandoned, why, nil
}

// collectStaged removes every staged-revision directory no promise names.
//
// It is the other half of declareResolution's ordering, and the counterpart of
// collectRetained. Content is staged before the record that names it, so a
// crash in between leaves bytes nothing points at; and abandonResolution clears
// the record before removing them, so a crash in *that* gap leaves the same
// thing. Neither is dangerous and neither is retried by the process that would
// have retried it, since that process is the one that died. This is the sweep
// that eventually takes them away, and it runs from finishResolutions so it
// reaches a vault whose channel has never been drained.
//
// A directory whose name is not a package name is left alone rather than swept,
// which means "eventually" excludes it. That is deliberate and is the same rule
// collectRetained follows: the name comes off the disk, and joining an unchecked
// one onto a path to remove it recursively is a worse thing to be wrong about
// than a directory nobody collects. Nothing this package writes can produce such
// a name, so one being there is already a statement that something else put it
// there.
func collectStaged(root string) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	state, err := loadState(root, local)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(resolutionsRoot(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list staged revisions %q: %w", resolutionsRoot(root), err)
	}
	var failures []error
	for _, entry := range entries {
		if _, promised := state.Resolutions[entry.Name()]; promised || !vaultsync.PackageName(entry.Name()) {
			continue
		}
		failures = append(failures, discardStaged(root, entry.Name()))
	}
	return errors.Join(failures...)
}

// finishResolution decides one of them, and reports whether a revision was made
// and whether the promise was dropped.
//
// A housekeeping failure is dropped rather than reported, which is the one
// place in this package that does so and is the same judgement Result.Cleanup
// encodes with nowhere to put it: a recovery pass has no result to carry it on,
// the vault is correct either way, and refusing over a scratch directory would
// block every import in a vault whose resolution is complete.
//
// The second reason to drop a promise is a revision the vault has moved past.
// Every step is pinned to what applying the operation left on the path, so a
// note edited between the application and the revision refuses the whole
// transaction with the edit intact — and that refusal is permanent, because
// nothing puts the note back. Keeping the promise would mean re-trying it from
// every entry point forever, and since this runs before any of them do, one
// person's typing would refuse every import, drain, resolution and recovery
// this vault ever attempts again. So the decision is abandoned: the edit is
// newer than the choice, the rejected version is still in the recovery and
// content histories the application wrote, and nothing is lost that a person
// cannot see. It is reported on Recovery.Abandoned rather than silently, since
// a decision that did not take effect is exactly the thing a person needs told.
func finishResolution(root string, state *State, record *Plan, opts Options) (done bool, why error, err error) {
	if record == nil {
		return false, nil, fmt.Errorf("%w: a promised resolution names no plan", ErrCorruptState)
	}
	if _, applied := state.Applied[record.PackageID]; !applied {
		cleared, clearErr := abandonResolution(root, record)
		return false, abandonmentOf(cleared, errApplicationNeverLanded), clearErr
	}
	err = reviseResolution(root, record, opts, &Resolution{PackageID: record.PackageID})
	if err != nil && unkeepable(err) {
		// The reason is carried out rather than dropped with the error. "Your edit
		// was newer" and "this vault's staged copy of your merge is damaged" are
		// both abandonments and are not remotely the same news, and a package name
		// on a list cannot tell them apart.
		cleared, clearErr := abandonResolution(root, record)
		return false, abandonmentOf(cleared, err), clearErr
	}
	return err == nil, nil, err
}

// abandonmentOf is the reason to report for a promise abandonResolution says it
// cleared, and nothing at all for one it says it did not.
//
// It keeps the rule that matters in one place: the news is announced only once
// it is true. A clearResolution that failed leaves the decision in the state
// file for the next pass to make again, so calling it dropped would be an
// announcement about something that has not happened — and the next pass would
// announce it a second time when it does.
func abandonmentOf(cleared bool, why error) error {
	if !cleared {
		return nil
	}
	return why
}

// errApplicationNeverLanded is the reason for the other abandonment, which is
// not a failure and so has no error of its own to report.
var errApplicationNeverLanded = errors.New(
	"vaultimport: the application the decision was made about never landed, so the package is still held and can be decided again")

// unkeepable reports a promise no later pass can keep, whatever it runs on.
//
// Three sentinels say that, and they are the whole list. The revision was
// refused by what its paths hold now (ErrConflict, the case above). The content
// it would install is gone (ErrResolutionUnavailable, from a staged file that
// no longer exists). Or what is still there is not what it is named
// (ErrCorruptState, from a staged digest that disagrees with its own file) —
// and writing those bytes anyway is exactly the substitution the digest is
// checked to prevent.
//
// What makes abandoning safe is that none of the three can reach here with a
// half-made change behind it, and for two of them that is because they are
// raised before the revision writes anything at all. ErrConflict is the one
// that needs an argument. It can be raised *after* the transaction captured the
// journal — a configuration pin re-checked around an individual change, the
// case errors.go describes — so a refusal can leave a manifest-less entry
// directory and compensated-back content. write takes that back out before
// returning, and returns the refusal only when the unwind succeeded; an unwind
// that failed is reported as its own error, never joined with this sentinel,
// precisely so it cannot be answered with an abandonment. So ErrConflict
// arriving here is already a statement that nothing is half-made.
//
// A failure raised *during* the unwind is ErrStrandedImport and is deliberately
// absent from this list — see putBack.
//
// One arrival is outside that argument and is self-limiting rather than
// reasoned about: clearResolution runs after the revision has committed and
// finalized, and its own state load can fail with ErrCorruptState. Abandoning
// goes through the same load, so it fails identically, the promise is kept, and
// the error is returned rather than acted on.
//
// The distinction each one has to earn is transient versus permanent, not
// serious versus minor, because this runs ahead of every import, drain,
// resolution and recovery: a promise kept over a failure that will not clear
// stops the vault synchronizing at all. Anything not on this list — an I/O
// error, a lock somebody holds, the unwind's own racing refusal — is returned
// as itself and tried again on the next pass.
func unkeepable(err error) bool {
	return errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrResolutionUnavailable) ||
		errors.Is(err, ErrCorruptState)
}

// IsUnrecordableDecision answers whether a revision failure is one no later pass
// will retry, and it is unkeepable's door for a caller that has to say what
// happens next rather than decide it.
//
// The two answers are not degrees of the same news. An ordinary revision failure
// leaves the promise in the state file and the next pass makes the second commit,
// so a vault holding the peer's version holds it for a moment. One of these three
// abandons the promise instead, so that vault holds the version somebody decided
// against until they decide again — and the difference is the whole of what a
// person needs told.
func IsUnrecordableDecision(err error) bool { return unkeepable(err) }

// abandonUnlessLanded drops a promise the vault has no use for, and keeps one
// whose application is still being decided.
//
// The two states it keeps the promise for are the two an application can be in
// without having returned a Result: already recorded as applied — which the
// application's own in-process recovery can do on its way out — and still
// pending, where the next pass decides the direction and finishResolution then
// asks this same question of an answer instead of a guess.
func abandonUnlessLanded(root string, record *Plan) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	state, err := loadState(root, local)
	if err != nil {
		return err
	}
	if _, applied := state.Applied[record.PackageID]; applied {
		return nil
	}
	if state.Pending != nil && state.Pending.Manifest != nil &&
		state.Pending.Manifest.PackageID == record.PackageID {
		return nil
	}
	// The caller here is resolveLocked, which is about to return the refusal that
	// stopped the application; whether the promise was cleared changes nothing it
	// can say, so only the failure is passed on.
	//
	// It is usually the first to ask, and sometimes the second. An application
	// refused before it declared its attempt — a stale plan, a precondition that
	// failed at staging — never reaches applier.unwind, and this is the only thing
	// that drops the promise. One refused *after* does reach it, and unwind is a
	// whole recovery pass that sees this same unapplied promise first; by the time
	// control gets back here the state no longer names the package and
	// clearResolution is a no-op. Both orders are correct and neither caller may
	// assume the other ran, which is why this asks rather than checks.
	_, err = abandonResolution(root, record)
	return err
}

// abandonResolution forgets a resolution whose application never landed, and
// says whether the promise is actually gone.
//
// The two answers are reported separately because they have separate
// consequences. clearResolution runs first and is what makes the abandonment
// durable: a failure there leaves the promise in the state file, the next pass
// decides it again, and a caller must not tell anybody their choice was
// dropped. discardStaged runs second and only removes bytes nothing names any
// more; its failure is worth returning, but by then the decision is gone for
// good, and treating that as "still promised" would mean nobody is ever told.
//
// A plan that revises nothing is never written to the state file at all — see
// declareResolution — so there is nothing to clear and the promise is vacuously
// gone.
func abandonResolution(root string, record *Plan) (cleared bool, err error) {
	if !record.Revises() {
		return true, nil
	}
	if err := clearResolution(root, record.PackageID); err != nil {
		return false, err
	}
	return true, discardStaged(root, record.PackageID)
}

// clearResolution stops the state naming a package's revision. It runs before
// the staged content is removed, the ordering every store in this package is
// written under: a record naming content that is gone is a promise nothing can
// keep, where content nothing names is removable at leisure.
func clearResolution(root, packageID string) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	state, err := loadState(root, local)
	if err != nil {
		return err
	}
	if _, promised := state.Resolutions[packageID]; !promised {
		return nil
	}
	next := state.clone()
	delete(next.Resolutions, packageID)
	if err := saveState(root, next); err != nil {
		return err
	}
	return nil
}

// resolutionsName is the staged-revision store inside the synchronization state
// directory, beside the retained branches and out of the content history's
// scope for the same reason: content that has not been decided on yet must
// never be committed as though it had.
const resolutionsName = "resolutions"

// resolutionsRoot is the store itself, derived from vaultsync.StateDir for the
// reason StatePath is: a literal `.bnotes/sync/` beside a derived one would let
// the two drift apart.
func resolutionsRoot(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), resolutionsName)))
}

// resolutionDir is where one resolution's staged content lives. The identity is
// checked before it is joined onto a directory, the rule internal/vaultsync
// states for every path derived from one.
func resolutionDir(root, packageID string) (string, error) {
	if !vaultsync.PackageName(packageID) {
		return "", fmt.Errorf("%w: %q is not a package identity", ErrCorruptState, packageID)
	}
	return filepath.Join(resolutionsRoot(root), packageID), nil
}

// stageRevision writes every byte the revision will install, before the
// revision is promised.
//
// It is what makes the second commit recoverable at all. The content a
// resolution installs is not in the package and is not necessarily on the disk
// — a merge exists only in the caller's memory, and this vault's own version is
// about to be overwritten by the application the revision sits on top of. So it
// is saved first, content-addressed the way the retained branches are, and the
// re-drive reads it back rather than re-deriving a choice nobody is there to
// make again.
func stageRevision(root string, held *ConflictRecord, record *Plan) error {
	dir, err := resolutionDir(root, record.PackageID)
	if err != nil {
		return err
	}
	for _, step := range record.steps() {
		if step.Action != vaultsync.ActionWrite {
			continue
		}
		data, err := record.content(root, held, step.SHA256)
		if err != nil {
			return err
		}
		if err := vault.WriteStateFile(filepath.Join(dir, step.SHA256), data, statePerm); err != nil {
			return fmt.Errorf("vaultimport: stage revision content %s for %s: %w",
				step.SHA256, record.PackageID, err)
		}
	}
	return nil
}

// stagedContent reads back one step's content.
//
// The digest is verified rather than trusted, because this is the last reading
// before the bytes become a commit. The store is written by this package alone
// and lives where nothing else looks, so a mismatch means the file was damaged
// rather than replaced — which is exactly the case where installing it would
// put content in the history that nobody chose.
func stagedContent(root, packageID, digest string) ([]byte, error) {
	dir, err := resolutionDir(root, packageID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, digest))
	// Only content that is *gone* says the decision can never be carried out, and
	// only that reading may reach unkeepable — which abandons the promise and
	// then deletes this very store. Everything else a read can fail with is a
	// condition of the moment rather than of the decision: a descriptor limit, a
	// network mount, a backup tool holding the file open. Abandoning on one of
	// those would destroy a merge somebody typed because the machine was busy
	// for a second, so it is returned as itself and read again on the next pass.
	//
	// "Gone" is three answers, not one. ENOENT is the ordinary one. ENOTDIR says
	// a directory on the way to it is a file now, and EISDIR says the file itself
	// is a directory now — neither maps to fs.ErrNotExist, and both mean the
	// bytes are as absent as ENOENT says they are while being permanent in the
	// same way. Leaving those two out would answer a destroyed store by refusing
	// every import in the vault forever instead of dropping the one decision it
	// cost.
	if err != nil {
		if gone(err) {
			return nil, fmt.Errorf("%w: the revision content %s of %s is gone: %v",
				ErrResolutionUnavailable, digest, packageID, err)
		}
		return nil, fmt.Errorf("vaultimport: read the revision content %s of %s: %w", digest, packageID, err)
	}
	if vaultsync.HashBytes(data) != digest {
		return nil, fmt.Errorf("%w: the staged revision content %s of %s is not what it is named",
			ErrCorruptState, digest, packageID)
	}
	return data, nil
}

// gone reports a read failure that says the bytes are not there, as against one
// that says this machine could not get at them just now. See stagedContent for
// why the difference decides whether somebody's decision survives.
//
// Only ENOENT reaches fs.ErrNotExist — syscall.Errno.Is maps that sentinel and
// no other — so the three named errnos are load-bearing rather than belt and
// braces. Each is a permanent statement about the path this vault chose and
// wrote itself: a component of it is a file (ENOTDIR), the digest-named file is
// a directory (EISDIR), or something on the way is a symlink cycle (ELOOP).
// None of them clears on a retry, and a permanent failure that is not on this
// list stops every import, drain, resolution and recovery in the vault forever,
// because finishResolutions runs ahead of all four.
//
// EACCES is deliberately not here. A permission that denies this process may be
// a permission somebody can give back, and the bytes are still on the disk in
// the meantime; treating that as gone would throw a decision away over a
// recoverable mistake. ENAMETOOLONG is not here either — a path this long is a
// property of the vault's own root rather than of one decision, so it would
// fail identically for every package and is not a fact about this one.
func gone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, syscall.ENOTDIR) ||
		errors.Is(err, syscall.EISDIR) ||
		errors.Is(err, syscall.ELOOP)
}

// pruneStaged removes staged content this resolution's record has stopped
// naming, and runs after that record is saved for the reason retention.prune
// does: removing first would invite a re-drive to read content that is already
// gone.
//
// It is needed because one package can be resolved more than once. A resolution
// that is abandoned — its application refused, or rolled back after a crash —
// leaves its content behind, and the next attempt at the same package stages
// different bytes under a different name. Without this, a package somebody
// resolves three different ways keeps all three.
func pruneStaged(root string, record *Plan) error {
	dir, err := resolutionDir(root, record.PackageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(root, dir); err != nil {
		return err
	}
	named := make(map[string]struct{}, len(record.Paths))
	for _, step := range record.steps() {
		if step.Action == vaultsync.ActionWrite {
			named[step.SHA256] = struct{}{}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list staged revision content for %s: %w", record.PackageID, err)
	}
	return removeUnnamed(dir, record.PackageID, entries, named)
}

// removeUnnamed is the removal itself, separated so the marker below covers one
// loop rather than a function that also reads state.
func removeUnnamed(dir, packageID string, entries []os.DirEntry, named map[string]struct{}) error {
	var failures []error
	for _, entry := range entries {
		if _, still := named[entry.Name()]; still {
			continue
		}
		// Not vault content: one staged copy of a resolution's content inside this
		// package's own scratch directory under `.bnotes/sync`, which the content
		// history and the portable scope both exclude wholesale.
		//bn:direct-write removes one staged revision the state file written just above has stopped naming
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			failures = append(failures, fmt.Errorf("vaultimport: discard staged revision %s of %s: %w",
				entry.Name(), packageID, err))
		}
	}
	return errors.Join(failures...)
}

// discardStaged removes one resolution's staged content. The caller must have
// already written the state that stopped naming it.
func discardStaged(root, packageID string) error {
	dir, err := resolutionDir(root, packageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(root, dir); err != nil {
		return err
	}
	// Nothing in internal/vault removes a tree, and this is not vault content: it
	// is this package's own scratch directory under `.bnotes/sync`, which the
	// content history and the portable scope both exclude wholesale.
	//bn:direct-write removes only this resolution's own staged-content directory, after the state file that named it was rewritten without it
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("vaultimport: discard staged revision content for %s: %w", packageID, err)
	}
	return nil
}
