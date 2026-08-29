package vaultgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// defaultRemote is the remote a vault is pushed to when the caller names none.
const defaultRemote = "origin"

// casAttempts bounds how many times a commit is rebuilt after losing a
// compare-and-swap race. Losing twice in a row means another writer is
// committing continuously; a third rebuild would more likely be a livelock than
// a success, and refusing is safe because nothing has been discarded.
const casAttempts = 3

// Options configure a Manager. The zero value is usable: every field has a
// default that suits a real vault.
type Options struct {
	// GitExecutable overrides PATH lookup. Tests use it to point at a stub.
	GitExecutable string
	// Remote names the push target. Defaults to "origin".
	Remote string
	// AuthorName and AuthorEmail stamp engine commits. They default to a fixed
	// machine identity rather than the user's, so the history distinguishes what
	// BrainNotes committed from what the user committed by hand.
	AuthorName  string
	AuthorEmail string
	// Now supplies the clock. Tests replace it to make commit messages and retry
	// schedules deterministic.
	Now func() time.Time
}

// Manager is the Git safety engine for one vault.
//
// Every method is safe to call concurrently with methods on a Manager in
// another process: the mutation path is serialized by a file lock and the ref
// move is a compare-and-swap, so two processes snapshotting the same vault
// interleave correctly rather than overwriting each other. Methods on the same
// Manager value are not internally serialized beyond that lock, which is
// sufficient because the lock is what actually protects the repository.
type Manager struct {
	// root is the vault root, which must also be the Git working tree top level.
	root string
	// gitDir is the repository directory.
	gitDir string
	// journalRoot is where finalized journal entries live on disk.
	journalRoot string

	git   *gitExec
	state *safetyState

	remote string
	who    identity
	now    func() time.Time

	// indexPath is the private index. It is re-seeded from HEAD at the start of
	// every operation and is never the user's index.
	indexPath string
}

// Open prepares the safety engine for a vault.
//
// A vault with no `.git` entry at all is not an error: it reports ErrUnmanaged
// and the caller carries on without content history. Everything else about a
// repository that exists but cannot be used safely — a `.git` file instead of a
// directory, a missing or detached HEAD, a working tree whose top level is not
// the vault root — is a typed *SafetyError, because in those cases the user
// believes they have history and the engine must say plainly that it will not
// provide it.
//
// Open performs no mutation and takes no lock. It does not read the block
// marker either: a blocked vault still opens, so a caller can report why it is
// blocked. Preflight is what refuses.
func Open(ctx context.Context, vaultRoot string, opts Options) (*Manager, error) {
	if strings.TrimSpace(vaultRoot) == "" {
		return nil, errors.New("vaultgit: vault root is required")
	}
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return nil, fmt.Errorf("vaultgit: resolve vault root: %w", err)
	}

	info, err := os.Lstat(filepath.Join(root, ".git"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, ErrUnmanaged
	case err != nil:
		return nil, safetyErrorf(KindRepositoryState, "open", "the vault's .git entry could not be inspected: %s", agent.SanitizedDiagnostic(err))
	case !info.IsDir():
		// A `.git` file points elsewhere: a linked worktree, a submodule, or a
		// separate git-dir layout. Each is a valid Git arrangement and none is one
		// this engine has been tested against, so it refuses rather than guessing
		// which directory it is allowed to write state into.
		return nil, safetyErrorf(KindRepositoryState, "open", "the vault's .git is a file rather than a directory, which this engine does not manage")
	}

	exe := strings.TrimSpace(opts.GitExecutable)
	if exe == "" {
		exe, err = exec.LookPath("git")
		if err != nil {
			return nil, safetyErrorf(KindGitUnavailable, "open", "the git executable could not be found on PATH")
		}
	}

	m := &Manager{
		root:        root,
		journalRoot: filepath.Join(root, filepath.FromSlash(config.DefaultJournalDir)),
		git:         &gitExec{exe: exe, dir: root},
		remote:      strings.TrimSpace(opts.Remote),
		who: identity{
			name:  strings.TrimSpace(opts.AuthorName),
			email: strings.TrimSpace(opts.AuthorEmail),
		},
		now: opts.Now,
	}
	if m.remote == "" {
		m.remote = defaultRemote
	}
	if m.who.name == "" {
		m.who.name = "BrainNotes"
	}
	if m.who.email == "" {
		m.who.email = "brainnotes@localhost"
	}
	if m.now == nil {
		m.now = time.Now
	}

	if err := m.resolveLayout(ctx); err != nil {
		return nil, err
	}
	// HEAD must name a branch. A detached HEAD is refused rather than handled:
	// committing onto it would build a history no ref points at, which is a
	// backup that garbage collection is entitled to delete.
	ref, err := m.git.symbolicHead(ctx)
	if err != nil {
		return nil, safetyErrorf(KindRepositoryState, "open", "the repository HEAD could not be read: %s", agent.SanitizedDiagnostic(err))
	}
	if ref == "" {
		return nil, safetyErrorf(KindRepositoryState, "open", "the repository HEAD is detached, so there is no branch to record snapshots on")
	}
	return m, nil
}

// resolveLayout asks git where it thinks it is and checks the answer against
// where the caller said the vault is.
//
// The top-level check is the load-bearing one. If the vault is a subdirectory
// of some larger repository — a dotfiles repo, a home directory someone put
// under version control — then every path this engine computes would be
// relative to the wrong root, and it would happily commit the vault into a
// history that is not the vault's. Refusing is the only safe answer.
func (m *Manager) resolveLayout(ctx context.Context) error {
	out, err := m.git.run(ctx, gitCall{args: []string{"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir"}})
	if err != nil {
		return safetyErrorf(KindRepositoryState, "open", "the vault's Git repository is not usable: %s", agent.SanitizedDiagnostic(err))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[1]) == "" {
		return safetyErrorf(KindRepositoryState, "open", "the repository layout could not be resolved")
	}
	toplevel := strings.TrimSpace(lines[0])
	gitDir := strings.TrimSpace(lines[1])

	// Compare through EvalSymlinks: on macOS a vault under /tmp is reached via a
	// /private/var symlink, so the literal strings differ for the same directory.
	if !sameDir(toplevel, m.root) {
		return safetyErrorf(KindRepositoryState, "open", "the vault is not the top level of its Git working tree, so BrainNotes will not manage it")
	}
	m.gitDir = gitDir
	m.state = newSafetyState(gitDir)
	m.indexPath = m.state.path(indexFileName)
	return nil
}

func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// Root reports the vault root.
func (m *Manager) Root() string { return m.root }

// StateDir reports where operational state is kept, so an operator can be
// pointed at the exact directory holding the block marker.
func (m *Manager) StateDir() string { return m.state.Dir() }

// Blocked reports the persisted block marker, if any. Unlike Preflight it takes
// no lock and never fails on a busy vault, so a status command can always
// explain a refusal.
func (m *Manager) Blocked() (BlockRecord, bool) {
	record, blocked, _ := m.state.LoadBlock()
	return record, blocked
}

// ClearBlock removes the block marker.
//
// It is deliberately the only way out of a block and it is deliberately not
// automatic: a block means the engine saw something it would not classify, and
// the condition that caused it is still on disk. Clearing without fixing the
// working tree only means the next preflight re-derives the same verdict, which
// is the point — the marker reports the refusal, it never causes it.
func (m *Manager) ClearBlock() error { return m.state.ClearBlock() }

// PushStatus reports the remote retry state.
func (m *Manager) PushStatus() (PushState, bool) { return m.state.LoadPushState() }

// NoteExternalChange records that the working tree changed, opening a debounce
// burst if none is open.
func (m *Manager) NoteExternalChange() (WatchState, error) {
	return m.state.NoteChange(m.now())
}

// ExternalCommitDue reports whether an open debounce burst has reached either
// its quiet period or its maximum latency.
func (m *Manager) ExternalCommitDue() bool {
	return m.state.LoadWatchState().DueAt(m.now())
}

// PushDue reports whether a retry is worth attempting now. A vault with no
// recorded failure is always due.
func (m *Manager) PushDue() bool {
	state, ok := m.state.LoadPushState()
	if !ok {
		return true
	}
	return state.DueAt(m.now())
}

// CommitResult describes what a snapshot did. It is safe to log in full: it
// carries paths and object names, never contents.
type CommitResult struct {
	// Committed reports whether a new commit was created. A snapshot that finds
	// nothing to record is a success with Committed false.
	Committed bool
	// Commit is the new commit's object name, when one was created.
	Commit string
	// Parent is the commit the snapshot was built on, empty for a root commit.
	Parent string
	// Branch is the full ref the snapshot advanced.
	Branch string
	// Message is the commit message that was used.
	Message string
	// Added lists the vault-relative paths whose content was recorded.
	Added []string
	// Deleted lists the vault-relative paths whose removal was recorded. Every
	// one of them was proven by an uncommitted finalized journal record.
	Deleted []string
	// Skipped lists paths the engine could not read and therefore left out. A
	// skip never removes anything from the history, but it does suppress every
	// deletion in the same pass — see commitPending.
	Skipped []SkippedPath
	// DeferredDeletions lists proven deletions that were held back because
	// something else in the same pass was skipped.
	DeferredDeletions []string
}

// SkippedPath is one path a snapshot could not read.
type SkippedPath struct {
	// Path is vault-relative.
	Path string
	// Reason is a sanitized single-line diagnostic from git.
	Reason string
}

// Preflight reports whether a mutation may proceed.
//
// It is the gate the mutating CLI paths call before touching a note, and it
// refuses on four conditions: a persisted block marker, another process already
// inside a safety operation, a repository state this engine will not write to,
// and any pending deletion that no uncommitted journal record explains. The
// last one is the reason preflight exists at all — if a note vanished outside
// BrainNotes, the next `bn` mutation would otherwise snapshot the working tree
// and make that disappearance permanent.
func (m *Manager) Preflight(ctx context.Context) error {
	release, err := m.acquire(safetyLockName, "preflight")
	if err != nil {
		return err
	}
	defer release()
	return m.preflightLocked(ctx, "preflight")
}

// preflightLocked runs the checks that require the safety lock already held.
func (m *Manager) preflightLocked(ctx context.Context, op string) error {
	if err := m.checkBlocked(op); err != nil {
		return err
	}
	if err := m.checkRepositoryState(op); err != nil {
		return err
	}
	head, _, err := m.headState(ctx, op)
	if err != nil {
		return err
	}
	if head == "" {
		// An unborn branch has nothing to delete from, so there is nothing to
		// prove and nothing that can be lost.
		return nil
	}
	if err := m.seedIndex(ctx, head, op); err != nil {
		return err
	}
	_, err = m.scanPending(ctx, head, op)
	return err
}

// checkBlocked converts a persisted marker into a refusal.
func (m *Manager) checkBlocked(op string) error {
	record, blocked, readErr := m.state.LoadBlock()
	if !blocked {
		return nil
	}
	return &SafetyError{
		Kind:   KindBlocked,
		Op:     op,
		Reason: record.Reason,
		Paths:  record.Paths,
		Err:    readErr,
	}
}

// inProgressMarkers are the files Git leaves in the repository directory while
// a multi-step operation is unfinished. Snapshotting during one would commit a
// half-applied merge or rebase as if it were the user's intended content.
var inProgressMarkers = []string{
	"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG",
	"rebase-merge", "rebase-apply",
}

func (m *Manager) checkRepositoryState(op string) error {
	for _, name := range inProgressMarkers {
		if _, err := os.Lstat(filepath.Join(m.gitDir, name)); err == nil {
			return safetyErrorf(KindRepositoryState, op,
				"a Git operation is in progress in this vault (%s), so BrainNotes will not snapshot it until that finishes", name)
		}
	}
	return nil
}

// acquire takes one of the package's two leaf locks.
//
// Neither lock is ever held while the other is, and this package never acquires
// a vault lock, so it cannot participate in a cycle with internal/vault's
// per-path locks. The ordering rule for callers is stated once, in doc.go:
// vault locks first, safety lock last, never the reverse.
func (m *Manager) acquire(name, op string) (func(), error) {
	if err := m.state.ensureDir(); err != nil {
		return nil, err
	}
	release, acquired, err := vault.TryLockPath(m.state.path(name))
	if err != nil {
		return nil, safetyErrorf(KindRepositoryState, op, "the %s could not be taken: %s", name, agent.SanitizedDiagnostic(err))
	}
	if !acquired {
		return nil, safetyErrorf(KindBusy, op, "another BrainNotes process is already performing a Git safety operation on this vault")
	}
	return release, nil
}

// headState reports the current branch ref and the commit it points at. An
// empty commit means the branch is unborn, which is a legitimate state: a
// freshly initialized vault gets its root commit from the first snapshot.
func (m *Manager) headState(ctx context.Context, op string) (head, ref string, err error) {
	ref, err = m.git.symbolicHead(ctx)
	if err != nil {
		return "", "", safetyErrorf(KindRepositoryState, op, "the repository HEAD could not be read: %s", agent.SanitizedDiagnostic(err))
	}
	if ref == "" {
		return "", "", safetyErrorf(KindRepositoryState, op, "the repository HEAD is detached, so there is no branch to record snapshots on")
	}
	head, err = m.git.revParse(ctx, ref)
	if err != nil {
		return "", ref, nil
	}
	return head, ref, nil
}

// seedIndex resets the private index to exactly what a commit holds.
//
// The old file is removed first rather than overwritten. `read-tree` would
// replace its contents anyway, but a run that died between staging and
// committing leaves an index whose entries are neither HEAD's nor the working
// tree's, and starting from a file that provably has no history is cheaper to
// reason about than starting from one that probably does not matter.
func (m *Manager) seedIndex(ctx context.Context, head, op string) error {
	if err := os.Remove(m.indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return safetyErrorf(KindRepositoryState, op, "the private Git index could not be reset: %s", agent.SanitizedDiagnostic(err))
	}
	if head == "" {
		// An absent index file is an empty index, which is what an unborn branch
		// should be compared against.
		return nil
	}
	if err := m.git.readTree(ctx, m.indexPath, head); err != nil {
		return safetyErrorf(KindRepositoryState, op, "the private Git index could not be seeded from HEAD: %s", agent.SanitizedDiagnostic(err))
	}
	return nil
}

// pendingSet is the classified, in-scope difference between the working tree
// and a commit, together with the deletions the pending journal records prove.
type pendingSet struct {
	added    []string
	modified []string
	deleted  []string
	// proofEntries are the journal entries whose records authorized the
	// deletions above. Their files must not be committed without them.
	proofEntries map[string]bool
}

func (p pendingSet) empty() bool {
	return len(p.added) == 0 && len(p.modified) == 0 && len(p.deleted) == 0
}

// scanPending classifies the working tree against a seeded index and refuses
// any deletion nothing explains, latching the block marker when it does.
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

	authority := collectAuthority(m.journalRoot, inScope)
	unexplained, provers, err := m.verifyDeletions(ctx, head, set.deleted, authority)
	if err != nil {
		return pendingSet{}, safetyErrorf(KindRepositoryState, op, "a pending deletion could not be checked against the history: %s", agent.SanitizedDiagnostic(err))
	}
	if len(unexplained) > 0 {
		return pendingSet{}, m.latch(unexplainedDeletionError(op, unexplained))
	}
	set.proofEntries = provers
	return set, nil
}

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
	if err := m.checkRepositoryState(op); err != nil {
		return CommitResult{}, err
	}

	result, err := m.withCAS(ctx, op, func(head string) (*commitPlan, error) {
		set, err := m.scanPending(ctx, head, op)
		if err != nil {
			return nil, err
		}
		if set.empty() {
			return nil, nil
		}
		plan := &commitPlan{
			add:          append(append([]string{}, set.added...), set.modified...),
			remove:       set.deleted,
			proofEntries: set.proofEntries,
			message:      autosaveMessage(m.now()),
		}
		sort.Strings(plan.add)
		return plan, nil
	})
	if err != nil {
		return result, err
	}
	if result.Committed {
		if _, err := m.state.NoteCommit(m.now()); err != nil {
			// The debounce clock is a scheduling hint. Losing it costs at most one
			// redundant scan, so it must not turn a successful commit into a failure.
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

// commitPlan is the exact set of index changes one snapshot will make.
type commitPlan struct {
	add    []string
	remove []string
	// proofEntries are the journal entry IDs whose records authorize the
	// removals. If a removal is held back, its proof is held back too.
	proofEntries map[string]bool
	message      string
}

// withCAS builds and lands a commit, rebuilding it from scratch if the branch
// moved underneath.
//
// The rebuild is total on purpose. A lost compare-and-swap means some other
// process committed, so this snapshot's tree was computed against a parent that
// is no longer the tip; reusing it would silently revert whatever that other
// process recorded. Re-seeding the index from the new HEAD and re-planning is
// the only correct response, and it is cheap because the objects already
// written stay written.
func (m *Manager) withCAS(ctx context.Context, op string, build func(head string) (*commitPlan, error)) (CommitResult, error) {
	for attempt := 1; ; attempt++ {
		head, ref, err := m.headState(ctx, op)
		if err != nil {
			return CommitResult{}, err
		}
		if err := m.seedIndex(ctx, head, op); err != nil {
			return CommitResult{}, err
		}
		plan, err := build(head)
		if err != nil {
			return CommitResult{}, err
		}
		if plan == nil {
			return CommitResult{Branch: ref, Parent: head}, nil
		}

		result, err := m.stageAndCommit(ctx, op, head, ref, plan)
		if err == nil {
			return result, nil
		}
		var conflict *casConflict
		if !errors.As(err, &conflict) {
			return CommitResult{}, err
		}
		if attempt >= casAttempts {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindRefConflict,
				Op:     op,
				Reason: fmt.Sprintf("the branch %s kept moving while BrainNotes tried to record a snapshot, so the snapshot was abandoned", ref),
			})
		}
	}
}

// casConflict signals that update-ref refused because the branch moved. It is
// internal to the retry loop and never escapes withCAS.
type casConflict struct{ ref string }

func (c *casConflict) Error() string { return "vaultgit: " + c.ref + " moved during the snapshot" }

// stageAndCommit realizes one plan against the private index.
func (m *Manager) stageAndCommit(ctx context.Context, op, head, ref string, plan *commitPlan) (CommitResult, error) {
	staged, skipped := m.stageAdditions(ctx, plan.add)

	removals := plan.remove
	if len(skipped) > 0 && len(removals) > 0 {
		// Something in this pass could not be read, and a rename's new side is
		// exactly the kind of thing that would be. Committing the delete side of a
		// pair whose add side was skipped would drop the content from the tip, so
		// every removal waits for a pass where nothing is skipped.
		//
		// The proof has to wait with it. A journal record authorizes a deletion
		// only while it is uncommitted, so committing the record now and the
		// deletion later would leave the later pass with an unexplained deletion —
		// the engine would block the vault over a removal it had already approved.
		// Unstaging the proving entry's files keeps the two together, which is the
		// same invariant that makes the proof meaningful in the first place.
		removals = nil
		var held []string
		for _, path := range staged {
			if id, _ := journalEntryID(path); id != "" && plan.proofEntries[id] {
				held = append(held, path)
			}
		}
		if len(held) > 0 {
			if err := m.git.stageRemove(ctx, m.indexPath, held); err != nil {
				return CommitResult{}, m.latch(&SafetyError{
					Kind:   KindCommitFailed,
					Op:     op,
					Reason: "a deferred deletion's journal record could not be held back, so nothing was committed",
					Err:    errors.New(agent.SanitizedDiagnostic(err)),
				})
			}
			staged = withoutPaths(staged, held)
		}
	}
	if err := m.git.stageRemove(ctx, m.indexPath, removals); err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "a proven deletion could not be staged, so nothing was committed",
			Paths:  removals,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}

	result := CommitResult{
		Branch:  ref,
		Parent:  head,
		Message: plan.message,
		Added:   staged,
		Deleted: removals,
		Skipped: skipped,
	}
	if len(skipped) > 0 {
		result.DeferredDeletions = plan.remove
	}
	if len(staged) == 0 && len(removals) == 0 {
		return result, nil
	}

	tree, err := m.git.writeTree(ctx, m.indexPath)
	if err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the snapshot tree could not be written, so nothing was committed",
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}
	// An identical tree means the plan's paths already match the tip. Committing
	// it would add an empty commit to a history a person has to read.
	if head != "" {
		if headTree, err := m.git.revParse(ctx, head+"^{tree}"); err == nil && headTree == tree {
			return result, nil
		}
	}

	commit, err := m.git.commitTree(ctx, tree, head, plan.message, m.who, m.now())
	if err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the snapshot commit could not be written, so nothing was recorded",
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}

	if err := m.git.updateRefCAS(ctx, ref, commit, head); err != nil {
		// Distinguish "someone else got there first" from "this repository is
		// broken" by asking where the branch is now. Only the second latches.
		if current, resolveErr := m.git.revParse(ctx, ref); resolveErr != nil || current != head {
			return CommitResult{}, &casConflict{ref: ref}
		}
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: fmt.Sprintf("the branch %s could not be advanced to the new snapshot", ref),
			OldOID: head,
			NewOID: commit,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}

	result.Committed = true
	result.Commit = commit
	m.refreshUserIndex(ctx, head, commit, staged, removals)
	return result, nil
}

// withoutPaths returns list with every member of drop removed, preserving order.
func withoutPaths(list, drop []string) []string {
	dropped := make(map[string]bool, len(drop))
	for _, path := range drop {
		dropped[path] = true
	}
	out := list[:0:0]
	for _, path := range list {
		if !dropped[path] {
			out = append(out, path)
		}
	}
	return out
}

// stageAdditions records working-tree content, skipping what it cannot read.
//
// An unreadable addition is reported rather than fatal, and the distinction is
// deliberate: recording a file is purely additive, so failing to record one
// leaves the history exactly as correct as it was a moment ago. A vault holds
// notes a user has deliberately chmod'd to 0000, and refusing to snapshot the
// entire vault because of one of them would trade a real safety net for a
// theoretical completeness guarantee.
//
// The batch is tried first because it is one process for the whole vault. Only
// when it fails does the engine fall back to one invocation per path, which is
// slow but is the only way to learn which path was the problem. Re-staging a
// path the failed batch already staged is idempotent.
func (m *Manager) stageAdditions(ctx context.Context, paths []string) (staged []string, skipped []SkippedPath) {
	if len(paths) == 0 {
		return nil, nil
	}
	if err := m.git.stageAdd(ctx, m.indexPath, paths); err == nil {
		return paths, nil
	}
	for _, path := range paths {
		if err := m.git.stageAdd(ctx, m.indexPath, []string{path}); err != nil {
			skipped = append(skipped, SkippedPath{Path: path, Reason: agent.SanitizedDiagnostic(err)})
			continue
		}
		staged = append(staged, path)
	}
	return staged, skipped
}

// refreshUserIndex updates the user's real index for the paths this snapshot
// just committed, and only where doing so cannot lose their work.
//
// Without it, an engine commit leaves `git status` showing a staged revert:
// the user's index still holds the old HEAD's entry for a note whose content
// the new HEAD has moved past, which git renders as `MM`, `AD`, or a staged
// deletion. That is alarming and, worse, invites the user to "fix" it with a
// checkout that discards the snapshot.
//
// The guard is that a path is only refreshed when the user's index still agrees
// with the *old* HEAD for it. If they staged something of their own — a
// different version, a deliberate `git add` — the entry differs and is left
// exactly as they left it. Every failure here is ignored: this is cosmetic
// repair of a file the user owns, and it must never turn a landed commit into
// a reported failure.
func (m *Manager) refreshUserIndex(ctx context.Context, oldHead, newCommit string, added, removed []string) {
	paths := append(append([]string{}, added...), removed...)
	if len(paths) == 0 {
		return
	}
	real, err := m.git.lsFilesStaged(ctx, "", paths)
	if err != nil {
		return
	}
	var old map[string]treeEntry
	if oldHead == "" {
		old = map[string]treeEntry{}
	} else if old, err = m.git.lsTree(ctx, oldHead, paths); err != nil {
		return
	}
	next, err := m.git.lsTree(ctx, newCommit, paths)
	if err != nil {
		return
	}
	for _, path := range paths {
		current, inReal := real[path]
		previous, inOld := old[path]
		if inReal != inOld {
			continue
		}
		if inReal && (current.oid != previous.oid || current.mode != previous.mode) {
			continue
		}
		if entry, ok := next[path]; ok {
			_ = m.git.setIndexEntry(ctx, "", entry)
			continue
		}
		_ = m.git.stageRemove(ctx, "", []string{path})
	}
}

// PushResult describes one remote attempt.
type PushResult struct {
	// Attempted reports whether a push was actually run. It is false when no
	// remote is configured or the branch has no commits yet.
	Attempted bool
	// Pushed reports whether the remote accepted the branch.
	Pushed bool
	// Branch is the full ref that was pushed.
	Branch string
	// Remote is the remote name.
	Remote string
	// Retry carries the recorded backoff state after a failure.
	Retry PushState
}

// Push publishes the current branch.
//
// It is deliberately not reachable from any mutation path. A push is a network
// round trip whose duration is bounded by nothing the user controls, and the
// content history is already durable on disk the moment the commit lands — so
// making a note save wait for a remote would buy no safety and cost every
// second the remote is slow. The two are separated by their own lock as well as
// by call site, so a push that hangs for its full timeout cannot delay a
// snapshot happening at the same moment.
//
// A push failure is operational, never a safety failure: it records retry state
// and returns a KindPushFailed error, and it never writes the block marker. The
// local history is complete whether or not the remote ever answers.
func (m *Manager) Push(ctx context.Context) (PushResult, error) {
	const op = "push"
	release, err := m.acquire(pushLockName, op)
	if err != nil {
		return PushResult{}, err
	}
	defer release()

	result := PushResult{Remote: m.remote}
	configured, err := m.git.remoteConfigured(ctx, m.remote)
	if err != nil {
		return result, safetyErrorf(KindPushFailed, op, "the vault's remotes could not be listed: %s", agent.SanitizedDiagnostic(err))
	}
	if !configured {
		// A vault with no remote is a supported configuration: the local history
		// is the backup. Reporting this as a failure would produce a permanent
		// error state for a vault that is working exactly as intended.
		return result, nil
	}

	ref, err := m.git.symbolicHead(ctx)
	if err != nil || ref == "" {
		return result, safetyErrorf(KindPushFailed, op, "there is no branch to push")
	}
	result.Branch = ref
	if _, err := m.git.revParse(ctx, ref); err != nil {
		// An unborn branch has nothing to publish yet.
		return result, nil
	}

	result.Attempted = true
	if err := m.git.pushBranch(ctx, m.remote, ref); err != nil {
		detail := agent.SanitizedDiagnostic(err)
		state, saveErr := m.state.RecordPushFailure(m.now(), detail)
		result.Retry = state
		return result, &SafetyError{
			Kind:   KindPushFailed,
			Op:     op,
			Reason: fmt.Sprintf("the remote %q did not accept branch %s; the local history is unaffected and the push will be retried", m.remote, ref),
			Err:    errors.Join(errors.New(detail), saveErr),
		}
	}
	if err := m.state.ClearPushState(); err != nil {
		// Failing to forget a resolved failure only costs a delayed next attempt.
		_ = err
	}
	result.Pushed = true
	return result, nil
}

// latch persists the block marker for a failure that warrants one and returns
// the error to the caller.
//
// The marker is a reporting convenience, not the safety mechanism. If this
// write fails, the condition that produced it is still on disk — the deleted
// file is still missing, the ref still will not advance — and the next
// preflight re-derives the same verdict from the same evidence. That is why a
// marker write failure is joined onto the error rather than replacing it: the
// caller still learns the real problem first.
func (m *Manager) latch(err error) error {
	var safety *SafetyError
	if !errors.As(err, &safety) || !safety.Kind.blocking() {
		return err
	}
	record := BlockRecord{
		Kind:       safety.Kind,
		Reason:     safety.Reason,
		Operation:  safety.Op,
		ObservedAt: m.now(),
		Paths:      safety.Paths,
		OldOID:     safety.OldOID,
		NewOID:     safety.NewOID,
		Recovery:   recoveryFor(safety.Kind, safety.Paths),
	}
	if saveErr := m.state.SaveBlock(record); saveErr != nil {
		safety.Err = errors.Join(safety.Err, saveErr)
	}
	return err
}

// withOp stamps an operation name onto a SafetyError raised by a helper that
// does not know which caller it is serving.
func withOp(err error, op string) error {
	var safety *SafetyError
	if errors.As(err, &safety) && safety.Op == "" {
		safety.Op = op
	}
	return err
}

// recoveryFor builds the manual procedure written into the block marker. Every
// step names a path or an object identifier and reads no note content, so the
// marker stays safe to share.
func recoveryFor(kind Kind, paths []string) []string {
	switch kind {
	case KindUnexplainedDeletion:
		return deletionRecovery(paths)
	case KindAmbiguousChange:
		steps := []string{
			"Nothing was committed and nothing was pushed.",
			"Run `git -C <vault> status` to see the change BrainNotes would not classify; it is usually a file replaced by a directory or a symlink.",
			"Resolve it by hand so the path is an ordinary file, an ordinary directory, or absent.",
			"Delete the block marker to allow BrainNotes mutations again.",
		}
		if len(paths) > 0 {
			steps = append(steps, "Paths involved: "+strings.Join(paths, ", "))
		}
		return steps
	case KindRefConflict:
		return []string{
			"Nothing was committed and nothing was pushed; the branch is wherever the other writer left it.",
			"Check for another `bn` process or watcher writing to this vault: `git -C <vault> log --oneline -10`.",
			"Once only one writer remains, delete the block marker; the next mutation snapshots the current working tree.",
		}
	case KindCommitFailed:
		return []string{
			"Nothing was committed and nothing was pushed; the working tree still holds every change.",
			"Check free disk space and the permissions on the vault's .git directory.",
			"Recover any object named in this record with `git -C <vault> cat-file blob <oid>`.",
			"Delete the block marker once the cause is fixed; the next mutation retries the snapshot.",
		}
	default:
		return nil
	}
}

// autosaveMessage names an external-change snapshot. The timestamp is the only
// variable part and it is UTC to the second, so the message is identical on
// every machine that observes the same moment and reveals nothing about what
// changed.
func autosaveMessage(now time.Time) string {
	return "brainnotes: autosave " + now.UTC().Format("2006-01-02T15:04:05Z")
}

// journalMessage names a `bn` mutation snapshot.
//
// The operation token is supplied by the caller and is scrubbed to a short
// lowercase identifier here, never derived from the journal entry. That is not
// defensive habit: journal.Entry.Command holds the full `bn` invocation, which
// contains note names, property values, and search text. Putting it in a commit
// message would publish note content to every clone of the repository.
func journalMessage(operation, entryID string) string {
	return "brainnotes: " + sanitizeOperation(operation) + " [" + entryID + "]"
}

// sanitizeOperation reduces a caller-supplied token to lowercase letters,
// digits, and single dashes, falling back to a fixed word. It is an allow-list
// rather than an escape: anything unexpected is dropped, not encoded.
func sanitizeOperation(operation string) string {
	const fallback = "mutation"
	const maxLen = 32
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(operation)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ', r == '\t', r == '\n', r == '\r':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		if b.Len() >= maxLen {
			break
		}
	}
	token := strings.Trim(b.String(), "-")
	if token == "" {
		return fallback
	}
	return token
}

// validEntryID checks that an entry ID is a plain identifier before it reaches
// a commit message or a path. Journal IDs are generated as timestamp-plus-hex,
// so anything else means the caller is passing something it should not.
func validEntryID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("a journal entry id is required")
	}
	if len(id) > 128 {
		return errors.New("the journal entry id is too long to be one")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return errors.New("the journal entry id contains characters a journal id never has")
		}
	}
	if strings.Contains(id, "..") {
		return errors.New("the journal entry id is not a plain identifier")
	}
	return nil
}
