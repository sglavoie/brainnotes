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
	return s.recordPushFailure(now, detail, false)
}

// StartPushStreak records a failure as the first of a new streak whatever the
// file says, for a caller that knows the recorded streak already ended in a
// success it could not write down.
func (s *safetyState) StartPushStreak(now time.Time, detail string) (PushState, error) {
	return s.recordPushFailure(now, detail, true)
}

func (s *safetyState) recordPushFailure(now time.Time, detail string, fresh bool) (PushState, error) {
	now = now.UTC()
	state, ok := s.LoadPushState()
	if !ok || fresh {
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
//
// A recorded retry further ahead than pushBackoffMax is due as well: no failure
// schedules one that far out, so it means the wall clock stepped backwards after
// it was written, and honouring it would stop publishing for as long as the
// clock had jumped.
func (p PushState) DueAt(now time.Time) bool {
	now = now.UTC()
	return !now.Before(p.NextAttemptAfter) || p.NextAttemptAfter.Sub(now) > pushBackoffMax
}
