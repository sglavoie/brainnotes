package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// undo takes an application that never became recovery history back out.
//
// The order is the contract. Content first, because that is the part another
// process can see; the half-built journal entry second, because its snapshots are
// what the content was restored *from* and removing them first would make a
// failure between the two unrecoverable; the attempt last, because it is what
// says either of the first two still has to happen.
//
// This can be interrupted too, so the content step writes down that it finished:
// see Attempt.Restored. The two steps after it are removals of things nothing
// else reads, which is what makes them safe to repeat — but only for a pass that
// is not still deciding whether the first step happened.
func (r *resolver) undo() (*Recovery, error) {
	restored, err := r.rollBack()
	if err != nil {
		return nil, err
	}
	if err := journal.DiscardUnfinalized(noteops.JournalRoot(r.root), r.attempt.JournalEntry); err != nil {
		return nil, err
	}
	if err := r.settle(nil); err != nil {
		return nil, err
	}
	outcome := OutcomeDiscarded
	if len(restored) > 0 {
		outcome = OutcomeRolledBack
	}
	return &Recovery{Outcome: outcome, PackageID: r.manifest.PackageID, Paths: restored}, nil
}

// rollBack puts the content back if that has not already happened, and reports
// the paths it put back.
//
// It asks two questions before it plans anything, and neither is an optimization.
// An attempt that already recorded a completed rollback is answered from that
// record, because the disk can no longer tell it apart from one that never
// started. And an attempt whose journal entry was never started cannot be planned
// against at all: plan decides per path by comparing the disk against what the
// package said it would find, so it is only meaningful when the package was right
// about that — and the commonest reason an application fails is that it was
// *wrong* about it. Without that branch an ordinary ErrConflict would leave an
// attempt no later pass could resolve, and the vault would refuse every
// subsequent import over a refusal that changed nothing. journal.Started answers
// it from the operation's own durable trace, which no claim in the package can
// contradict.
//
// The record is written after the restoration rather than before it, and the gap
// between the two is the one thing here that is narrowed rather than closed. A
// content transaction and a state file cannot be made one write, so a process
// that dies between them leaves a finished rollback that says nothing — and a
// person who edits one of those notes before the next pass runs turns it into the
// stranding refusal above, over work that was already correct. The other order is
// worse and not a trade: a record written first, then a crash before the restore,
// makes the next pass skip the restoration entirely, discard the entry holding
// the only copy of the pre-change bytes, and settle an attempt whose content is
// still the peer's. So the window is kept and made as small as one fsynced write:
// everything after it — discarding the entry directory, which is a recursive
// removal and not atomic either, and settling the attempt — is covered.
func (r *resolver) rollBack() ([]string, error) {
	if r.attempt.Restored != nil {
		return r.attempt.Restored.Paths, nil
	}
	started, err := journal.Started(noteops.JournalRoot(r.root), r.attempt.JournalEntry)
	if err != nil {
		return nil, err
	}
	var plan []string
	if started {
		if plan, err = r.plan(); err != nil {
			return nil, err
		}
		if err := r.restore(plan); err != nil {
			return nil, err
		}
	}
	if err := r.markRestored(plan); err != nil {
		return nil, err
	}
	importCrash(crashRestored)
	return plan, nil
}

// markRestored makes the finished rollback durable, in the same file and under
// the same lock as everything else this package remembers.
//
// It is written even when nothing was put back, and that is the case it matters
// most for: "there was nothing to restore" is a conclusion drawn from what the
// disk held at one moment, and a pass that has to draw it again later may be
// looking at a vault somebody has edited since.
func (r *resolver) markRestored(paths []string) error {
	next := r.state.clone()
	next.Pending.Restored = &Rollback{Paths: paths}
	if err := saveStateWith(r.root, next, r.opts.writeState); err != nil {
		return err
	}
	r.state = next
	r.attempt = next.Pending
	return nil
}

// plan reports which of the package's paths have to be put back, and refuses if
// any of them has moved on.
//
// Three answers per path and no fourth. A path holding what the package expected
// to find was never written, so there is nothing to undo. A path holding what the
// package installed was written and is undone. A path holding anything else was
// edited by somebody after the interruption — and that edit is newer than the
// import, newer than the snapshot, and the only copy of itself.
//
// A path the attempt recorded as converged is none of the three and is not asked
// about. It already held the package's result before the import began, so it
// reads as installed and would be "undone" to a baseline this vault left behind
// on its own — destroying local content the interrupted application never
// touched. See Attempt.Converged.
func (r *resolver) plan() ([]string, error) {
	var (
		plan      []string
		contested []string
	)
	skip := make(map[string]struct{}, len(r.attempt.Converged))
	for _, rel := range r.attempt.Converged {
		skip[rel] = struct{}{}
	}
	for _, change := range r.operation.Changes {
		if _, converged := skip[change.Path]; converged {
			continue
		}
		state, err := diskState(absPath(r.root, change.Path))
		if err != nil {
			return nil, err
		}
		switch {
		case state == beforeState(change):
			continue
		case state == afterState(change):
			plan = append(plan, change.Path)
		default:
			contested = append(contested, change.Path)
		}
	}
	if len(contested) > 0 {
		return nil, r.stranded("rolled back", contested)
	}
	return plan, nil
}

// restore puts the listed paths back as one transaction.
//
// It is journaled by nothing and snapshotted into nothing, deliberately. A
// rollback returns the vault to the state it was in before the import began,
// which is a state the content history already describes; recording an undo entry
// for it would add a manifest describing a change that, once finished, cannot be
// observed, and taking a snapshot would commit the vault's own prior content back
// over itself as though something had happened.
//
// Every expectation here is pinned to what the *package* installed rather than to
// what the plan just saw. That is what makes "no rollback over a newer edit" a
// property of the transaction instead of a property of a check: internal/vault
// verifies each one under the path's own lock, so an edit landing between the
// plan and the commit refuses the rollback rather than losing to it.
func (r *resolver) restore(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	txn, err := vault.NewFileTxn(r.root)
	if err != nil {
		return err
	}
	index := changeIndex(r.operation)
	for _, rel := range paths {
		if err := r.stage(txn, index[rel]); err != nil {
			return err
		}
	}
	if err := txn.Commit(nil); err != nil {
		if vault.IsTxnConflict(err) {
			return r.stranded("rolled back", paths)
		}
		return fmt.Errorf("vaultimport: roll back %s: %w", r.manifest.PackageID, err)
	}
	return nil
}

// stage puts one path's restoration into the transaction.
func (r *resolver) stage(txn *vault.FileTxn, change vaultsync.Change) error {
	expect := resultExpectation(change)
	if !change.ExpectExists {
		return txn.Remove(change.Path, expect)
	}
	data, err := journal.ReadSnapshot(noteops.JournalRoot(r.root), r.attempt.JournalEntry, change.Path)
	if err != nil {
		return err
	}
	// The snapshot was taken before the change and the change only committed
	// because the path held what the producer believed, so these two must agree.
	// They disagreeing means this is not the entry the attempt names, and writing
	// the bytes anyway would restore some other operation's idea of the path.
	if got := vaultsync.HashBytes(data); got != change.ExpectSHA256 {
		return fmt.Errorf("%w: entry %s holds %s for %q, the package replaced %s",
			ErrStrandedImport, r.attempt.JournalEntry, got, change.Path, change.ExpectSHA256)
	}
	return txn.Write(change.Path, data, 0, expect)
}

// settle writes the one state change that ends an attempt: the pending record is
// dropped, and — when the application stands — the package is remembered as
// applied. Both in a single write, because a crash between two would either
// strand the attempt or forget the application.
func (r *resolver) settle(record *Record) error {
	next := r.state.clone()
	next.Pending = nil
	released := false
	if record != nil {
		next.Applied[r.manifest.PackageID] = *record
		// A package cannot be both applied and held — State.validate refuses the
		// pair outright — and an interrupted application that completes is an
		// application. A hold on this package could only predate it, from a
		// delivery refused before a later one got through.
		_, released = next.Conflicts[r.manifest.PackageID]
		delete(next.Conflicts, r.manifest.PackageID)
	}
	if err := saveStateWith(r.root, next, r.opts.writeState); err != nil {
		return err
	}
	if released {
		// The record has stopped naming them, so a failure here leaves a
		// directory nothing points at — which the next hold on this package
		// overwrites and Drain's sweep collects. Recovery has no housekeeping
		// channel to carry it out on and must not fail an application over it.
		_ = discardRetained(r.root, r.manifest.PackageID)
	}
	return nil
}

// stranded builds the refusal that keeps every version of the content. It names
// the paths and the journal entry, because those are what a person needs to see
// all of them: the entry's snapshots hold what the vault had, the paths hold what
// is there now, and the package is still in the channel.
func (r *resolver) stranded(action string, paths []string) error {
	return fmt.Errorf("%w: %s cannot be %s because %s no longer holds what the import left; "+
		"the attempt, journal entry %s, and the content are all kept",
		ErrStrandedImport, r.manifest.PackageID, action, strings.Join(paths, ", "), r.attempt.JournalEntry)
}

// pathState is what a path holds, reduced to the three distinctions an import
// cares about. A non-regular file is deliberately not "present with unknown
// content": a directory or a symlink standing where a note was is something no
// change in a package could have produced, so it compares equal to nothing.
type pathState struct {
	present bool
	regular bool
	sha256  string
}

func diskState(abs string) (pathState, error) {
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pathState{}, nil
	case err != nil:
		return pathState{}, fmt.Errorf("vaultimport: inspect %q: %w", abs, err)
	case !info.Mode().IsRegular():
		return pathState{present: true}, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return pathState{}, fmt.Errorf("vaultimport: read %q: %w", abs, err)
	}
	return pathState{present: true, regular: true, sha256: vaultsync.HashBytes(data)}, nil
}

// beforeState is what the package expected the path to hold. Manifest.Validate
// has already made ExpectExists and ExpectSHA256 agree, so this cannot describe a
// file with no content.
func beforeState(change vaultsync.Change) pathState {
	if !change.ExpectExists {
		return pathState{}
	}
	return pathState{present: true, regular: true, sha256: change.ExpectSHA256}
}

// afterState is what the package installs at the path.
func afterState(change vaultsync.Change) pathState {
	if change.Action == vaultsync.ActionDelete {
		return pathState{}
	}
	return pathState{present: true, regular: true, sha256: change.ResultSHA256}
}

// resultExpectation is afterState as the precondition a rollback is allowed
// under.
func resultExpectation(change vaultsync.Change) vault.FileExpectation {
	if change.Action == vaultsync.ActionDelete {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(change.ResultSHA256)
}

// ensureSnapshot records the entry in the vault's content history if it is not
// there already, under the token the operation was begun with. An import and a
// resolution's revision are different acts and reach the commit message as
// different words, so the token is passed in rather than assumed.
func ensureSnapshot(root, entryID, token string) error {
	return ensureSnapshotContext(context.Background(), root, entryID, token)
}

func ensureSnapshotContext(ctx context.Context, root, entryID, token string) error {
	needed, err := snapshotNeeded(root, entryID)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	return noteops.SnapshotEntryContext(ctx, root, entryID, token)
}

// snapshotNeeded asks the content history whether the entry's manifest is at the
// tip, which is how this decides whether to commit it again.
//
// The question matters because an entry that deleted a note proves that deletion
// with its own uncommitted record: re-committing an entry whose deletion already
// landed meets a path gone from the working tree *and* gone from HEAD, which is
// the shape internal/vaultgit refuses as an unexplained deletion. Recovering from
// an interruption must not be able to block the vault, so vaultgit answers the
// same question again under its own lock and treats an entry the history already
// holds as nothing to do. This asks anyway, because the answer read here is also
// the one this package reports and reasons about, and because a recovery pass
// that can see there is nothing to do should not take the vault's write lock to
// find out.
//
// A vault with no content history needs no snapshot. A vault whose branch has no
// commits yet needs one: the entry is not in a history that exists, and the
// snapshot is what creates it.
func snapshotNeeded(root, entryID string) (bool, error) {
	ctx := context.Background()
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{})
	if err != nil {
		if vaultgit.IsUnmanaged(err) {
			return false, nil
		}
		return false, fmt.Errorf("vaultimport: open content history: %w", err)
	}
	tip, err := history.Tip(ctx)
	if err != nil {
		if errors.Is(err, vaultgit.ErrUnbornBranch) {
			return true, nil
		}
		return false, fmt.Errorf("vaultimport: resolve history tip: %w", err)
	}
	rel := path.Join(config.DefaultJournalDir, entryID, journal.ManifestFileName)
	kinds, err := history.EntryKinds(ctx, tip, []string{rel})
	if err != nil {
		return false, fmt.Errorf("vaultimport: look up %q in the content history: %w", rel, err)
	}
	return kinds[rel] != "blob", nil
}
