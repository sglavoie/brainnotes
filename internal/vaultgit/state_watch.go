package vaultgit

import (
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// LoadWatchState reports the debounce clock, defaulting to an empty one.
//
// It reports the record as it is on disk, including a pre-existing version-1
// record that predates the schedule fields. Deriving those is publishWatchState's
// job, so that every path which persists a change publishes the same schedule
// rather than each reader computing its own.
func (s *safetyState) LoadWatchState() WatchState {
	var state WatchState
	found, err := s.readJSON(watchStateFileName, &state)
	if !found || err != nil || state.Version != stateVersion {
		return WatchState{Version: stateVersion}
	}
	return state
}

// publishWatchState writes the record with its derived fields recomputed, and
// only when doing so would change what is on disk.
//
// The conditional is what makes it safe to call from the watcher's poll: an idle
// vault whose record already says what this call would say costs one read, and a
// vault whose policy or schedule moved gets one atomic republish. Every mutator
// below routes through here so there is exactly one place a due time is derived.
func (s *safetyState) publishWatchState(state WatchState, policy config.AutosavePolicy, force bool) (WatchState, error) {
	before := state
	state.Version = stateVersion
	state.applyPolicy(policy)
	if !force && before.publishedAs(state) {
		return state, nil
	}
	return state, s.writeJSON(watchStateFileName, state)
}

// RefreshWatchState republishes the derived fields of the persisted record under
// the vault's current policy and reports the result.
//
// This is the "read an older record and bring it up to date" path: a version-1
// record written before the schedule existed, or one written under a different
// floor, is recomputed from the burst timestamps it already carries and rewritten
// atomically. Nothing about the burst itself is invented — an absent
// LastCommitAt stays absent rather than being inferred from the Git history,
// because a history full of explicit `bn` commits would otherwise be read as
// external autosaves and would hold the very first external snapshot back behind
// a floor nothing ever anchored.
func (s *safetyState) RefreshWatchState(policy config.AutosavePolicy) (WatchState, error) {
	return s.publishWatchState(s.LoadWatchState(), policy, false)
}

// NoteChange records that the working tree changed at now, starting a burst if
// one is not already open.
func (s *safetyState) NoteChange(policy config.AutosavePolicy, now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	if state.FirstChangeAt.IsZero() {
		state.FirstChangeAt = now
	}
	state.LastChangeAt = now
	// Forced: a change always rewrites the record. The burst timestamps moved even
	// when the due time did not — a quiet period restarted under a floor that is
	// still the binding deadline computes the same due time, and a reader that saw
	// no write would think the burst had gone still.
	return s.publishWatchState(state, policy, true)
}

// NoteSettled closes an open burst that no longer has anything to commit,
// without claiming a commit happened.
//
// It exists for the case where a burst opens and then resolves itself: an
// editor writes a file and the user undoes it, a sync client touches a
// timestamp and puts it back. NoteCommit is wrong there because LastCommitAt
// would name a snapshot that was never taken, and leaving the burst open is
// worse — DueAt would stay true forever and every poll would rescan and commit
// nothing.
// It reports whether there was an open burst to close, so a caller polling an
// idle vault can stay silent instead of announcing the same non-event forever.
// It clears the due time with the burst, because there is no longer anything for
// a schedule to be about. LastCommitAt is left exactly where it was: a burst that
// resolved itself took no snapshot, so it must not move the floor a real one
// would be measured from.
//
// ExternalCommitStartedAt is left alone for the sharper version of the same
// reason. The state a crash between a landed commit and its anchor write leaves
// behind is exactly "an in-flight marker and a clean working tree", so this is
// the first path that runs afterwards; retiring the marker here would drop the
// only durable evidence of that snapshot and hand the next burst a stale floor.
func (s *safetyState) NoteSettled(policy config.AutosavePolicy) (closed bool, err error) {
	state := s.LoadWatchState()
	if state.FirstChangeAt.IsZero() && state.LastChangeAt.IsZero() {
		// Nothing to close, but the policy or the schedule fields may still be stale
		// — an unforced publish rewrites the record only if they are.
		_, err = s.publishWatchState(state, policy, false)
		return false, err
	}
	state.FirstChangeAt = time.Time{}
	state.LastChangeAt = time.Time{}
	_, err = s.publishWatchState(state, policy, true)
	return true, err
}

// BeginExternalCommit records that an external autosave is about to be attempted
// at now, and establishes a conservative floor marker before it can be taken.
//
// This is the write-ahead half of the floor's durability contract, and its
// ordering is the first half of it: it runs before anything the attempt does can
// produce a commit. Once the ref lands, the recovery intent carries the later
// observed landing bound until NoteCommit publishes it here. Its failure is
// decisive for the same reason
// [safetyState.SaveIntent]'s is — a snapshot whose floor could not be anchored is
// one that could be taken twice inside the interval the vault configured — and it
// costs nothing but a deferral, because the burst it refuses stays open and the
// next poll retries it.
//
// It forces the write. The marker is not a derived field, so an unforced publish
// would skip it whenever the due time it implies happens to match the one already
// on disk, which is precisely the case where the floor is binding.
func (s *safetyState) BeginExternalCommit(policy config.AutosavePolicy, now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	state.ExternalCommitStartedAt = &now
	return s.publishWatchState(state, policy, true)
}

// AbandonExternalCommit retires the marker BeginExternalCommit wrote at
// startedAt, for the pass that provably recorded nothing.
//
// A snapshot that reached the repository and committed no tree owes the floor
// nothing, so holding the interval against it would spend a vault's snapshot
// budget on a non-event. The instant is matched rather than assumed: only the
// attempt that wrote a marker may retire it, so a pass cannot lift a floor some
// other attempt is still holding.
func (s *safetyState) AbandonExternalCommit(policy config.AutosavePolicy, startedAt time.Time) (WatchState, error) {
	state := s.LoadWatchState()
	if state.ExternalCommitStartedAt == nil || !state.ExternalCommitStartedAt.Equal(startedAt.UTC()) {
		return state, nil
	}
	state.ExternalCommitStartedAt = nil
	return s.publishWatchState(state, policy, true)
}

// NoteCommit closes the current burst and anchors the external floor at now.
//
// It is called from exactly one place — [Manager.CommitPending], after a
// successful external snapshot — and that is the whole of the floor's contract.
// Every other entry point commits without touching this record, so an explicit
// mutation neither waits for the floor nor resets it.
//
// It also retires the in-flight marker, which is what makes this the *durable*
// statement of the same fact rather than a second one: from here on the anchor is
// LastCommitAt, and it names the first observation after the ref landed rather
// than when staging started. A failure here is survivable because the committed
// recovery intent remains on disk; the next recovery pass publishes its landing
// bound before allowing another external snapshot.
func (s *safetyState) NoteCommit(policy config.AutosavePolicy, now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	state.FirstChangeAt = time.Time{}
	state.LastChangeAt = time.Time{}
	state.LastCommitAt = now
	state.ExternalCommitStartedAt = nil
	return s.publishWatchState(state, policy, true)
}

// applyPolicy recomputes the two derived fields in place. It is the only writer
// of either, so the published token and the published due time cannot disagree
// with each other or with the schedule DueAt enforces.
func (w *WatchState) applyPolicy(policy config.AutosavePolicy) {
	policy = resolveAutosavePolicy(policy)
	w.AutosaveMinInterval = policy.Canonical
	due, scheduled := w.DueTime(policy)
	if !scheduled {
		w.NextCommitDueAt = nil
		return
	}
	w.NextCommitDueAt = &due
}

// publishedAs reports whether w already carries exactly the derived fields of
// next, so an unforced publish can skip the write.
func (w WatchState) publishedAs(next WatchState) bool {
	if w.Version != next.Version || w.AutosaveMinInterval != next.AutosaveMinInterval {
		return false
	}
	switch {
	case w.NextCommitDueAt == nil && next.NextCommitDueAt == nil:
		return true
	case w.NextCommitDueAt == nil || next.NextCommitDueAt == nil:
		return false
	default:
		return w.NextCommitDueAt.Equal(*next.NextCommitDueAt)
	}
}

// DueTime computes the schedule the plan specifies:
//
//	ready_at = min(last_change + DebounceQuiet, first_change + DebounceMaxLatency)
//	floor_at = last_external_autosave + policy.Interval
//	due_at   = max(ready_at, floor_at)
//
// Readiness still decides eligibility on its own — the floor can only ever push a
// due time later, never earlier — so an editor that goes quiet and an editor that
// never does are handled exactly as before, and the floor is what stops a vault
// under continuous edits from acquiring one commit every two minutes forever.
//
// It reports false when there is no schedule to state: the policy is off, or no
// burst is open. With no previous external autosave the floor has nothing to be
// measured from, and readiness alone controls the first snapshot. It is exactly
// what the persisted NextCommitDueAt holds.
func (w WatchState) DueTime(policy config.AutosavePolicy) (time.Time, bool) {
	policy = resolveAutosavePolicy(policy)
	if policy.Disabled || w.FirstChangeAt.IsZero() {
		return time.Time{}, false
	}
	ready := w.FirstChangeAt.UTC().Add(DebounceMaxLatency)
	if !w.LastChangeAt.IsZero() {
		if quiet := w.LastChangeAt.UTC().Add(DebounceQuiet); quiet.Before(ready) {
			ready = quiet
		}
	}
	anchor := w.externalAnchor()
	if anchor.IsZero() {
		return ready, true
	}
	if floor := anchor.Add(policy.Interval); floor.After(ready) {
		return floor, true
	}
	return ready, true
}

// externalAnchor reports the instant the floor is measured from: the later of the
// last external autosave known to have finished and the last one known to have
// been attempted.
//
// The two disagree while an attempt is in flight and across the recovery window
// after a ref landed but its completion could not be written. During that latter
// window the retained intent prevents a second external commit; recovery then
// replaces the attempted-at fallback with the observed landing bound before it
// permits another one.
func (w WatchState) externalAnchor() time.Time {
	anchor := w.LastCommitAt.UTC()
	if w.ExternalCommitStartedAt == nil {
		return anchor
	}
	if started := w.ExternalCommitStartedAt.UTC(); started.After(anchor) {
		return started
	}
	return anchor
}

// DueAt reports whether an open burst may be committed at now: readiness has
// elapsed *and* the configured floor since the last external autosave has too. A
// state with no open burst, and every state under a disabled policy, is never
// due.
func (w WatchState) DueAt(policy config.AutosavePolicy, now time.Time) bool {
	due, scheduled := w.DueTime(policy)
	if !scheduled {
		return false
	}
	return !now.UTC().Before(due)
}

// resolveAutosavePolicy fills in what a caller left out, so that the zero
// AutosavePolicy means the vault default rather than "commit as often as you
// like".
//
// The direction matters: a policy that arrived unset must not resolve to no
// floor. Everything in this package that can hand one over — an Options literal a
// test wrote, a future reload that failed — is a place where "I did not say"
// should mean the shipped default, because the alternative is a vault that
// quietly loses the protection its owner never turned off.
func resolveAutosavePolicy(policy config.AutosavePolicy) config.AutosavePolicy {
	if policy.Disabled {
		return config.AutosavePolicy{Disabled: true, Canonical: config.AutosaveOff}
	}
	if policy.Interval <= 0 {
		fallback, err := config.ParseAutosaveMinInterval(config.DefaultAutosaveMinInterval)
		if err != nil {
			// Unreachable: the default is a constant this build parses at every
			// startup. Falling back to the bound rather than to zero keeps the
			// impossible case on the safe side of the floor.
			return config.AutosavePolicy{
				Interval:  config.MinAutosaveInterval,
				Canonical: config.CanonicalAutosaveDuration(config.MinAutosaveInterval),
			}
		}
		return fallback
	}
	if policy.Canonical == "" {
		policy.Canonical = config.CanonicalAutosaveDuration(policy.Interval)
	}
	return policy
}
