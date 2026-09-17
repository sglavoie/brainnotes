package vaultgit

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
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
// The classification is deliberately coarse and refuses anything it does not
// recognize. Three states matter and everything else is an error:
//
//   - added and modified are safe by construction, because committing them only
//     ever adds content to the history;
//   - deleted is the one that needs proof, so it is separated out and never
//     inferred from a code that could mean something else;
//   - anything ambiguous — a type change, a rename pair, an unmerged entry, a
//     status code this parser does not know — is refused rather than guessed at.
//     A wrong guess here is how a note stops being in the history.
//
// The index is seeded from HEAD, so the "staged" column is blank for every
// path. A code that appears in it anyway means the index is not what this
// function was promised, and that is treated as ambiguity rather than ignored.
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
// leave an unexplained deletion pending forever, blocking nothing and committing
// nothing.
func inTrackedScope(change pendingChange) bool {
	return change.tracked || InScope(change.path)
}

// deletionProof is one journal record that authorizes removing a path.
type deletionProof struct {
	// entryID is the journal entry the record came from.
	entryID string
	// sha256 and size are the content the journal snapshotted before the
	// deletion. Both must equal what HEAD holds for the proof to apply.
	sha256 string
	size   int64
}

// deletionAuthority is the set of deletions the pending journal records
// authorize, keyed by vault-relative path.
type deletionAuthority struct {
	byPath map[string][]deletionProof
	// entryIDs are the journal entries that contributed, for reporting.
	entryIDs []string
}

// collectAuthority loads the finalized journal records among a pending change
// set and indexes every deletion they attest to.
//
// Only *uncommitted* records count, and that restriction is the whole point.
// A journal record already in the history proves a transition that already
// happened; letting it authorize a fresh deletion would mean deleting a note,
// recreating it, and deleting it again by hand would pass as intended. Binding
// the proof to the same pending change set means the record and the deletion it
// explains land in the same commit, so the history always contains its own
// justification.
//
// A manifest that does not load — truncated, mid-write, or malformed — simply
// contributes no proof. It is not an error here: the deletion it would have
// explained stays unexplained and blocks, which is the correct conservative
// outcome, and the manifest itself is still committed as an ordinary addition.
func collectAuthority(journalRoot string, changes []pendingChange) *deletionAuthority {
	authority := &deletionAuthority{byPath: map[string][]deletionProof{}}
	for _, change := range changes {
		if change.kind == changeDeleted {
			continue
		}
		id, isManifest := journalEntryID(change.path)
		if !isManifest {
			continue
		}
		entry, err := journal.LoadEntry(journalRoot, id)
		if err != nil {
			continue
		}
		authority.add(entry)
	}
	sort.Strings(authority.entryIDs)
	return authority
}

// add indexes every delete-side record in one finalized entry. A record counts
// only when it moves a real file to nothing: `Before` must be a file with a
// hash, and `After` must be missing. A rename's delete side has exactly that
// shape, which is why renames need no special case — the new path is an
// ordinary addition and the old path is an ordinary proven deletion.
func (a *deletionAuthority) add(entry *journal.Entry) {
	contributed := false
	for _, record := range entry.Files {
		if record.Before.Kind != journal.KindFile || record.After.Kind != journal.KindMissing {
			continue
		}
		if record.Before.SHA256 == "" {
			continue
		}
		a.byPath[record.Path] = append(a.byPath[record.Path], deletionProof{
			entryID: entry.ID,
			sha256:  record.Before.SHA256,
			size:    record.Before.Size,
		})
		contributed = true
	}
	if contributed {
		a.entryIDs = append(a.entryIDs, entry.ID)
	}
}

// authorize reports the journal entry proving that removing path drops exactly
// the content HEAD holds for it. Both the hash and the size must match: the
// size alone is worthless and the hash alone is enough in practice, but a
// mismatch in either is a signal that the record does not describe this file
// and the deletion should be refused.
func (a *deletionAuthority) authorize(path, headSHA256 string, headSize int64) (string, bool) {
	for _, proof := range a.byPath[path] {
		if proof.sha256 == headSHA256 && proof.size == headSize {
			return proof.entryID, true
		}
	}
	return "", false
}

// headContent reports the sha256 and byte length of what a commit holds for
// each path, which is the content a deletion of that path would drop from the
// tip. A path the commit does not hold is absent from the result.
func (m *Manager) headContent(ctx context.Context, commit string, paths []string) (map[string]deletionProof, error) {
	entries, err := m.git.lsTree(ctx, commit, paths)
	if err != nil {
		return nil, err
	}
	content := make(map[string]deletionProof, len(entries))
	for path, entry := range entries {
		// A gitlink or a symlink is not a file whose bytes the journal could have
		// snapshotted, so no proof can ever match one. Refusing here keeps the
		// mismatch a deliberate decision rather than an accidental hash miss.
		if entry.mode != "100644" && entry.mode != "100755" {
			continue
		}
		sum, size, err := m.git.blobDigest(ctx, entry.oid)
		if err != nil {
			return nil, err
		}
		content[path] = deletionProof{sha256: sum, size: size}
	}
	return content, nil
}

// verifyDeletions checks every pending deletion against the journal records in
// the same change set.
//
// It reports the paths nothing explains and, for the rest, which journal entry
// did the explaining. The caller needs that second answer: a proof only counts
// while it is uncommitted, so an entry whose deletion is held back for any
// reason must be held back with it.
func (m *Manager) verifyDeletions(ctx context.Context, commit string, deleted []string, authority *deletionAuthority) (unexplained []string, provers map[string]bool, err error) {
	provers = map[string]bool{}
	if len(deleted) == 0 {
		return nil, provers, nil
	}
	content, err := m.headContent(ctx, commit, deleted)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range deleted {
		head, ok := content[path]
		if !ok {
			// The path is gone from the working tree and is not a regular file in
			// HEAD either. There is nothing to prove and nothing to lose, but it is
			// also not the shape this engine understands, so it is refused.
			unexplained = append(unexplained, path)
			continue
		}
		entryID, ok := authority.authorize(path, head.sha256, head.size)
		if !ok {
			unexplained = append(unexplained, path)
			continue
		}
		provers[entryID] = true
	}
	sort.Strings(unexplained)
	return unexplained, provers, nil
}

// unexplainedDeletionError builds the refusal, including the manual recovery
// steps that go into the block marker. The steps name object identifiers rather
// than describing content, so a person can restore the exact bytes without this
// package ever having read them.
func unexplainedDeletionError(op string, paths []string) *SafetyError {
	return &SafetyError{
		Kind:   KindUnexplainedDeletion,
		Op:     op,
		Reason: "a tracked path is missing from the working tree and no uncommitted BrainNotes journal record proves the deletion was intended",
		Paths:  paths,
	}
}

func deletionRecovery(paths []string) []string {
	steps := []string{
		"Nothing was committed and nothing was pushed: the listed paths are still present in the Git history at HEAD.",
		"Inspect what the history holds with `git -C <vault> show HEAD:<path>`.",
		"To restore a path, run `git -C <vault> checkout HEAD -- <path>`; to accept the deletion, delete it deliberately through `bn` so a journal record is written.",
		"BrainNotes mutations stay refused until the working tree matches the history or the block marker is removed by hand.",
	}
	if len(paths) > 0 {
		steps = append(steps, "Paths involved: "+strings.Join(paths, ", "))
	}
	return steps
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
