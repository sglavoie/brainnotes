package vaultexport

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// run resolves whatever the last pass left behind, then exports.
//
// The branch is checked first, before anything writes to the record. Recovering a
// reservation records the current branch as the cursor's, so a check made
// afterwards would be comparing the branch against itself and could never fire —
// and the one pass that most needs the check is exactly the interrupted one,
// because a person who switched branches is the likeliest reason it was
// interrupted at all.
func (e *exporter) run(ctx context.Context) error {
	// Deferred here rather than at the point the cursor moves, so every path out
	// of this pass reports it — including the halts, since Export returns its
	// result alongside the error. resolveReservation can adopt a package and
	// advance the cursor durably *before* the epoch check refuses, and a Cursor
	// left empty on that path would say "a baseline is still unfinished", which is
	// a different vault state entirely.
	defer func() { e.result.Cursor = e.state.Commit }()
	if err := e.checkBranch(); err != nil {
		return err
	}
	if err := e.resolveReservation(); err != nil {
		return err
	}
	if err := e.checkEnrollmentEpoch(); err != nil {
		return err
	}
	tip, err := e.history.Tip(ctx)
	if err != nil {
		// A vault between `git init` and its first snapshot has nothing to say.
		// That is not a failure and must not look like one, or enrolling a fresh
		// vault would report an error on its first synchronization.
		if errors.Is(err, vaultgit.ErrUnbornBranch) {
			return nil
		}
		return err
	}
	if e.state.Commit == "" {
		return e.exportBaseline(ctx, tip)
	}
	return e.exportCommits(ctx, tip)
}

// checkEnrollmentEpoch refuses a cursor from a previous enrollment generation.
//
// The epoch advances when a vault is restored from a backup, which is exactly
// when the history the cursor describes may not be the history this vault has
// any more. A cursor with nothing behind it — a fresh vault, or one after Reset —
// simply adopts the current epoch.
func (e *exporter) checkEnrollmentEpoch() error {
	if e.state.Epoch == e.local.Epoch {
		return nil
	}
	if e.state.Commit == "" && e.state.Baseline == "" {
		next := e.state.clone()
		next.Epoch = e.local.Epoch
		return e.commitState(next)
	}
	return fmt.Errorf("%w: cursor is at epoch %d, this vault is at %d; Reset re-baselines",
		ErrEpochChanged, e.state.Epoch, e.local.Epoch)
}

// checkBranch refuses a cursor recorded against a different branch.
func (e *exporter) checkBranch() error {
	if e.state.Branch == "" || e.state.Branch == e.history.Branch() {
		return nil
	}
	return fmt.Errorf("%w: cursor follows %s, HEAD is on %s", ErrBranchChanged, e.state.Branch, e.history.Branch())
}

// exportCommits replays the commits between the cursor and the tip.
func (e *exporter) exportCommits(ctx context.Context, tip string) error {
	contained, err := e.history.Contains(ctx, e.state.Commit, tip)
	if err != nil {
		return err
	}
	if !contained {
		return fmt.Errorf("%w: %s is not an ancestor of %s; Reset re-baselines",
			ErrHistoryRewritten, e.state.Commit, tip)
	}
	commits, err := e.history.CommitsAfter(ctx, e.state.Commit, tip)
	if err != nil {
		return err
	}
	e.result.PendingCommits = len(commits)
	previous := e.state.Commit
	for _, commit := range commits {
		if len(e.result.Packages) >= e.maxPackages {
			break
		}
		info, err := e.history.Inspect(ctx, commit)
		if err != nil {
			return err
		}
		// The chain is checked explicitly rather than inferred from rev-list's
		// output. A merge has two parents and cannot be replayed as one operation;
		// a commit whose parent is not the one before it means the range is not a
		// line at all. Either way the peer would be told a sequence of operations
		// that never happened in that order.
		if len(info.Parents) != 1 || info.Parents[0] != previous {
			return fmt.Errorf("%w: %s has %d parents and does not follow %s",
				ErrUnsupportedHistory, commit, len(info.Parents), previous)
		}
		if err := e.exportCommit(ctx, commit, info); err != nil {
			return err
		}
		previous = commit
		e.result.PendingCommits--
	}
	return nil
}

// exportCommit publishes one commit, or advances past it.
func (e *exporter) exportCommit(ctx context.Context, commit string, info vaultgit.CommitInfo) error {
	changes, err := e.history.Changes(ctx, commit)
	if err != nil {
		return err
	}
	authority, err := readCommitAuthority(ctx, e.history, changes)
	if err != nil {
		return err
	}
	portable := portableChanges(changes)

	imported, isImport, err := e.importedOperation(authority.entryIDs)
	if err != nil {
		return fmt.Errorf("%s: %w", commit, err)
	}
	if isImport {
		return e.skipImported(commit, portable, imported, authority.entryIDs)
	}
	if len(portable) == 0 {
		return e.skipCommit(commit, SkipPrivate, portable, "")
	}
	blobs, err := e.history.Blobs(ctx, changedBlobOIDs(portable))
	if err != nil {
		return err
	}
	return e.publishCommit(ctx, commit, info, portable, blobs)
}

// importedOperation reports the peer operation a commit applied, if it applied
// one.
//
// A commit either is an applied import or is not; a commit naming both an
// imported journal entry and a local one is refused rather than guessed at.
// internal/noteops commits exactly one entry's paths, so the mixed shape cannot
// arise from this codebase — and if it ever does, treating it as an import would
// silently drop the local half and treating it as local would echo the peer's.
func (e *exporter) importedOperation(entryIDs []string) (Import, bool, error) {
	if !e.knowsAll(entryIDs) {
		if err := e.refreshImported(); err != nil {
			return Import{}, false, err
		}
	}
	var found []Import
	for _, id := range entryIDs {
		if record, known := e.state.Imported[id]; known {
			found = append(found, record)
		}
	}
	if len(found) == 0 {
		return Import{}, false, nil
	}
	if len(found) != len(entryIDs) {
		return Import{}, false, fmt.Errorf("%w: commit carries both imported and local journal entries", ErrUnsupportedHistory)
	}
	for _, record := range found[1:] {
		if record.OperationID != found[0].OperationID {
			return Import{}, false, fmt.Errorf("%w: commit carries two different imported operations", ErrUnsupportedHistory)
		}
	}
	return found[0], true, nil
}

// knowsAll reports whether the in-memory ledger already answers for every entry,
// so the common commit — a local one naming one entry the ledger lacks — is the
// only kind that pays for re-reading the record.
func (e *exporter) knowsAll(entryIDs []string) bool {
	for _, id := range entryIDs {
		if _, known := e.state.Imported[id]; !known {
			return false
		}
	}
	return true
}

// skipImported advances past a commit that applied a peer operation, recording
// the peer's operation as what last touched each of its paths.
//
// That ledger update is the point of skipping rather than ignoring. A later
// local edit to one of these paths must name the peer's operation as its causal
// predecessor, which is how the peer learns this device had seen its change
// before making its own. Dropping the edge would make every local edit after an
// import look like a conflict.
//
// The consumed ledger entries are deleted in the same write that advances the
// cursor, so the record cannot outlive its usefulness and cannot be lost before
// it is used.
func (e *exporter) skipImported(commit string, portable []vaultgit.FileChange, imported Import, entryIDs []string) error {
	next := e.state.clone()
	for _, change := range portable {
		next.Paths[change.Path] = imported.OperationID
	}
	for _, id := range entryIDs {
		delete(next.Imported, id)
	}
	next.Commit = commit
	next.Branch = e.history.Branch()
	e.result.Skipped = append(e.result.Skipped, Skipped{Commit: commit, Reason: SkipImported})
	return e.commitState(next)
}

// skipCommit advances past a commit that produced no package. operationID, when
// set, is recorded as what last touched each portable path.
func (e *exporter) skipCommit(commit string, reason SkipReason, portable []vaultgit.FileChange, operationID string) error {
	next := e.state.clone()
	if operationID != "" {
		for _, change := range portable {
			next.Paths[change.Path] = operationID
		}
	}
	next.Commit = commit
	next.Branch = e.history.Branch()
	e.result.Skipped = append(e.result.Skipped, Skipped{Commit: commit, Reason: reason})
	return e.commitState(next)
}

// commitState makes a record durable and only then adopts it in memory, so a
// failed write can never leave this pass believing something a restart would
// not.
//
// The record's lock is taken here, for this one write, rather than for the pass.
// Every producer field is this pass's alone — the producer guard excludes the
// other writers of them — but the import ledger is not: RecordImport adds to it
// while the pass runs, and writing the pass's copy over the disk would drop the
// provenance of an import whose commit the next pass then publishes straight
// back to its origin. So the disk is re-read under the lock, its producer fields
// are required to be the ones this pass last wrote, and every ledger entry the
// pass has not consumed is carried into the record being written.
func (e *exporter) commitState(next *State) error {
	release, err := lockState(e.root)
	if err != nil {
		return err
	}
	defer release()
	current, err := loadState(e.root, e.local)
	if err != nil {
		return err
	}
	if !sameProducer(current, e.state) {
		return fmt.Errorf("%w: %s", ErrStateMoved, StatePath(e.root))
	}
	consuming := e.mergeImported(next, current)
	if err := saveState(e.root, next); err != nil {
		return err
	}
	for id := range consuming {
		e.consumed[id] = struct{}{}
	}
	e.state = next
	return nil
}

// mergeImported carries into next every ledger entry the disk holds that this
// pass has neither consumed already nor is consuming in next, and returns the
// ones next consumes. An entry the pass knew and next drops is being consumed
// by this very write, and must not be merged back from the disk that still
// holds it.
func (e *exporter) mergeImported(next, current *State) map[string]struct{} {
	consuming := map[string]struct{}{}
	for id := range e.state.Imported {
		if _, kept := next.Imported[id]; !kept {
			consuming[id] = struct{}{}
		}
	}
	for id, record := range current.Imported {
		_, consumed := e.consumed[id]
		_, dropped := consuming[id]
		if _, known := next.Imported[id]; !known && !consumed && !dropped {
			next.Imported[id] = record
		}
	}
	return consuming
}

// sameProducer reports whether two records agree on every field only a producer
// writes. The path ledger is left out: it is written only alongside the cursor,
// so a record that agrees on the cursor and the sequence agrees on it too.
func sameProducer(a, b *State) bool {
	if a.Epoch != b.Epoch || a.Branch != b.Branch || a.Commit != b.Commit ||
		a.Baseline != b.Baseline || a.Sequence != b.Sequence {
		return false
	}
	if a.Pending == nil || b.Pending == nil {
		return a.Pending == nil && b.Pending == nil
	}
	return *a.Pending == *b.Pending
}

// refreshImported adopts ledger entries recorded since this pass read the record.
//
// An import records its provenance before its commit exists, and the pass reads
// the tip after it reads the record, so a commit in this pass's range can carry
// an entry the in-memory ledger has never seen. The disk already holds it —
// RecordImport is durable before the commit it describes — so adopting it is
// reading a durable fact rather than believing something a restart would not.
func (e *exporter) refreshImported() error {
	current, err := loadState(e.root, e.local)
	if err != nil {
		return err
	}
	for id, record := range current.Imported {
		if _, consumed := e.consumed[id]; consumed {
			continue
		}
		if _, known := e.state.Imported[id]; !known {
			e.state.Imported[id] = record
		}
	}
	return nil
}

// portableChanges filters a commit's changes to the portable content scope, in a
// fixed path order.
//
// vaultsync.Portable is the only filter, and it is applied to every path without
// exception: this vault's own export cursor, its undo journal, its sidecar state,
// its machine-local configuration overlay, and the synchronization channels
// themselves are all under `.bnotes/`, which that policy excludes wholesale.
func portableChanges(changes []vaultgit.FileChange) []vaultgit.FileChange {
	portable := make([]vaultgit.FileChange, 0, len(changes))
	for _, change := range changes {
		if vaultsync.Portable(change.Path) {
			portable = append(portable, change)
		}
	}
	sort.Slice(portable, func(i, j int) bool { return portable[i].Path < portable[j].Path })
	return portable
}

// changedBlobOIDs lists every blob a change set references, both sides.
//
// Both sides, in one read, because a package carries the producer's *prior*
// belief about each path as well as its new content — and that prior belief is a
// SHA-256 of the old bytes, which only the old blob can supply. Asking for them
// separately would double the process count on exactly the commits that change
// the most.
func changedBlobOIDs(changes []vaultgit.FileChange) []string {
	seen := map[string]struct{}{}
	oids := make([]string, 0, len(changes)*2)
	add := func(oid string) {
		if oid == "" {
			return
		}
		if _, known := seen[oid]; known {
			return
		}
		seen[oid] = struct{}{}
		oids = append(oids, oid)
	}
	for _, change := range changes {
		add(change.OldOID)
		add(change.NewOID)
	}
	return oids
}
