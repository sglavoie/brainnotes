package vaultgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Preflight reports whether a mutation may proceed.
//
// Mutations refuse unresolved safety failures, concurrent safety operations,
// interrupted snapshots and unsupported repository states. Missing tracked
// regular files are ordinary external changes and do not prevent a mutation.
func (m *Manager) Preflight(ctx context.Context) error {
	release, err := m.acquirePreflight(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.preflightLocked(ctx, "preflight")
}

// preflightLocked runs the checks that require the safety lock already held.
func (m *Manager) preflightLocked(ctx context.Context, op string) error {
	if err := m.checkBlocked(ctx, op); err != nil {
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
func (m *Manager) checkBlocked(ctx context.Context, op string) error {
	record, blocked, readErr := m.state.LoadBlock()
	if !blocked {
		return nil
	}
	if readErr == nil && record.Kind == KindUnexplainedDeletion {
		return m.recoverDeletionBlock(ctx, op)
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
//
// The safety lock — and only the safety lock — is taken behind the vault's
// checkpoint barrier, which sits above both. Everything this lock guards writes
// the content history, and a copy taken between a note landing and its commit
// holds a change the history does not explain. Most callers have joined already,
// through noteops.BeginOperation or vaultstate.Save, and a nested join is
// counted rather than taken; the one that has not is the autosave watcher, which
// commits on its own schedule and is exactly the writer a checkpoint is most
// likely to overlap.
//
// The push lock is deliberately excluded. A push is a network round trip that
// changes nothing on this disk, and holding the barrier across it would let an
// unreachable remote exhaust every checkpoint's drain budget.
func (m *Manager) acquire(name, op string, contexts ...context.Context) (func(), error) {
	ctx := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	return m.acquireWithin(name, op, ctx, 0)
}

func (m *Manager) acquireWithin(name, op string, ctx context.Context, budget time.Duration) (func(), error) {
	barrier := func() {}
	if name == safetyLockName {
		_, joined, err := checkpoint.JoinContext(ctx, m.root)
		if err != nil {
			return nil, safetyErrorf(KindBusy, op,
				"a checkpoint is copying this vault, so the %s was not taken; try again shortly", name)
		}
		barrier = joined
	}
	if err := m.state.ensureDir(); err != nil {
		barrier()
		return nil, err
	}
	acquire := func() (func(), error) { return m.tryGitLock(name, op) }
	var release func()
	var err error
	if budget > 0 {
		release, err = waitForPreflight(ctx, budget, acquire)
	} else {
		release, err = acquire()
	}
	if err != nil {
		barrier()
		return nil, err
	}
	return func() { release(); barrier() }, nil
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
	head, err = m.git.resolveBranch(ctx, ref)
	if err != nil {
		return "", "", safetyErrorf(KindRepositoryState, op, "the branch %s could not be resolved: %s", ref, agent.SanitizedDiagnostic(err))
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

// recoverDeletionBlock migrates only the obsolete deletion policy. Other
// failures remain blocking, including a recovery intent that cannot be proved.
func (m *Manager) recoverDeletionBlock(ctx context.Context, op string) error {
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
	if err := m.seedIndex(ctx, head, op); err != nil {
		return err
	}
	if _, err := m.scanPending(ctx, head, op); err != nil {
		return err
	}
	return m.state.ClearBlock()
}

func (m *Manager) tryGitLock(name, op string) (func(), error) {
	release, acquired, err := vault.TryLockPath(m.state.path(name))
	if err != nil {
		return nil, safetyErrorf(KindRepositoryState, op, "the %s could not be taken: %s", name, agent.SanitizedDiagnostic(err))
	}
	if !acquired {
		return nil, safetyErrorf(KindBusy, op, "another BrainNotes process is already performing a Git safety operation on this vault")
	}
	return release, nil
}
