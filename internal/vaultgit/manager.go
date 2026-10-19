package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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

	// pushStale records that a push succeeded but push.json could not be
	// removed afterwards. The file on disk then describes a streak that is over,
	// and every reader in this process ignores it until a later write replaces
	// it: otherwise the next failure would continue the old streak and double a
	// backoff the remote never earned.
	pushMu    sync.Mutex
	pushStale bool
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
	root, git, err := openWorkingTree(vaultRoot, opts.GitExecutable)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		root:        root,
		journalRoot: filepath.Join(root, filepath.FromSlash(config.DefaultJournalDir)),
		git:         git,
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

// openWorkingTree resolves a vault root to an absolute path and a git executable
// bound to it, applying the two refusals that are about the *vault* rather than
// about any particular use of its history.
//
// It is shared by Open and OpenHistory so the set of directories this package
// considers managed is stated once. A read-only opening that disagreed with the
// writing one — accepting a `.git` file, say — would report history for a layout
// no snapshot could ever be written to.
func openWorkingTree(vaultRoot, executable string) (string, *gitExec, error) {
	if strings.TrimSpace(vaultRoot) == "" {
		return "", nil, errors.New("vaultgit: vault root is required")
	}
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", nil, fmt.Errorf("vaultgit: resolve vault root: %w", err)
	}

	info, err := os.Lstat(filepath.Join(root, ".git"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil, ErrUnmanaged
	case err != nil:
		return "", nil, safetyErrorf(KindRepositoryState, "open", "the vault's .git entry could not be inspected: %s", agent.SanitizedDiagnostic(err))
	case !info.IsDir():
		// A `.git` file points elsewhere: a linked worktree, a submodule, or a
		// separate git-dir layout. Each is a valid Git arrangement and none is one
		// this engine has been tested against, so it refuses rather than guessing
		// which directory it is allowed to write state into.
		return "", nil, safetyErrorf(KindRepositoryState, "open", "the vault's .git is a file rather than a directory, which this engine does not manage")
	}

	exe := strings.TrimSpace(executable)
	if exe == "" {
		exe, err = exec.LookPath("git")
		if err != nil {
			return "", nil, safetyErrorf(KindGitUnavailable, "open", "the git executable could not be found on PATH")
		}
	}
	return root, &gitExec{exe: exe, dir: root}, nil
}

// resolveLayout asks git where it thinks it is and checks the answer against
// where the caller said the vault is, then derives the private state locations
// from the answer.
func (m *Manager) resolveLayout(ctx context.Context) error {
	gitDir, err := resolveGitDir(ctx, m.git, m.root)
	if err != nil {
		return err
	}
	m.gitDir = gitDir
	m.state = newSafetyState(gitDir)
	m.indexPath = m.state.path(indexFileName)
	return nil
}

// resolveGitDir asks git where it thinks it is and checks the answer against
// where the caller said the vault is.
//
// The top-level check is the load-bearing one. If the vault is a subdirectory
// of some larger repository — a dotfiles repo, a home directory someone put
// under version control — then every path this engine computes would be
// relative to the wrong root, and it would happily commit the vault into a
// history that is not the vault's. Refusing is the only safe answer, and it is
// the same answer for a reader: paths read out of that history would be relative
// to the wrong root too, so an exporter would send a peer the enclosing
// repository's files under vault-relative names.
func resolveGitDir(ctx context.Context, git *gitExec, root string) (string, error) {
	out, err := git.run(ctx, gitCall{args: []string{"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir"}})
	if err != nil {
		return "", safetyErrorf(KindRepositoryState, "open", "the vault's Git repository is not usable: %s", agent.SanitizedDiagnostic(err))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[1]) == "" {
		return "", safetyErrorf(KindRepositoryState, "open", "the repository layout could not be resolved")
	}
	// Compare through EvalSymlinks: on macOS a vault under /tmp is reached via a
	// /private/var symlink, so the literal strings differ for the same directory.
	if !sameDir(strings.TrimSpace(lines[0]), root) {
		return "", safetyErrorf(KindRepositoryState, "open", "the vault is not the top level of its Git working tree, so BrainNotes will not manage it")
	}
	return strings.TrimSpace(lines[1]), nil
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
	return m.checkpointMutation("clear block", func() error {
		return errors.Join(m.state.ClearBlock(), m.state.ClearIntent())
	})
}

// Interrupted reports a surviving recovery intent, if any. Like Blocked it takes
// no lock and never fails, so a status command can explain a refusal that no
// block marker accounts for.
func (m *Manager) Interrupted() (IntentRecord, bool) {
	record, present, _ := m.state.LoadIntent()
	return record, present
}

// PushStatus reports the remote retry state.
func (m *Manager) PushStatus() (PushState, bool) { return m.loadPushState() }

// loadPushState is LoadPushState with a resolved-but-unremoved streak treated
// as absent; see pushStale.
func (m *Manager) loadPushState() (PushState, bool) {
	m.pushMu.Lock()
	stale := m.pushStale
	m.pushMu.Unlock()
	if stale {
		return PushState{}, false
	}
	return m.state.LoadPushState()
}

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
	return m.checkpointMutation("publish autosave policy", func() error {
		_, err := m.state.RefreshWatchState(resolved)
		return err
	})
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
	return m.checkpointMutation("reset external burst", func() error {
		_, err := m.state.NoteSettled(m.policy())
		return err
	})
}

// WatchStatus reports the persisted external-change schedule without changing
// it. Like Blocked it takes no lock and never fails, so a status command can
// always say when the next external snapshot is expected.
func (m *Manager) WatchStatus() WatchState { return m.state.LoadWatchState() }

// NoteExternalChange records that the working tree changed, opening a debounce
// burst if none is open, and republishes the due time the change implies.
func (m *Manager) NoteExternalChange() (WatchState, error) {
	var state WatchState
	err := m.checkpointMutation("record external change", func() error {
		var err error
		state, err = m.state.NoteChange(m.policy(), m.now())
		return err
	})
	return state, err
}

// NoteExternalSettled closes an open debounce burst whose changes are no longer
// there to commit, so a burst that resolved itself does not stay due forever.
// It reports whether there was one to close.
func (m *Manager) NoteExternalSettled() (closed bool, err error) {
	err = m.checkpointMutation("settle external change", func() error {
		closed, err = m.state.NoteSettled(m.policy())
		return err
	})
	return closed, err
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
	release, err := checkpoint.JoinWithin(m.root, time.Millisecond)
	if err != nil {
		return m.state.LoadWatchState().DueAt(policy, m.now())
	}
	defer release()
	state, _ := m.state.RefreshWatchState(policy)
	return state.DueAt(policy, m.now())
}

// checkpointMutation brackets operational Git state that is not already under
// the safety lock. These writes are small and local; network push work stays
// outside and joins only to publish its result.
func (m *Manager) checkpointMutation(op string, mutate func() error) error {
	release, err := checkpoint.Join(m.root)
	if err != nil {
		return barrierJoinError(op, "nothing was written", err)
	}
	defer release()
	return mutate()
}

// recordPushFailure advances the retry streak. A streak this process already
// saw resolved starts again from one attempt, and the stale marker is dropped
// only once a record describing the new streak is on disk.
func (m *Manager) recordPushFailure(now time.Time, detail string) (state PushState, err error) {
	m.pushMu.Lock()
	defer m.pushMu.Unlock()
	err = m.checkpointMutation("record push failure", func() error {
		state, err = m.state.RecordPushFailure(now, detail, m.pushStale)
		return err
	})
	if err == nil {
		m.pushStale = false
	}
	return state, err
}

// clearPushState forgets a resolved streak. When the record cannot be removed
// the streak is still over, so it is marked stale in memory rather than left to
// lengthen the next outage's backoff.
func (m *Manager) clearPushState() error {
	m.pushMu.Lock()
	defer m.pushMu.Unlock()
	err := m.checkpointMutation("clear push retry", m.state.ClearPushState)
	m.pushStale = err != nil
	return err
}

// PushDue reports whether a retry is worth attempting now. A vault with no
// recorded failure is always due.
func (m *Manager) PushDue() bool {
	state, ok := m.loadPushState()
	if !ok {
		return true
	}
	return state.DueAt(m.now())
}
