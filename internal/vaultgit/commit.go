package vaultgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

// CommitPending records external changes: everything a person did to the vault
// with an editor, a sync client, or a shell, rather than through `bn`.
//
// Regular-file deletions are recorded like additions and modifications. Earlier
// commits retain the removed content; a journal is optional for external edits.
//
// It never pushes. The remote is not part of local correctness, and a snapshot
// that waited on a network round trip would make saving a note as slow and as
// failure-prone as the least reliable link in the user's setup.
func (m *Manager) CommitPending(ctx context.Context) (CommitResult, error) {
	const op = "commit-pending"
	release, err := m.acquire(safetyLockName, op, ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()
	return m.commitPendingLocked(ctx, op)
}

// completeExternalCommit anchors the external floor at a landed snapshot's
// completion bound and retires its intent. An external intent remains live
// until watch.json durably names the post-ref anchor and the ref's directory is
// synced. If either is missing, or clearing fails, the next recovery pass proves
// the commit landed, writes the same anchor again, and retires it.
func (m *Manager) completeExternalCommit(result CommitResult) {
	if _, err := m.state.NoteCommit(m.policy(), result.landedAt); err == nil && result.refDurable {
		_ = m.clearIntent()
	}
}

func (m *Manager) commitPendingLocked(ctx context.Context, op string) (CommitResult, error) {
	if err := m.checkBlocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	recoveredExternal, err := m.recoverIntentLocked(ctx, op)
	if err != nil {
		return CommitResult{}, err
	}
	if recoveredExternal {
		// Recovery just anchored a previously landed external snapshot at its
		// observed completion bound. Do not let this same call land a second one;
		// the watcher will reopen the still-dirty burst on its next scan and the
		// recovered floor will schedule it normally.
		return CommitResult{}, nil
	}
	if err := m.checkRepositoryState(op); err != nil {
		return CommitResult{}, err
	}

	// The instant the floor was anchored at, for the attempt that is about to run.
	// It is zero until an attempt writes one, which is what distinguishes "this
	// pass took no snapshot" from "this pass found nothing to snapshot" — only the
	// first of those has a marker of its own to retire.
	var anchoredAt time.Time
	result, err := m.withCAS(ctx, op, func(head string) (*commitPlan, error) {
		return m.planExternalCommit(ctx, head, op, &anchoredAt)
	})
	if err != nil {
		m.abandonStaleExternalCommit(err, anchoredAt)
		// For other failures the marker stays. An attempt that failed on the way to the repository may
		// or may not have left a commit behind — a latched block says the vault is
		// refused either way, and a floor held for one interval by a snapshot that
		// never landed is the cheap side of that uncertainty.
		return result, err
	}
	if result.Committed {
		// This is the one call that advances the external floor's anchor, because
		// this is the one entry point that takes an external autosave. It also
		// retires the write-ahead marker, replacing it with the later ref-landing
		// boundary already persisted in the recovery intent.
		m.completeExternalCommit(result)
		return result, nil
	}
	if !anchoredAt.IsZero() {
		// The pass reached the repository and provably recorded nothing — every
		// discovered addition was unreadable, or the tree it built already matched
		// the tip. No history was written, so no interval is owed.
		if _, err := m.state.AbandonExternalCommit(m.policy(), anchoredAt); err != nil {
			// Failing to retire the marker only defers the next external snapshot by
			// one interval, which is the safe direction and self-correcting: the
			// anchor it states recedes with the clock.
			_ = err
		}
	}
	return result, nil
}

func (m *Manager) planExternalCommit(ctx context.Context, head, op string, anchoredAt *time.Time) (*commitPlan, error) {
	set, err := m.scanPending(ctx, head, op)
	if err != nil || set.empty() {
		return nil, err
	}
	at := m.now()
	if _, err := m.state.BeginExternalCommit(m.policy(), at); err != nil {
		return nil, safetyErrorf(KindRepositoryState, op,
			"the external autosave floor could not be anchored, so no snapshot was attempted: %s", agent.SanitizedDiagnostic(err))
	}
	*anchoredAt = at
	plan := &commitPlan{
		at:                at,
		add:               append(append([]string{}, set.added...), set.modified...),
		remove:            set.deleted,
		deletionEntries:   set.deletionEntries,
		message:           autosaveMessage(at),
		externalAdditions: true,
	}
	sort.Strings(plan.add)
	return plan, nil
}

// CommitJournalEntry records one finalized BrainNotes mutation.
//
// It stages exactly the paths the journal entry names plus the entry's own
// files, and nothing else — a note the user happened to be editing in another
// window is not swept into a commit labelled as a rename. Before staging
// anything it verifies that the working tree holds what the entry says it
// produced. The journal preserves pre-mutation bytes even when they differ
// from HEAD. A mismatch means the world moved between the mutation and this
// call, which is a stale request rather than damage — nothing is committed,
// nothing is blocked, and the next pending-change snapshot picks up whatever
// the vault actually holds now.
//
// An entry already present in history is a no-op, checked under the safety lock
// because the watcher may have committed it since the mutation finished.
func (m *Manager) CommitJournalEntry(ctx context.Context, entryID, operation string) (CommitResult, error) {
	const op = "commit-journal"
	if err := validEntryID(entryID); err != nil {
		return CommitResult{}, safetyErrorf(KindStaleRequest, op, "%s", err.Error())
	}
	release, err := m.acquire(safetyLockName, op, ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	if _, err := m.recoverIntentLocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	if err := m.checkRepositoryState(op); err != nil {
		return CommitResult{}, err
	}

	entry, err := journal.LoadEntry(m.journalRoot, entryID)
	if err != nil {
		return CommitResult{}, safetyErrorf(KindStaleRequest, op, "journal entry %q could not be loaded, so there is nothing to commit for it", entryID)
	}
	entryFiles, err := journalEntryFiles(m.journalRoot, entryID)
	if err != nil {
		return CommitResult{}, safetyErrorf(KindStaleRequest, op, "journal entry %q could not be read from disk", entryID)
	}

	spec := journalCommit{
		op:      op,
		entryID: entryID,
		message: journalMessage(operation, entryID),
		entry:   entry,
		files:   entryFiles,
	}
	return m.withCAS(ctx, op, func(head string) (*commitPlan, error) {
		return m.planJournalCommit(ctx, head, spec)
	})
}

// journalCommit carries one CommitJournalEntry call's already-loaded inputs into
// the compare-and-swap, so a retry against a moved head replans from them rather
// than reading the entry off the disk again.
type journalCommit struct {
	op      string
	entryID string
	message string
	entry   *journal.Entry
	files   []string
}

// planJournalCommit builds the plan for one journal entry against head, or
// returns a nil plan when there is nothing to commit.
//
// A nil plan means either that the history already holds this entry or that the
// entry names nothing this engine can represent. Both are no-ops rather than
// failures, and both are decided here — under the lock, against the head this
// commit would be built on — because that is the only answer still true when the
// commit happens.
func (m *Manager) planJournalCommit(ctx context.Context, head string, spec journalCommit) (*commitPlan, error) {
	recorded, err := m.holdsJournalEntry(ctx, head, spec.entryID)
	if err != nil {
		return nil, safetyErrorf(KindRepositoryState, spec.op,
			"journal entry %q could not be looked up in the history: %s", spec.entryID, agent.SanitizedDiagnostic(err))
	}
	if recorded {
		return nil, nil
	}
	add, deletions, err := m.journalEntryPaths(spec.op, spec.entryID, spec.entry)
	if err != nil {
		return nil, err
	}
	plan := &commitPlan{message: spec.message, add: append(add, spec.files...)}
	sort.Strings(plan.add)

	if len(deletions) > 0 {
		if err := m.authorizeDeletions(ctx, spec.op, head, spec.entry, deletions, plan); err != nil {
			return nil, err
		}
	}
	if len(plan.add) == 0 && len(plan.remove) == 0 {
		return nil, nil
	}
	return plan, nil
}

// journalEntryPaths splits the entry's in-scope records into the paths this
// commit adds and the ones it removes.
//
// Both ends of every record are verified here rather than at staging: the
// working tree must still hold what the entry says it produced. A mismatch
// refuses the whole commit rather than the one path, because a journal entry is
// one mutation and committing half of it would record a change nobody made.
func (m *Manager) journalEntryPaths(op, entryID string, entry *journal.Entry) ([]string, []string, error) {
	var add, deletions []string
	for _, record := range entry.Files {
		if !InScope(record.Path) {
			continue
		}
		ok, err := m.matchesWorktree(record)
		if err != nil {
			return nil, nil, safetyErrorf(KindStaleRequest, op, "%q could not be read to confirm the recorded change, so nothing was committed", record.Path)
		}
		if !ok {
			return nil, nil, safetyErrorf(KindStaleRequest, op, "%q no longer matches what journal entry %s recorded, so nothing was committed", record.Path, entryID)
		}
		switch record.After.Kind {
		case journal.KindFile:
			add = append(add, record.Path)
		case journal.KindMissing:
			if record.Before.Kind == journal.KindFile {
				deletions = append(deletions, record.Path)
			}
		default:
			// The entry says the path ended as a directory or a socket. That is a
			// real state a `bn` command can record, and it is not one this engine
			// can represent in a tree, so the path is left out rather than guessed
			// at. It shows up as an ordinary pending change on the next scan.
			continue
		}
	}
	return add, deletions, nil
}

// authorizeDeletions validates the removed paths without requiring HEAD to match
// the journal's pre-image. The journal preserves edits made since the last commit.
func (m *Manager) authorizeDeletions(ctx context.Context, op, head string, entry *journal.Entry, deletions []string, plan *commitPlan) error {
	if err := m.validateRemovals(ctx, head, op, deletions); err != nil {
		return err
	}
	plan.remove = deletions
	plan.deletionEntries = map[string]bool{entry.ID: true}
	sort.Strings(plan.remove)
	return nil
}

// CommitPaths records exactly the paths a caller names, and nothing else.
//
// It is the entry point for durable state that is not a note and has no journal
// entry behind it: the brief, the cockpit queue, and the recall sidecar. Those
// files are rewritten wholesale by their owning package under its own lock, so
// there is no before/after record to verify and none is needed — the caller is
// reporting a write it has already completed at a path it named.
//
// It can only ever add. A path that no longer exists is dropped rather than
// staged as a removal: CommitPending owns external deletions, while this method
// records only the writes its caller names. Out-of-scope paths are dropped for the same reason
// they are dropped everywhere else: the scope policy decides what the engine may
// add on its own initiative.
func (m *Manager) CommitPaths(ctx context.Context, operation string, paths []string) (CommitResult, error) {
	const op = "commit-paths"
	staged, err := m.relativeExistingPaths(paths)
	if err != nil {
		return CommitResult{}, safetyErrorf(KindStaleRequest, op, "%s", err.Error())
	}
	if len(staged) == 0 {
		return CommitResult{}, nil
	}

	release, err := m.acquire(safetyLockName, op, ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	if _, err := m.recoverIntentLocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	if err := m.checkRepositoryState(op); err != nil {
		return CommitResult{}, err
	}

	message := journalMessage(operation, "state")
	return m.withCAS(ctx, op, func(string) (*commitPlan, error) {
		return &commitPlan{add: staged, message: message}, nil
	})
}

// relativeExistingPaths reduces caller paths to sorted, deduplicated,
// vault-relative, in-scope paths that exist in the working tree right now.
func (m *Manager) relativeExistingPaths(paths []string) ([]string, error) {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(m.root, filepath.FromSlash(p))
		}
		rel, err := filepath.Rel(m.root, abs)
		if err != nil {
			return nil, errors.New("a path could not be expressed relative to the vault root")
		}
		slash := filepath.ToSlash(rel)
		if !InScope(slash) || seen[slash] {
			continue
		}
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		seen[slash] = true
		out = append(out, slash)
	}
	sort.Strings(out)
	return out, nil
}

// CommitRestore snapshots the full working tree after an explicitly requested
// backup restore. It records removals without a journal because the archive
// restoration is a whole-vault operation rather than a note mutation.
func (m *Manager) CommitRestore(ctx context.Context, operation string) (CommitResult, error) {
	const op = "commit-restore"
	release, err := m.acquire(safetyLockName, op, ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	if _, err := m.recoverIntentLocked(ctx, op); err != nil {
		return CommitResult{}, err
	}
	if err := m.checkRepositoryState(op); err != nil {
		return CommitResult{}, err
	}

	message := journalMessage(operation, "restore")
	return m.withCAS(ctx, op, func(head string) (*commitPlan, error) {
		return m.planRestore(ctx, op, message)
	})
}

func (m *Manager) planRestore(ctx context.Context, op, message string) (*commitPlan, error) {
	entries, err := m.git.status(ctx, m.indexPath)
	if err != nil {
		return nil, safetyErrorf(KindRepositoryState, op, "the working tree could not be inspected: %s", agent.SanitizedDiagnostic(err))
	}
	changes, err := classifyPending(entries)
	if err != nil {
		return nil, m.latch(withOp(err, op))
	}
	plan := &commitPlan{message: message}
	for _, change := range changes {
		if !inTrackedScope(change) {
			continue
		}
		switch change.kind {
		case changeAdded, changeModified:
			plan.add = append(plan.add, change.path)
		case changeDeleted:
			plan.remove = append(plan.remove, change.path)
		}
	}
	if len(plan.add) == 0 && len(plan.remove) == 0 {
		return nil, nil
	}
	sort.Strings(plan.add)
	sort.Strings(plan.remove)
	return plan, nil
}

// matchesWorktree reports whether the working tree currently holds what a
// journal record says the mutation produced.
func (m *Manager) matchesWorktree(record journal.FileRecord) (bool, error) {
	abs := filepath.Join(m.root, filepath.FromSlash(record.Path))
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return record.After.Kind == journal.KindMissing, nil
	case err != nil:
		return false, err
	}
	switch {
	case info.Mode().IsRegular():
		if record.After.Kind != journal.KindFile {
			return false, nil
		}
		sum, size, err := fileDigest(abs)
		if err != nil {
			return false, err
		}
		return sum == record.After.SHA256 && size == record.After.Size, nil
	case info.IsDir():
		return record.After.Kind == journal.KindDirectory, nil
	default:
		return record.After.Kind == journal.KindOther, nil
	}
}

// fileDigest hashes a file without holding it in memory. Vault attachments can
// be large and this runs on every mutation.
func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	hasher := sha256.New()
	size, err := io.Copy(hasher, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// A recreated path aborts before commit creation, so that retry owes no floor.
func (m *Manager) abandonStaleExternalCommit(err error, startedAt time.Time) {
	if KindOf(err) == KindStaleRequest && !startedAt.IsZero() {
		_, _ = m.state.AbandonExternalCommit(m.policy(), startedAt)
	}
}
