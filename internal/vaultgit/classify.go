package vaultgit

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/journal"
)

// changeKind is what the engine decided a pending path is.
type changeKind string

const (
	changeAdded    changeKind = "added"
	changeModified changeKind = "modified"
	changeDeleted  changeKind = "deleted"
)

// pendingChange is one path the working tree holds differently from HEAD.
type pendingChange struct {
	// path is vault-relative with forward slashes, exactly as git reported it.
	path string
	kind changeKind
	// tracked reports whether HEAD holds this path.
	tracked bool
}

// classifyPending reads the working tree against a HEAD-seeded index and
// decides what each difference is.
//
// Additions, modifications and regular-file deletions are ordinary changes.
// Type changes, unmerged entries and an unexpected staged column are refused:
// the private index must have been seeded from HEAD immediately before scanning.
func classifyPending(entries []statusEntry) ([]pendingChange, error) {
	changes := make([]pendingChange, 0, len(entries))
	var ambiguous []string
	var unmerged []string
	for _, entry := range entries {
		code := string([]byte{entry.x, entry.y})
		switch {
		case code == "??":
			changes = append(changes, pendingChange{path: entry.path, kind: changeAdded})
		case entry.x == 'U' || entry.y == 'U' || code == "AA" || code == "DD":
			unmerged = append(unmerged, entry.path)
		case entry.x != ' ':
			// The index was seeded from HEAD immediately before this ran, so a
			// staged difference means something wrote to the private index between
			// the seed and the scan. Refuse rather than commit against an index
			// whose provenance is unknown.
			ambiguous = append(ambiguous, entry.path)
		case entry.y == 'M':
			changes = append(changes, pendingChange{path: entry.path, kind: changeModified, tracked: true})
		case entry.y == 'D':
			changes = append(changes, pendingChange{path: entry.path, kind: changeDeleted, tracked: true})
		default:
			// 'T' (a file became a symlink or a directory), 'R', 'C', and anything
			// a future git adds all land here. A file replaced by a directory in
			// particular looks like a deletion plus an untracked add, and treating
			// it as either would be a guess about intent.
			ambiguous = append(ambiguous, entry.path)
		}
	}
	if len(unmerged) > 0 {
		sort.Strings(unmerged)
		return nil, &SafetyError{
			Kind:   KindRepositoryState,
			Op:     "classify",
			Reason: "the repository has unmerged paths, so BrainNotes will not snapshot it until the conflict is resolved",
			Paths:  unmerged,
		}
	}
	if len(ambiguous) > 0 {
		sort.Strings(ambiguous)
		return nil, &SafetyError{
			Kind:   KindAmbiguousChange,
			Op:     "classify",
			Reason: "git reported a change this engine will not classify (a type change, a rename pair, or an unstaged-index disagreement)",
			Paths:  ambiguous,
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	return changes, nil
}

// inTrackedScope reports whether a pending change belongs to BrainNotes.
//
// The scope policy narrows additions only. A path already tracked in HEAD is
// always in scope, whatever the policy says, because the alternative is worse in
// both directions: a tracked file whose modification we skipped would drift out
// of the history silently, and a tracked file whose deletion we skipped would
// leave its deletion pending forever.
func inTrackedScope(change pendingChange) bool {
	return change.tracked || InScope(change.path)
}

// deletionJournalEntries associates pending removals with their finalized journal
// entries. This keeps a deferred removal and its undo record in the same commit;
// a journal is not required to authorize an external deletion.
func deletionJournalEntries(journalRoot string, changes []pendingChange) map[string]bool {
	deleted := map[string]bool{}
	for _, change := range changes {
		if change.kind == changeDeleted {
			deleted[change.path] = true
		}
	}
	entries := map[string]bool{}
	for _, change := range changes {
		if change.kind == changeDeleted {
			continue
		}
		id, manifest := journalEntryID(change.path)
		if !manifest {
			continue
		}
		entry, err := journal.LoadEntry(journalRoot, id)
		if err != nil {
			continue
		}
		if journalDeletesAny(entry, deleted) {
			entries[id] = true
		}
	}
	return entries
}

// deletionRecovery describes markers written by older versions. Normal
// deletions no longer latch a block; valid legacy markers migrate automatically.
func deletionRecovery(paths []string) []string {
	return []string{
		"This deletion block was written under the old journal-proof policy.",
		"Update BrainNotes and its guard together; the next safety check revalidates the repository and clears this obsolete block.",
		"Deleted content already committed remains recoverable from Git history.",
		"Paths involved: " + strings.Join(paths, ", "),
	}
}

// holdsJournalEntry reports whether commit already contains entryID's manifest,
// which is the history's own record that the entry was committed.
//
// The manifest is the right thing to ask about because it is committed with the
// change it describes and never without it: CommitJournalEntry stages the
// entry's files alongside the paths it names, in one commit. So a manifest in
// the tree means the whole mutation is already history, and its absence means
// none of it is.
//
// An unborn branch holds nothing, and asking git about a commit that does not
// exist is an error rather than an answer, so it is answered here.
func (m *Manager) holdsJournalEntry(ctx context.Context, commit, entryID string) (bool, error) {
	if strings.TrimSpace(commit) == "" {
		return false, nil
	}
	rel := bnotesJournalPrefix + entryID + "/" + journal.ManifestFileName
	entries, err := m.git.lsTree(ctx, commit, []string{rel})
	if err != nil {
		return false, err
	}
	entry, ok := entries[rel]
	return ok && (entry.mode == "100644" || entry.mode == "100755"), nil
}

// journalEntryFiles lists the vault-relative paths that make up one journal
// entry directory: the manifest and every snapshot under it.
//
// The whole directory is staged, not just the manifest, because a manifest
// whose snapshots are missing is a record of an undo that can no longer be
// performed. `bn undo` reads the snapshot files, so committing one without the
// other would put a promise in the history that the history cannot keep.
func journalEntryFiles(journalRoot, entryID string) ([]string, error) {
	dir := filepath.Join(journalRoot, entryID)
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, bnotesJournalPrefix+entryID+"/"+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("vaultgit: read journal entry %q: %w", entryID, err)
	}
	sort.Strings(out)
	return out, nil
}

func journalDeletesAny(entry *journal.Entry, deleted map[string]bool) bool {
	for _, record := range entry.Files {
		if deleted[record.Path] && record.Before.Kind == journal.KindFile && record.After.Kind == journal.KindMissing {
			return true
		}
	}
	return false
}
