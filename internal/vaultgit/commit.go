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
// Additions and modifications are accepted on sight, because recording them can
// only add content to the history. A deletion is accepted only when an
// uncommitted finalized journal record proves that exact transition, so a note
// that vanished for any other reason stops the snapshot instead of being
// committed. That refusal is the entire reason this package exists.
//
// It never pushes. The remote is not part of local correctness, and a snapshot
// that waited on a network round trip would make saving a note as slow and as
// failure-prone as the least reliable link in the user's setup.
func (m *Manager) CommitPending(ctx context.Context) (CommitResult, error) {
	const op = "commit-pending"
	release, err := m.acquire(safetyLockName, op)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(op); err != nil {
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
		set, err := m.scanPending(ctx, head, op)
		if err != nil {
			return nil, err
		}
		if set.empty() {
			return nil, nil
		}
		// Write-ahead the floor's first marker before anything below can produce a
		// commit. Once the ref lands, the still-live recovery intent is updated with
		// that observed bound and remains until NoteCommit publishes it. Thus a
		// lost completion write leaves a recoverable landed intent rather than a
		// shortened floor.
		//
		// Its failure stops the attempt, exactly as a failed intent record does and
		// for a sibling reason — a snapshot that cannot say when it happened is one
		// the next burst would be free to repeat inside the vault's own interval.
		// Nothing has been written to the repository at this point, the burst stays
		// open, and the next poll retries.
		at := m.now()
		if _, err := m.state.BeginExternalCommit(m.policy(), at); err != nil {
			return nil, safetyErrorf(KindRepositoryState, op,
				"the external autosave floor could not be anchored, so no snapshot was attempted: %s", agent.SanitizedDiagnostic(err))
		}
		anchoredAt = at
		plan := &commitPlan{
			// External autosaves deliberately retain their write-ahead timestamp.
			// Explicit mutations leave at zero and choose theirs at commit creation.
			// The floor itself is anchored later, at the observed ref landing; see
			// stageAndCommit and recoverIntentLocked.
			at:           at,
			add:          append(append([]string{}, set.added...), set.modified...),
			remove:       set.deleted,
			proofEntries: set.proofEntries,
			message:      autosaveMessage(at),
			// The external scan is the one entry point whose additions are
			// discovered rather than named, so it is the one that may skip an
			// addition the engine cannot read.
			externalAdditions: true,
		}
		sort.Strings(plan.add)
		return plan, nil
	})
	if err != nil {
		// The marker stays. An attempt that failed on the way to the repository may
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
		if _, err := m.state.NoteCommit(m.policy(), result.landedAt); err == nil {
			// An external intent remains live until watch.json durably names the
			// post-ref anchor. If clearing fails, the next recovery pass proves the
			// commit landed, writes the same anchor again, and retires it.
			_ = m.clearIntent()
		}
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

// CommitJournalEntry records one finalized BrainNotes mutation.
//
// It stages exactly the paths the journal entry names plus the entry's own
// files, and nothing else — a note the user happened to be editing in another
// window is not swept into a commit labelled as a rename. Before staging
// anything it verifies both ends of every record: the working tree must hold
// what the entry says it produced, and HEAD must hold what the entry says it
// replaced. A mismatch means the world moved between the mutation and this
// call, which is a stale request rather than damage — nothing is committed,
// nothing is blocked, and the next pending-change snapshot picks up whatever
// the vault actually holds now.
func (m *Manager) CommitJournalEntry(ctx context.Context, entryID, operation string) (CommitResult, error) {
	const op = "commit-journal"
	if err := validEntryID(entryID); err != nil {
		return CommitResult{}, safetyErrorf(KindStaleRequest, op, "%s", err.Error())
	}
	release, err := m.acquire(safetyLockName, op)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(op); err != nil {
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

	message := journalMessage(operation, entryID)
	return m.withCAS(ctx, op, func(head string) (*commitPlan, error) {
		plan := &commitPlan{message: message}
		var deletions []string
		for _, record := range entry.Files {
			if !InScope(record.Path) {
				continue
			}
			ok, err := m.matchesWorktree(record)
			if err != nil {
				return nil, safetyErrorf(KindStaleRequest, op, "%q could not be read to confirm the recorded change, so nothing was committed", record.Path)
			}
			if !ok {
				return nil, safetyErrorf(KindStaleRequest, op, "%q no longer matches what journal entry %s recorded, so nothing was committed", record.Path, entryID)
			}
			switch record.After.Kind {
			case journal.KindFile:
				plan.add = append(plan.add, record.Path)
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
		plan.add = append(plan.add, entryFiles...)
		sort.Strings(plan.add)

		if len(deletions) > 0 {
			// The deletion proof is this entry itself, and it only counts while it is
			// still uncommitted: the manifest is being staged in this very commit, so
			// the record and the removal it authorizes land together.
			authority := &deletionAuthority{byPath: map[string][]deletionProof{}}
			authority.add(entry)
			unexplained, provers, err := m.verifyDeletions(ctx, head, deletions, authority)
			if err != nil {
				return nil, safetyErrorf(KindRepositoryState, op, "a recorded deletion could not be checked against the history: %s", agent.SanitizedDiagnostic(err))
			}
			if len(unexplained) > 0 {
				return nil, m.latch(unexplainedDeletionError(op, unexplained))
			}
			plan.remove = deletions
			plan.proofEntries = provers
			sort.Strings(plan.remove)
		}
		if len(plan.add) == 0 && len(plan.remove) == 0 {
			return nil, nil
		}
		return plan, nil
	})
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
// staged as a removal, because a state file that vanished is exactly the kind of
// disappearance CommitPending is supposed to stop and refuse, not something this
// path may quietly ratify. Out-of-scope paths are dropped for the same reason
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

	release, err := m.acquire(safetyLockName, op)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(op); err != nil {
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

// CommitRestore records a wholesale archive restore.
//
// This is the one place where a deletion is committed without an uncommitted
// journal record proving it, and the exception is deliberate and narrow. A
// backup restore is a person naming an archive and saying "make the vault look
// like this"; every removal it performs is authorized by that sentence, and
// there is no journal entry to authorize it with because a restore replaces the
// tree rather than editing notes one at a time. Refusing here would leave the
// vault blocked immediately after the operation a person reaches for precisely
// when things have already gone wrong.
//
// What it does not do is widen the rule for anything else. The authorization is
// the caller's, it is not persisted, and the next snapshot through any other
// entry point demands journal proof again exactly as before.
func (m *Manager) CommitRestore(ctx context.Context, operation string) (CommitResult, error) {
	const op = "commit-restore"
	release, err := m.acquire(safetyLockName, op)
	if err != nil {
		return CommitResult{}, err
	}
	defer release()

	if err := m.checkBlocked(op); err != nil {
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
	})
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
