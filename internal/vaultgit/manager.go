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
	"sync"
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
	// Autosave is the vault's resolved external autosave floor. The zero value
	// means the shipped default rather than "no floor", so a caller that has not
	// yet learned the vault's policy gets the protective answer rather than the
	// permissive one.
	//
	// It is the policy the Manager starts under, not the one it is stuck with:
	// [Manager.SetAutosavePolicy] adopts a later one, which is how a running guard
	// applies a `brainnotes.toml` edit without a restart. What this field settles
	// is that the floor is data the engine is told, never a constant it owns.
	Autosave config.AutosavePolicy
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
	// autosave is the resolved external floor. It is normalized on every write so
	// that every read of it is already a usable policy, and it is guarded because
	// it is the one field a live vault reassigns: the watcher adopts a new policy
	// between two operations that both read this.
	autosaveMu sync.RWMutex
	autosave   config.AutosavePolicy

	// indexPath is the private index. It is re-seeded from HEAD at the start of
	// every operation and is never the user's index.
	indexPath string

	// execBitOnce guards the one-time read of the repository's core.fileMode,
	// which decides whether a staged mode may be compared exactly.
	execBitOnce    sync.Once
	execBitTrusted bool
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
// Open does not read the block marker: a blocked vault still opens, so a caller
// can report why it is blocked. Preflight is what refuses.
//
// It does read the recovery intent, and takes the safety lock to resolve one if
// it finds it — the one case where opening mutates anything. The read is a
// single failed open on a healthy vault, and a vault with an interrupted
// snapshot is exactly the vault where the next thing to happen should be a
// recovery pass rather than another operation on top of it. Its verdict is
// discarded here, for the same reason the block marker is not read.
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
		now:      opts.Now,
		autosave: resolveAutosavePolicy(opts.Autosave),
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
	m.recoverIntentOnOpen(ctx)
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

// ClearBlock removes the block marker and any surviving recovery intent.
//
// It is deliberately the only way out of a block and it is deliberately not
// automatic: a block means the engine saw something it would not classify, and
// the condition that caused it is still on disk. Clearing without fixing the
// working tree only means the next preflight re-derives the same verdict, which
// is the point — the marker reports the refusal, it never causes it.
//
// The intent record goes with it because it is the same act. A record that
// recovery could retire on its own is already gone by the time anyone runs this;
// one that is still here is a refusal only a person can lift, and leaving it
// behind would make clearing the marker appear to do nothing.
func (m *Manager) ClearBlock() error {
	return errors.Join(m.state.ClearBlock(), m.state.ClearIntent())
}

// Interrupted reports a surviving recovery intent, if any. Like Blocked it takes
// no lock and never fails, so a status command can explain a refusal that no
// block marker accounts for.
func (m *Manager) Interrupted() (IntentRecord, bool) {
	record, present, _ := m.state.LoadIntent()
	return record, present
}

// PushStatus reports the remote retry state.
func (m *Manager) PushStatus() (PushState, bool) { return m.state.LoadPushState() }

// AutosavePolicy reports the external autosave floor this engine is enforcing
// right now. A caller that has to act on it more than once in a row should read
// it once and use that value, because a live reload may replace it in between.
func (m *Manager) AutosavePolicy() config.AutosavePolicy { return m.policy() }

// policy reads the floor under the lock. Every internal use goes through it, so
// a reload landing mid-operation cannot leave one step of that operation reading
// the old policy and the next reading the new one from a torn field.
func (m *Manager) policy() config.AutosavePolicy {
	m.autosaveMu.RLock()
	defer m.autosaveMu.RUnlock()
	return m.autosave
}

// SetAutosavePolicy adopts a new resolved floor and republishes the schedule it
// implies, so `watch.json` states the policy the engine is actually enforcing
// rather than the one it started under.
//
// It is the whole of live reload inside this package: the Manager is told what
// the policy now is and never learns where that came from, which is what keeps
// TOML parsing out of here. Adopting is unconditional and immediate — the
// caller is the one that decides a change is safe to publish, because the one
// transition that is *not* safe to publish immediately (enabled to off, which
// owes the vault a final snapshot first) is a scheduling decision and this
// package's scheduler is the watcher.
//
// The returned error reports only that the republished record could not be
// written. The policy is adopted either way: an unwritable state file costs a
// stale published field, and refusing to enforce the vault's own configuration
// because a scheduling hint could not be persisted would be the worse answer.
func (m *Manager) SetAutosavePolicy(policy config.AutosavePolicy) error {
	resolved := resolveAutosavePolicy(policy)
	m.autosaveMu.Lock()
	m.autosave = resolved
	m.autosaveMu.Unlock()
	_, err := m.state.RefreshWatchState(resolved)
	return err
}

// ResetExternalBurst forgets the open debounce burst without claiming a snapshot
// was taken, leaving the floor's anchor exactly where it was.
//
// It exists for the two policy transitions that must not inherit a burst: the
// vault stops autosaving, so the burst it was accumulating has nothing left to
// schedule, and the vault starts again, where a burst opened under the old
// policy would backdate the new one's readiness deadline to whenever the working
// tree first went dirty — possibly hours ago, which would snapshot on the very
// next poll instead of after a quiet period. Keeping LastCommitAt is deliberate:
// re-enabling does not entitle a vault to an immediate commit, so the previous
// external snapshot stays the floor's anchor.
func (m *Manager) ResetExternalBurst() error {
	_, err := m.state.NoteSettled(m.policy())
	return err
}

// WatchStatus reports the persisted external-change schedule without changing
// it. Like Blocked it takes no lock and never fails, so a status command can
// always say when the next external snapshot is expected.
func (m *Manager) WatchStatus() WatchState { return m.state.LoadWatchState() }

// NoteExternalChange records that the working tree changed, opening a debounce
// burst if none is open, and republishes the due time the change implies.
func (m *Manager) NoteExternalChange() (WatchState, error) {
	return m.state.NoteChange(m.policy(), m.now())
}

// NoteExternalSettled closes an open debounce burst whose changes are no longer
// there to commit, so a burst that resolved itself does not stay due forever.
// It reports whether there was one to close.
func (m *Manager) NoteExternalSettled() (closed bool, err error) {
	return m.state.NoteSettled(m.policy())
}

// ExternalCommitDue reports whether an open debounce burst has reached both its
// readiness deadline and the floor since the last external autosave.
//
// It is read-only in intent but not in effect, in the same way and for the same
// reason as ScanPending: it republishes the persisted schedule when the record
// on disk does not already state it. That is what brings a record written before
// this build, or under a different floor, up to date without a separate
// migration pass — and what makes the due time a health check reads the same one
// the watcher is about to act on. The write is conditional, so polling an
// unchanged vault costs a read.
//
// A failed republish is deliberately not surfaced, and the state it returns is
// used anyway: the schedule is computed in memory before it is written, so an
// unwritable record costs a stale published field and never a wrong answer here.
func (m *Manager) ExternalCommitDue() bool {
	policy := m.policy()
	state, _ := m.state.RefreshWatchState(policy)
	return state.DueAt(policy, m.now())
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
	//
	// Only an external-change snapshot can produce one. An exact snapshot fails
	// closed instead, so a caller that named its paths is never handed a success
	// that quietly omits one of them.
	Skipped []SkippedPath
	// DeferredDeletions lists proven deletions that were held back because
	// something else in the same pass was skipped.
	DeferredDeletions []string
	// landedAt is the first clock reading after the ref update was observed to
	// succeed and its containing directory was synced. It stays internal because
	// callers need only the commit verdict; the external floor protocol uses it
	// to record the completion boundary.
	landedAt time.Time
}

// Unrecorded reports the paths this snapshot was asked to record and did not:
// everything it skipped, plus every proven deletion it held back behind a skip.
// It is empty for a complete snapshot, including one that found nothing to do.
//
// An exact snapshot can never return a non-empty answer, because the engine
// refuses rather than reporting an unstaged required path. Exact callers check
// it anyway, and the redundancy is the point: the guarantee they depend on is
// that content the history does not hold is never reported as recorded, and a
// caller that confirms its own result does not have to take that on trust.
func (r CommitResult) Unrecorded() []string {
	if len(r.Skipped) == 0 && len(r.DeferredDeletions) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Skipped)+len(r.DeferredDeletions))
	for _, skip := range r.Skipped {
		out = append(out, skip.Path)
	}
	out = append(out, r.DeferredDeletions...)
	sort.Strings(out)
	return out
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
	// Recovery runs before anything reads the repository. An interrupted snapshot
	// is a fifth refusal condition and it is the only one the working tree does
	// not show: the paths involved may look perfectly ordinary while the history
	// is missing them.
	if _, err := m.recoverIntentLocked(ctx, op); err != nil {
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
// It is read-only in intent but not in effect, and deliberately so: it runs the
// same interrupted-snapshot recovery and the same deletion verification the
// mutation path runs, so an unexplained deletion latches the block marker the
// moment a poll observes it rather than at the next mutation. That is the whole
// reason the guard polls at all.
func (m *Manager) ScanPending(ctx context.Context) (PendingSummary, error) {
	const op = "scan-pending"
	release, err := m.acquire(safetyLockName, op)
	if err != nil {
		return PendingSummary{}, err
	}
	defer release()

	if err := m.checkBlocked(op); err != nil {
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

// commitPlan is the exact set of index changes one snapshot will make.
type commitPlan struct {
	// at is the instant the commit object is stamped with, in both its author and
	// its committer date. Only an external autosave sets it deliberately, tying
	// the commit subject and object to its write-ahead marker. Explicit mutation
	// plans leave it zero and stageAndCommit chooses the commit-creation reading.
	//
	// That matters for exactly one plan: an external autosave uses the same instant
	// for its marker, generated subject, and commit metadata. It is not the final
	// scheduling boundary; the external floor is measured from the later observed
	// ref landing.
	at     time.Time
	add    []string
	remove []string
	// proofEntries are the journal entry IDs whose records authorize the
	// removals. If a removal is held back, its proof is held back too.
	proofEntries map[string]bool
	message      string
	// externalAdditions marks a plan whose additions were discovered by scanning
	// the working tree rather than named by a caller. Only such a plan may skip
	// an addition it cannot read.
	//
	// The distinction decides whether an unstaged path is tolerable. Nobody asked
	// for the external scan's paths in particular — it offers to record whatever
	// the vault happens to hold — so leaving one out costs nothing the history had
	// a moment ago. Every other entry point is a caller saying "these exact paths
	// are now the truth"; silently recording some of them would answer a question
	// it was not asked, and the caller would report a success the history does
	// not back. See stageAndCommit.
	externalAdditions bool
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
//
// The whole body is bracketed by the recovery intent: it is written and fsynced
// before the first index write and retired only once the branch has moved and
// the directory naming its ref is durable. Every failure in between either
// latches a block marker — which takes the record's place as the durable
// statement — or leaves the record standing, which is what makes an interrupted
// snapshot distinguishable from one that never started. See intent.go.
func (m *Manager) stageAndCommit(ctx context.Context, op, head, ref string, plan *commitPlan) (CommitResult, error) {
	intent, err := m.beginIntent(op, ref, head, plan)
	if err != nil {
		return CommitResult{}, err
	}

	staged, skipped, stageErr := m.stageAdditions(ctx, plan.add)
	if stageErr != nil {
		// Staging failed for a path the engine can read perfectly well, which
		// makes it a failure of the repository rather than of that path. It would
		// hit everything else in the pass too, so continuing would build a tree
		// missing content the working tree holds — the exact shape of loss this
		// package exists to refuse.
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the vault's content could not be staged, so nothing was committed",
			Err:    stageErr,
		})
	}
	if len(skipped) > 0 && !plan.externalAdditions {
		// The caller named these paths, so leaving one out is not a smaller
		// snapshot, it is a wrong one. Refusing here is what lets the caller say
		// "the change is on disk and the history does not cover it" instead of
		// reporting a success.
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "a path this snapshot was required to record could not be staged, so nothing was committed",
			Paths:  skippedPaths(skipped),
			Err:    skippedError(skipped),
		})
	}
	if !plan.externalAdditions {
		// An exact snapshot's promise is that the paths it names are in the tree
		// it builds *with the content they hold right now*, so it reads the index
		// back rather than trusting an exit status. Asking only whether the path
		// has an entry answers yes for every already-tracked path before anything
		// is staged, because the index is seeded from HEAD — so the comparison is
		// against the required object and mode, which is the only check that
		// survives a `git` reporting success without doing the work.
		wrong, err := m.misstagedAdditions(ctx, staged)
		if err != nil {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "the snapshot could not confirm what it had staged, so nothing was committed",
				Err:    errors.New(agent.SanitizedDiagnostic(err)),
			})
		}
		if len(wrong) > 0 {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "the private index does not hold the content this snapshot was required to record, so nothing was committed",
				Paths:  wrong,
			})
		}
	}

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
	if !plan.externalAdditions && len(removals) > 0 {
		// The same readback for the removal half of an exact plan, and for the same
		// reason: the index is seeded from HEAD, so a `--force-remove` that reported
		// success without dropping the entry leaves the tip's copy in the tree. A
		// plan that removes without adding then writes a tree identical to HEAD and
		// returns a nil success naming deletions the history never recorded.
		remaining, err := m.unremovedDeletions(ctx, removals)
		if err != nil {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "the snapshot could not confirm the removals it had staged, so nothing was committed",
				Err:    errors.New(agent.SanitizedDiagnostic(err)),
			})
		}
		if len(remaining) > 0 {
			return CommitResult{}, m.latch(&SafetyError{
				Kind:   KindCommitFailed,
				Op:     op,
				Reason: "a path this snapshot was required to remove is still in the index, so nothing was committed",
				Paths:  remaining,
			})
		}
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
		// Nothing to record and so nothing to recover: this pass ends before the
		// intent does any work, and clearing it keeps a held-back snapshot from
		// leaving a record that outlives the reason for it.
		_ = m.clearIntent()
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
			_ = m.clearIntent()
			return result, nil
		}
	}

	commitAt := plan.at
	if commitAt.IsZero() {
		// Explicit mutations retain the pre-floor behavior: their visible author
		// and committer dates are chosen immediately before commit creation, after
		// intent creation, staging, and tree writing have completed.
		commitAt = m.now()
	}
	commit, err := m.git.commitTree(ctx, tree, head, plan.message, m.who, commitAt)
	if err != nil {
		return CommitResult{}, m.latch(&SafetyError{
			Kind:   KindCommitFailed,
			Op:     op,
			Reason: "the snapshot commit could not be written, so nothing was recorded",
			OldOID: head,
			Err:    errors.New(agent.SanitizedDiagnostic(err)),
		})
	}

	// The commit object exists but nothing points at it yet. Recording its name
	// now is what lets a restart after the next line ask the only question that
	// settles the outcome: is this commit in the branch's history?
	intent, err = m.promoteIntent(op, intent, commit)
	if err != nil {
		return CommitResult{}, err
	}

	if err := m.git.updateRefCAS(ctx, ref, commit, head); err != nil {
		// Distinguish "someone else got there first" from "this repository is
		// broken" by asking where the branch is now. Only the second latches.
		if current, resolveErr := m.git.revParse(ctx, ref); resolveErr != nil || current != head {
			// A branch that provably moved to something other than head means this
			// update never applied, so the attempt is over and withCAS rebuilds from
			// the new tip with a fresh record. When the branch could not be read at
			// all, nothing is proven and the record stays.
			if resolveErr == nil {
				_ = m.clearIntent()
			}
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

	// The branch has moved. Git wrote the ref file, but the directory entry that
	// names it is what makes the move survive power loss, and "the ref landing is
	// durable" is exactly the condition under which the record may be retired.
	// Best effort on purpose: the commit has landed either way, and turning a
	// successful snapshot into a failure over a housekeeping fsync would be the
	// destructive answer to the harmless problem. A packed ref has no such
	// directory, and the failed sync is ignored for the same reason.
	_ = vault.SyncDir(filepath.Dir(filepath.Join(m.gitDir, filepath.FromSlash(ref))))
	result.landedAt = m.now().UTC()
	if plan.externalAdditions {
		// The ref is now observably durable. Persist that upper bound in the intent
		// before watch.json is asked to replace the write-ahead marker. If this
		// rewrite fails, the earlier committed-phase intent remains and recovery
		// conservatively anchors at the later instant when it proves landing.
		intent.LandedAt = &result.landedAt
		intent.ObservedAt = result.landedAt
		_ = m.state.SaveIntent(intent)
	} else {
		// Explicit commits have no external floor to complete, so their durable ref
		// update remains the point at which the recovery intent can be retired.
		_ = m.clearIntent()
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

// stageAdditions records working-tree content, skipping only what the vault
// itself will not hand over.
//
// An unreadable addition is reported rather than fatal, and the distinction is
// deliberate: recording a file is purely additive, so failing to record one
// leaves the history exactly as correct as it was a moment ago. A vault holds
// notes a user has deliberately chmod'd to 0000, and refusing to snapshot the
// entire vault because of one of them would trade a real safety net for a
// theoretical completeness guarantee.
//
// That tolerance is bounded by the third return value, which is the repair for
// the failure mode it used to hide. A path git refused to stage is only a skip
// when the engine can independently confirm it cannot read the bytes either;
// anything else means git failed for a reason that has nothing to do with this
// path — an index it cannot write, an object store that will not take a write —
// and that reason applies to every other path in the same pass. Turning it into
// a skip made a repository-wide failure look like an empty vault: nothing
// staged, no error, no commit, and a caller told the snapshot succeeded.
//
// The batch is tried first because it is one process for the whole vault. Only
// when it fails does the engine fall back to one invocation per path, which is
// slow but is the only way to learn which path was the problem. Re-staging a
// path the failed batch already staged is idempotent.
func (m *Manager) stageAdditions(ctx context.Context, paths []string) (staged []string, skipped []SkippedPath, err error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if batchErr := m.git.stageAdd(ctx, m.indexPath, paths); batchErr == nil {
		return paths, nil, nil
	}
	for _, path := range paths {
		stageErr := m.git.stageAdd(ctx, m.indexPath, []string{path})
		if stageErr == nil {
			staged = append(staged, path)
			continue
		}
		detail := agent.SanitizedDiagnostic(stageErr)
		if !m.contentUnreadable(path) {
			return staged, skipped, fmt.Errorf("%s could not be staged although the vault hands its content over on request: %s", path, detail)
		}
		skipped = append(skipped, SkippedPath{Path: path, Reason: detail})
	}
	return staged, skipped, nil
}

// contentUnreadable reports whether the engine, asking on its own account, also
// cannot read this path's bytes.
//
// It is the question that separates the one staging failure this engine may
// tolerate from every other one, and it is asked by opening the file rather than
// by reading git's diagnostic. A message is a string a future git is free to
// reword; whether the vault will hand over the content is the fact the tolerance
// was justified by in the first place.
//
// A path that vanished between the scan and the staging answers yes, which is
// the right answer for both callers: an external scan offers to record whatever
// is there and there is nothing there, and an exact snapshot required a path the
// vault no longer holds and must refuse.
func (m *Manager) contentUnreadable(rel string) bool {
	f, err := os.Open(filepath.Join(m.root, filepath.FromSlash(rel)))
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	// Opening a directory succeeds on Unix and fails only on the read, so the
	// content has to actually be asked for. An empty file answers io.EOF, which
	// is a file that reads fine and holds nothing.
	var probe [1]byte
	if _, err := f.Read(probe[:]); err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	return false
}

// misstagedAdditions reports which of paths the private index does not hold
// with exactly the object and mode the working tree currently has.
//
// Path membership is not the question, and asking it was a hole. The index is
// seeded from HEAD, so every already-tracked path arrives with an entry of its
// own before anything is staged: an `update-index --add` that reported success
// without doing the work left HEAD's entry sitting under the requested path, the
// readback found it, and the snapshot went on to build a tree identical to the
// tip and return a nil success with the change absent from the history. Only
// comparing the entry against the content the snapshot is required to record
// separates "the index holds this file" from "the index holds this file's old
// version".
//
// A path whose required entry cannot be established at all — it vanished, it is
// neither a regular file nor a symlink, git would not hash it — is an error
// rather than a mismatch, because the caller must fail closed either way and the
// two failures deserve different diagnostics.
func (m *Manager) misstagedAdditions(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	entries, err := m.git.lsFilesStaged(ctx, m.indexPath, paths)
	if err != nil {
		return nil, err
	}
	required, err := m.requiredIndexEntries(ctx, paths)
	if err != nil {
		return nil, err
	}
	trustExec := m.executableBitTrusted(ctx)
	var wrong []string
	for _, path := range paths {
		staged, present := entries[path]
		want := required[path]
		if !present || staged.oid != want.oid || !modeRecords(want.mode, staged.mode, trustExec) {
			wrong = append(wrong, path)
		}
	}
	return wrong, nil
}

// requiredIndexEntries reports the object and mode the private index must hold
// for each path if the tree this snapshot builds is to record the working tree.
func (m *Manager) requiredIndexEntries(ctx context.Context, paths []string) (map[string]treeEntry, error) {
	required := make(map[string]treeEntry, len(paths))
	hashable := make([]string, 0, len(paths))
	for _, path := range paths {
		abs := filepath.Join(m.root, filepath.FromSlash(path))
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("%s could not be inspected to confirm what the index must hold: %s", path, agent.SanitizedDiagnostic(err))
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				return nil, fmt.Errorf("%s could not be read to confirm what the index must hold: %s", path, agent.SanitizedDiagnostic(err))
			}
			oid, err := m.git.hashBlob(ctx, []byte(target))
			if err != nil {
				return nil, err
			}
			required[path] = treeEntry{mode: "120000", oid: oid, path: path}
		case info.Mode().IsRegular():
			required[path] = treeEntry{mode: regularIndexMode(info.Mode()), path: path}
			hashable = append(hashable, path)
		default:
			return nil, fmt.Errorf("%s is neither a regular file nor a symbolic link, so what the index must hold for it cannot be established", path)
		}
	}
	if len(hashable) == 0 {
		return required, nil
	}
	oids, err := m.git.hashWorktreeContent(ctx, hashable)
	if err != nil {
		return nil, err
	}
	for _, path := range hashable {
		oid, ok := oids[path]
		if !ok {
			return nil, fmt.Errorf("%s was not hashed, so what the index must hold for it cannot be established", path)
		}
		entry := required[path]
		entry.oid = oid
		required[path] = entry
	}
	return required, nil
}

// regularIndexMode is git's own rule for a regular file: any executable bit
// makes it 100755 and nothing else matters.
func regularIndexMode(mode os.FileMode) string {
	if mode.Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

// modeRecords reports whether a staged mode records the required one.
//
// The two are compared exactly wherever the executable bit means anything. On a
// repository with `core.fileMode` off — a vault living on a filesystem that does
// not carry the bit — git deliberately keeps whatever mode the entry already
// had, so demanding equality there would block the vault over a permission it
// was never going to record in the first place. The file *type* is still
// compared, because a regular file staged as a symlink is not a permissions
// question.
func modeRecords(required, staged string, trustExec bool) bool {
	if required == staged {
		return true
	}
	if trustExec {
		return false
	}
	return isRegularIndexMode(required) && isRegularIndexMode(staged)
}

func isRegularIndexMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}

// executableBitTrusted reports the repository's `core.fileMode`, read once.
//
// Every caller reaches it holding the safety lock, and the value is a property
// of the repository rather than of the pass, so one read serves the process.
func (m *Manager) executableBitTrusted(ctx context.Context) bool {
	m.execBitOnce.Do(func() {
		m.execBitTrusted = m.git.configBool(ctx, "core.fileMode", true)
	})
	return m.execBitTrusted
}

// unremovedDeletions reports which of paths the private index still holds after
// a removal was staged.
//
// It is the other half of the readback. A removal that never reached the index
// leaves the tip's entry in the tree, and for a plan that removes without adding
// that comes out as a tree identical to HEAD — no commit, no error, and a caller
// told the deletion was recorded. An unmerged entry surfaces as an error from
// lsFilesStaged, which is the same refusal by a different route.
func (m *Manager) unremovedDeletions(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	entries, err := m.git.lsFilesStaged(ctx, m.indexPath, paths)
	if err != nil {
		return nil, err
	}
	var present []string
	for _, path := range paths {
		if _, ok := entries[path]; ok {
			present = append(present, path)
		}
	}
	return present, nil
}

// skippedPaths reduces skips to their paths, for a block record.
func skippedPaths(skipped []SkippedPath) []string {
	out := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		out = append(out, skip.Path)
	}
	return out
}

// skippedError joins the skips into one diagnostic. Every part of it is a vault
// path or a sanitized single line from git, so it stays as safe to log as the
// block record it ends up in.
func skippedError(skipped []SkippedPath) error {
	errs := make([]error, 0, len(skipped))
	for _, skip := range skipped {
		errs = append(errs, fmt.Errorf("%s: %s", skip.Path, skip.Reason))
	}
	return errors.Join(errs...)
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
		// The marker did not land, so if a recovery intent is outstanding it is now
		// the only durable statement that a snapshot was attempted and never proved
		// to have finished. It stays, and every later Open and Preflight refuses on
		// it until recovery proves the snapshot landed or a person clears it.
		return err
	}
	// The marker says everything the intent said and more, and unlike the intent
	// it is a verdict rather than a question, so the record is retired here. A
	// failure to retire it is harmless: the next recovery pass reads it, cannot
	// prove it landed, and refuses — which is what the marker already does.
	if clearErr := m.clearIntent(); clearErr != nil {
		safety.Err = errors.Join(safety.Err, clearErr)
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
