package vaultgit

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
)

// scanAndCommit reports whether it created a snapshot.
func (w *Watcher) scanAndCommit(ctx context.Context) bool {
	summary, err := w.mgr.ScanPending(ctx)
	if err != nil {
		w.reportFailure("the vault working tree could not be inspected", err)
		return false
	}
	// A scan that succeeds proves the vault is not blocked, so a block announced
	// earlier and since cleared can be announced again if it recurs.
	w.blockAnnounced = false

	if summary.Empty() {
		w.reportSettled()
		return false
	}

	// Read once: the rest of this poll must act under one policy, not under
	// whichever one each step happens to observe.
	off := w.mgr.AutosavePolicy().Disabled

	w.reportPending(summary, off)

	if w.pendingOff {
		return w.commitTransitionSnapshot(ctx)
	}
	if off {
		// Off still reports pending changes, including deletions, but does not
		// create external snapshots.
		return false
	}
	if !w.mgr.ExternalCommitDue() {
		return false
	}
	result, err := w.mgr.CommitPending(ctx)
	if err != nil {
		w.reportFailure("the external changes could not be recorded", err)
		return false
	}
	if !result.Committed {
		// The burst was due and the scan found changes, but the commit found none.
		// Something else — a `bn` mutation, another guard — took them first, which
		// is a race the compare-and-swap is built for and not a failure.
		return false
	}
	w.lastFingerprint = ""
	w.emitCommitted(result, "external changes were recorded in the content history")
	return true
}

// commitTransitionSnapshot takes the one immediate snapshot that turning
// external autosave off is defined to produce, and publishes `off` only if it
// lands.
//
// It deliberately does not consult ExternalCommitDue: the whole point of the
// final snapshot is that it bypasses both the quiet period and the floor, so the
// vault is left whole at the instant the user asked for the guard to stop
// committing rather than up to a floor's worth of edits behind. Everything else
// a snapshot checks still applies — the lock, removal validation, the block
// marker — because leaving the vault whole is exactly what those checks are for.
//
// Its failure paths are the reason the transition is deferred rather than
// published up front. Contention is a retry, and the intent survives it because
// it is held in the watcher rather than consumed by the attempt. A safety
// failure is fail-closed: a vault that cannot take its final snapshot does not
// get to stop taking snapshots, so it stays enabled, stays blocked, and keeps
// reporting until a person clears it.
func (w *Watcher) commitTransitionSnapshot(ctx context.Context) bool {
	result, err := w.mgr.CommitPending(ctx)
	if err != nil {
		w.reportFailure("the final snapshot before external autosave stops could not be recorded", err)
		return false
	}
	if !result.Committed {
		// Something else took the changes first, which leaves the vault exactly as
		// whole as this snapshot would have. Nothing is owed, so the switch happens.
		w.finishOffTransition()
		return false
	}
	w.lastFingerprint = ""
	w.emitCommitted(result, "external changes were recorded before external autosave stops")
	w.finishOffTransition()
	return true
}

// emitCommitted reports a snapshot. The path list is the union of what the
// commit recorded and what it removed, sorted so two runs over the same change
// set produce the same line.
func (w *Watcher) emitCommitted(result CommitResult, message string) {
	recorded := append(append([]string{}, result.Added...), result.Deleted...)
	sort.Strings(recorded)
	w.emit(WatchEvent{
		Kind:      WatchEventCommitted,
		Message:   message,
		Commit:    result.Commit,
		Branch:    result.Branch,
		Paths:     capPaths(recorded),
		PathCount: len(recorded),
	})
}

// maybePush attempts the remote when the interval has elapsed or when the
// caller has a reason to try now, and always defers to the persisted backoff.
func (w *Watcher) maybePush(ctx context.Context, urgent bool) {
	now := w.now()
	if !urgent && !w.lastPushAttempt.IsZero() && now.Sub(w.lastPushAttempt) < w.push {
		return
	}
	if !w.mgr.PushDue() {
		return
	}
	w.lastPushAttempt = now

	result, err := w.mgr.Push(ctx)
	if err != nil {
		detail := ""
		var safety *SafetyError
		if errors.As(err, &safety) {
			detail = safety.Reason
		} else {
			detail = agent.SanitizedDiagnostic(err)
		}
		w.emit(WatchEvent{
			Kind:     WatchEventPushFailed,
			Message:  "the content history could not be published; local history is unaffected",
			Branch:   result.Branch,
			Attempts: result.Retry.Attempts,
			Detail:   detail,
		})
		w.maybeAnnouncePushStreak(result.Retry)
		return
	}
	w.pushStreakNotified = time.Time{}
	if !result.Attempted || !result.Pushed {
		// No remote configured, or an unborn branch. Both are supported states and
		// neither is worth a line in the log on every interval.
		return
	}
	w.emit(WatchEvent{
		Kind:    WatchEventPushed,
		Message: "the content history was published",
		Branch:  result.Branch,
	})
	if result.RetryStateErr != nil {
		w.emit(WatchEvent{
			Kind:    WatchEventError,
			Message: "the push succeeded but the old push retry record could not be removed; this process treats it as resolved",
			Branch:  result.Branch,
			Detail:  agent.SanitizedDiagnostic(result.RetryStateErr),
		})
	}
}

// maybeAnnouncePushStreak emits one WatchEventPushFailing per outage.
func (w *Watcher) maybeAnnouncePushStreak(state PushState) {
	if state.Attempts < pushFailureNotifyAfter {
		return
	}
	if state.FirstFailureAt.Equal(w.pushStreakNotified) {
		return
	}
	w.pushStreakNotified = state.FirstFailureAt
	w.emit(WatchEvent{
		Kind:     WatchEventPushFailing,
		Message:  "the content history has not reached the remote for several attempts; local snapshots are still being taken",
		Attempts: state.Attempts,
		Detail:   state.LastError,
	})
}

// reportFailure turns a Manager error into the right event.
//
// The three cases are genuinely different and a log that conflated them would
// be useless: contention resolves itself on the next poll, a block needs a
// person, and everything else is worth reading. A block is announced once —
// repeating it every five seconds for however long it takes someone to notice
// would bury the reason it happened under thousands of copies of itself.
func (w *Watcher) reportFailure(message string, err error) {
	if IsBusy(err) {
		w.emit(WatchEvent{Kind: WatchEventBusy, Message: "another BrainNotes process holds the safety lock", Detail: message})
		return
	}
	var safety *SafetyError
	blocking := errors.As(err, &safety) && safety.Kind.blocking()
	if _, latched := w.mgr.Blocked(); latched || blocking {
		if w.blockAnnounced {
			return
		}
		w.blockAnnounced = true
		record, _ := w.mgr.Blocked()
		reason := record.Reason
		paths := record.Paths
		if errors.As(err, &safety) {
			if safety.Reason != "" {
				reason = safety.Reason
			}
			if len(safety.Paths) > 0 {
				paths = safety.Paths
			}
		}
		w.emit(WatchEvent{
			Kind:      WatchEventBlocked,
			Message:   "the vault is blocked and BrainNotes will not snapshot it until a person clears it",
			Paths:     capPaths(paths),
			PathCount: len(paths),
			Detail:    reason,
		})
		return
	}
	w.emit(WatchEvent{Kind: WatchEventError, Message: message, Detail: agent.SanitizedDiagnostic(err)})
}

func (w *Watcher) reportSettled() {
	w.lastFingerprint = ""
	// Only worth an event when there was a burst to close. An idle vault has no
	// open burst, and saying "settled" on every poll of one would drown the log.
	closed, err := w.mgr.NoteExternalSettled()
	switch {
	case err != nil:
		w.emit(WatchEvent{Kind: WatchEventError, Message: "the debounce clock could not be closed", Detail: agent.SanitizedDiagnostic(err)})
	case closed:
		w.emit(WatchEvent{Kind: WatchEventSettled, Message: "the working tree matches the content history again"})
	}
	// A clean working tree owes the outgoing policy nothing, so a pending switch
	// to off takes effect immediately rather than waiting for an edit that may
	// never come.
	w.finishOffTransition()
}

func (w *Watcher) reportPending(summary PendingSummary, off bool) {
	if current := w.fingerprint(summary); current != w.lastFingerprint {
		w.lastFingerprint = current
		// The clock is advanced only when there is a snapshot for it to schedule.
		// While off, opening a burst would leave a readiness deadline behind that
		// re-enabling would inherit — the working tree may have been dirty for
		// hours by then, and the first poll under the new policy would commit
		// instantly instead of waiting out a quiet period.
		if !off {
			if _, err := w.mgr.NoteExternalChange(); err != nil {
				// The clock is a scheduling hint; losing a write to it costs at most one
				// extra poll before the burst is reopened.
				w.emit(WatchEvent{Kind: WatchEventError, Message: "the debounce clock could not be advanced", Detail: agent.SanitizedDiagnostic(err)})
			}
		}
		paths := summaryPaths(summary)
		w.emit(WatchEvent{
			Kind:      WatchEventPending,
			Message:   "the working tree differs from the content history",
			Paths:     capPaths(paths),
			PathCount: len(paths),
		})
	}

}
