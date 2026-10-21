package vaultgit

import (
	"context"

	"github.com/sglavoie/brainnotes/internal/agent"
)

// pendingSet is the classified, in-scope difference between the working tree
// and a commit, together with journal entries associated with pending removals.
type pendingSet struct {
	added    []string
	modified []string
	deleted  []string
	// deletionEntries are the journal entries whose records describe the
	// deletions above. Their files must not be committed without them.
	deletionEntries map[string]bool
}

func (p pendingSet) empty() bool {
	return len(p.added) == 0 && len(p.modified) == 0 && len(p.deleted) == 0
}

// scanPending classifies the working tree against a seeded index, validating
// removals as absent regular files rather than requiring a journal.
func (m *Manager) scanPending(ctx context.Context, head, op string) (pendingSet, error) {
	entries, err := m.git.status(ctx, m.indexPath)
	if err != nil {
		return pendingSet{}, safetyErrorf(KindRepositoryState, op, "the working tree could not be inspected: %s", agent.SanitizedDiagnostic(err))
	}
	changes, err := classifyPending(entries)
	if err != nil {
		return pendingSet{}, m.latch(withOp(err, op))
	}

	var set pendingSet
	inScope := make([]pendingChange, 0, len(changes))
	for _, change := range changes {
		if !inTrackedScope(change) {
			continue
		}
		inScope = append(inScope, change)
		switch change.kind {
		case changeAdded:
			set.added = append(set.added, change.path)
		case changeModified:
			set.modified = append(set.modified, change.path)
		case changeDeleted:
			set.deleted = append(set.deleted, change.path)
		}
	}

	if err := m.validateRemovals(ctx, head, op, set.deleted); err != nil {
		return pendingSet{}, err
	}
	set.deletionEntries = deletionJournalEntries(m.journalRoot, inScope)
	return set, nil
}

// PendingSummary is the in-scope difference between the working tree and HEAD,
// reported without changing anything. Like CommitResult it carries paths and
// never contents, so it is safe to log in full.
type PendingSummary struct {
	Added    []string
	Modified []string
	Deleted  []string
}

// Empty reports whether the working tree matches HEAD across the tracked scope.
func (p PendingSummary) Empty() bool {
	return len(p.Added) == 0 && len(p.Modified) == 0 && len(p.Deleted) == 0
}

// Count reports how many tracked paths differ.
func (p PendingSummary) Count() int {
	return len(p.Added) + len(p.Modified) + len(p.Deleted)
}

// ScanPending classifies the working tree and reports what a snapshot would
// record, without recording it.
//
// It is the watcher's poll: a loop that only ever called CommitPending would
// have no way to tell "nothing changed" from "something changed and the
// debounce has not elapsed", and one that only ever called Preflight would
// learn that a snapshot is permitted without learning whether there is anything
// to snapshot.
//
// A scan can recover an interrupted snapshot or migrate a legacy deletion block.
// Unsupported changes still latch a safety block. Ordinary deletions are returned
// in the summary so the watcher schedules them like any other external edit.
func (m *Manager) ScanPending(ctx context.Context) (PendingSummary, error) {
	const op = "scan-pending"
	release, err := m.acquire(safetyLockName, op, ctx)
	if err != nil {
		return PendingSummary{}, err
	}
	defer release()

	if err := m.checkBlocked(ctx, op); err != nil {
		return PendingSummary{}, err
	}
	if _, err := m.recoverIntentLocked(ctx, op); err != nil {
		return PendingSummary{}, err
	}
	if err := m.checkRepositoryState(op); err != nil {
		return PendingSummary{}, err
	}
	head, _, err := m.headState(ctx, op)
	if err != nil {
		return PendingSummary{}, err
	}
	if err := m.seedIndex(ctx, head, op); err != nil {
		return PendingSummary{}, err
	}
	set, err := m.scanPending(ctx, head, op)
	if err != nil {
		return PendingSummary{}, err
	}
	return PendingSummary{Added: set.added, Modified: set.modified, Deleted: set.deleted}, nil
}
