package vaultgit

import (
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/config"
)

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
		_, err := m.state.RefreshWatchState(resolved, m.now())
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
	state, _ := m.state.RefreshWatchState(policy, m.now())
	return state.DueAt(policy, m.now())
}
