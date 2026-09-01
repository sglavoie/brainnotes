package vaultgit

import (
	"time"
)

// LoadPushState reports remote retry state. An unreadable file is reported as
// absent, the opposite of the block marker: forgetting a backoff schedule costs
// one early retry, while forgetting a block would cost a note.
func (s *safetyState) LoadPushState() (PushState, bool) {
	var state PushState
	found, err := s.readJSON(pushStateFileName, &state)
	if !found || err != nil || state.Version != stateVersion {
		return PushState{}, false
	}
	return state, true
}

// RecordPushFailure advances the retry schedule and persists it.
func (s *safetyState) RecordPushFailure(now time.Time, detail string) (PushState, error) {
	now = now.UTC()
	state, ok := s.LoadPushState()
	if !ok {
		state = PushState{FirstFailureAt: now}
	}
	state.Version = stateVersion
	state.Attempts++
	state.LastAttemptAt = now
	state.LastError = detail
	state.NextAttemptAfter = now.Add(pushBackoff(state.Attempts))
	return state, s.writeJSON(pushStateFileName, state)
}

// ClearPushState forgets the failure streak after a successful push.
func (s *safetyState) ClearPushState() error { return s.remove(pushStateFileName) }

// pushBackoff doubles from 30 seconds and caps at five minutes, which is the
// plan's "at least every five minutes" floor expressed as a ceiling on the
// wait.
func pushBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	wait := pushBackoffBase
	for i := 1; i < attempts; i++ {
		wait *= 2
		if wait >= pushBackoffMax {
			return pushBackoffMax
		}
	}
	return wait
}

// DueAt reports whether a retry is worth making at now.
func (p PushState) DueAt(now time.Time) bool {
	return !now.UTC().Before(p.NextAttemptAfter)
}
