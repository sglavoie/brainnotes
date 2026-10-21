package vaultimport

import (
	"errors"
	"fmt"
	"slices"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
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
	if err := declareResolution(root, state, held, record, opts); err != nil {
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
		return nil, errors.Join(applyErr, abandonUnlessLanded(root, record, opts))
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
func declareResolution(root string, state *State, held *ConflictRecord, record *Plan, opts Options) error {
	if !record.Revises() {
		return nil
	}
	if err := stageRevision(root, held, record); err != nil {
		return err
	}
	next := state.clone()
	next.Resolutions[record.PackageID] = record
	if err := saveStateWith(root, next, opts.writeState); err != nil {
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
	if err := clearResolution(root, record.PackageID, opts); err != nil {
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
