package vaultgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// The watcher is the scheduling half of this package. The Manager knows how to
// take a snapshot safely; the watcher knows when to ask for one.
//
// It deliberately owns no policy of its own. The debounce deadlines live in the
// persisted WatchState, the autosave floor lives in the Manager's resolved
// config.AutosavePolicy, the push backoff lives in the persisted PushState, and
// the refusal to commit an unexplained deletion lives in the Manager. What the
// watcher adds is a loop, a poll interval, and the decision to stop asking once
// the vault has latched a block — none of which changes what a snapshot means.
//
// ExternalCommitDue is the whole of the loop's scheduling knowledge, and that is
// why the floor could be added without touching this file's logic: a burst that
// is ready but still inside its floor is simply not due yet, which is the same
// answer the loop already knew how to act on.
//
// Live reload does not change that split; it changes when the policy is read.
// The loop polls a [PolicySource] before each scan and hands whatever it finds
// to the Manager, so the engine still owns what a floor means and still learns
// it rather than deciding it — and this file still parses nothing. The one piece
// of policy the loop genuinely does own is the *ordering* of a transition: that
// enabled-to-off owes the vault a final snapshot before it takes effect is a
// scheduling statement, and scheduling is what this file is.
//
// That split is why a restart cannot postpone a pending snapshot. The clock
// this loop reads is on disk, so a guard that dies fifteen seconds into a burst
// and comes back thirty seconds later finds the burst already past its quiet
// period rather than starting a fresh one.
const (
	// DefaultPollInterval paces the working-tree scan. It is well under
	// DebounceQuiet so the quiet period is measured with useful resolution, and
	// far enough above a single `git status` that polling a large vault costs a
	// rounding error of one core.
	DefaultPollInterval = 5 * time.Second
	// DefaultPushInterval is the plan's "at least every five minutes" floor for
	// remote attempts, expressed as the longest the loop will go without trying.
	DefaultPushInterval = 5 * time.Minute
	// pushFailureNotifyAfter is how many consecutive push failures the loop
	// tolerates before it says so out loud. One failure is a closed laptop lid;
	// three in a row spans at least a minute and a half of backoff and is worth
	// interrupting someone for.
	pushFailureNotifyAfter = 3
)

// WatchEventKind names what the watcher observed. It is a closed vocabulary so
// a log consumer can switch on it.
type WatchEventKind string

const (
	// WatchEventStarted is emitted once, when the loop begins.
	WatchEventStarted WatchEventKind = "started"
	// WatchEventStopped is emitted once, when the loop returns.
	WatchEventStopped WatchEventKind = "stopped"
	// WatchEventPending reports that a poll found in-scope changes and opened or
	// extended a debounce burst.
	WatchEventPending WatchEventKind = "pending"
	// WatchEventSettled reports that an open burst resolved itself with nothing
	// left to commit.
	WatchEventSettled WatchEventKind = "settled"
	// WatchEventCommitted reports a snapshot of external changes.
	WatchEventCommitted WatchEventKind = "committed"
	// WatchEventPushed reports a successful remote attempt.
	WatchEventPushed WatchEventKind = "pushed"
	// WatchEventPushFailed reports a failed remote attempt. It is operational,
	// never a safety failure.
	WatchEventPushFailed WatchEventKind = "push-failed"
	// WatchEventPushFailing reports a failure streak long enough to tell someone
	// about. It is emitted once per streak, not once per attempt.
	WatchEventPushFailing WatchEventKind = "push-failing"
	// WatchEventBlocked reports that the vault has latched fail-closed state. The
	// loop keeps running and keeps reporting, but it stops asking for snapshots.
	WatchEventBlocked WatchEventKind = "blocked"
	// WatchEventBusy reports that another BrainNotes process held the safety lock
	// for this poll. It is expected and harmless.
	WatchEventBusy WatchEventKind = "busy"
	// WatchEventError reports a failure that is neither a block nor contention.
	WatchEventError WatchEventKind = "error"
	// WatchEventConfigChanged reports that a new autosave policy took effect
	// without a restart. It is emitted when the policy actually changes, not
	// whenever the configuration file's bytes do.
	WatchEventConfigChanged WatchEventKind = "config-changed"
	// WatchEventConfigInvalid reports a configuration version this build will not
	// act on. The last valid policy stays in force. It is emitted once per
	// distinct version rather than once per poll.
	WatchEventConfigInvalid WatchEventKind = "config-invalid"
	// WatchEventConfigRecovered reports that the configuration is readable again
	// after an invalid version.
	WatchEventConfigRecovered WatchEventKind = "config-recovered"
)

// WatchEvent is one thing the watcher observed.
//
// Every field is a path, a count, an object name, or a sanitized diagnostic.
// Nothing read out of a note reaches this struct, which is what makes it safe
// to write to a log file and to hand to a desktop notification.
type WatchEvent struct {
	// Kind names the observation.
	Kind WatchEventKind
	// At is when it happened, in UTC.
	At time.Time
	// Vault is the vault root the loop is watching.
	Vault string
	// Message is a one-clause human explanation with no note contents in it.
	Message string
	// Paths are the vault-relative paths involved, when there are few enough to
	// be worth naming.
	Paths []string
	// PathCount is the untruncated count of involved paths.
	PathCount int
	// Commit is the object name of a snapshot, when one was created.
	Commit string
	// Branch is the ref involved in a snapshot or a push.
	Branch string
	// Attempts is the length of the current push failure streak.
	Attempts int
	// Policy is the canonical autosave policy token the event is about: "off" or
	// a duration spelling such as "5m". It is content-free by construction — the
	// vocabulary is closed and neither half of it can come from a note.
	Policy string
	// Detail is a sanitized single-line diagnostic. It never carries note bytes:
	// everything routed here has already been through agent.SanitizedDiagnostic
	// or is one of this package's own SafetyError reasons.
	Detail string
}

// eventPathCap bounds how many paths one event names. The count stays exact in
// PathCount, so a truncated list never misrepresents the size of a burst.
const eventPathCap = 32

// PolicySnapshot is one read of the vault's configuration source.
type PolicySnapshot struct {
	// Hash fingerprints the exact bytes the source read. It is what makes a
	// reload cheap — two polls of an unedited file compare equal — and it is what
	// makes an invalid version reportable once rather than once per poll, so a
	// source should fill it in even when it goes on to return an error.
	Hash string
	// Policy is what those bytes resolve to. It is meaningful only when the
	// source returned no error.
	Policy config.AutosavePolicy
}

// PolicySource reads the vault's autosave policy from wherever it lives.
//
// It is the seam that lets this package reload a policy without learning to
// parse TOML: the caller owns the file format and hands back the only two things
// scheduling needs, a fingerprint of the bytes and the policy they resolve to.
// An error means the version on disk is one the caller will not act on, and it
// is not a failure of the watcher — the last valid policy stays in force and the
// vault keeps being scanned, protected, and pushed exactly as before.
type PolicySource func() (PolicySnapshot, error)

// WatcherOptions configure a Watcher. The zero value is usable and matches the
// plan's fixed policy; apart from Policy, every field exists for tests rather
// than for users, which is why nothing here is reachable from `brainnotes.toml`
// or from a flag.
type WatcherOptions struct {
	// PollInterval overrides DefaultPollInterval.
	PollInterval time.Duration
	// PushInterval overrides DefaultPushInterval.
	PushInterval time.Duration
	// Observe receives every event. A nil Observe discards them, which is only
	// ever right in a test.
	Observe func(WatchEvent)
	// Now supplies the clock.
	Now func() time.Time
	// Policy re-reads the vault's autosave policy on every poll. A nil Policy
	// means the Manager's policy is fixed for this loop's lifetime, which is the
	// pre-reload behavior and is what every caller that is not the guard wants.
	Policy PolicySource
}

// Watcher polls one managed vault and drives its snapshot and push schedules.
type Watcher struct {
	mgr    *Manager
	poll   time.Duration
	push   time.Duration
	notify func(WatchEvent)
	now    func() time.Time
	policy PolicySource

	// lastPushAttempt is in-process state, unlike everything else the loop reads.
	// It only has to be right within one process lifetime: a restart pushes
	// immediately, which is what the plan asks for anyway.
	lastPushAttempt time.Time
	// pushStreakNotified records the FirstFailureAt of the streak already
	// reported, so a continuing outage is announced once rather than every five
	// minutes, and a new outage after a recovery is announced again.
	pushStreakNotified time.Time
	// blockAnnounced suppresses repeating the block event on every poll. The
	// block itself is persistent; the notification is not.
	blockAnnounced bool
	// lastFingerprint is the previous poll's view of the pending set. It is what
	// separates "the working tree is still dirty" from "the working tree changed
	// again", and without it the quiet period could never elapse: a loop that
	// called NoteExternalChange on every poll of a dirty tree would push
	// LastChangeAt forward faster than DebounceQuiet could catch it, leaving
	// DebounceMaxLatency as the only deadline that ever fired.
	lastFingerprint string

	// policyHash is the fingerprint of the configuration version already acted
	// on, so an unedited file costs one read per poll and no decision at all.
	policyHash string
	// invalidPolicyKey identifies the rejected configuration version already
	// reported. It is what turns "this file has been broken for six hours" into
	// one log line instead of four thousand, and clearing it is what lets the
	// *next* distinct breakage be reported rather than swallowed.
	invalidPolicyKey string
	// pendingOff records that a valid configuration asked for `off` and the final
	// snapshot the vault is owed under the outgoing policy has not landed yet.
	// The intent lives here rather than on disk on purpose: it is worth exactly
	// one process lifetime, because a guard that restarts mid-transition re-reads
	// the same configuration on its first poll and re-derives the same intent.
	pendingOff bool
	// pendingPolicy is what will be published once that snapshot lands.
	pendingPolicy config.AutosavePolicy
}

// NewWatcher builds a watcher over an open Manager.
func NewWatcher(m *Manager, opts WatcherOptions) *Watcher {
	w := &Watcher{
		mgr:    m,
		poll:   opts.PollInterval,
		push:   opts.PushInterval,
		notify: opts.Observe,
		now:    opts.Now,
		policy: opts.Policy,
	}
	if w.poll <= 0 {
		w.poll = DefaultPollInterval
	}
	if w.push <= 0 {
		w.push = DefaultPushInterval
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.notify == nil {
		w.notify = func(WatchEvent) {}
	}
	return w
}

// Run polls until ctx is cancelled, then returns nil.
//
// Cancellation is the only way out and it is always a clean one: every unit of
// work the loop starts is bounded by a single Manager call, so a cancelled
// context finishes the call in flight and stops before the next one rather than
// abandoning a half-taken snapshot. There is no error return worth having —
// every failure this loop can meet is either transient (contention, a network
// round trip) or already durable on disk as a block marker, and neither is a
// reason to take the guard down.
func (w *Watcher) Run(ctx context.Context) error {
	w.emit(WatchEvent{Kind: WatchEventStarted, Message: "watching the vault working tree"})
	defer w.emit(WatchEvent{Kind: WatchEventStopped, Message: "stopped watching the vault working tree"})

	// The first poll runs before the first tick rather than one interval into the
	// loop. A guard that is starting is most often a guard that was just
	// restarted, and the two things that are true at that moment are exactly the
	// two this poll handles: a debounce burst may already be past its deadline,
	// and the remote may never have seen the commits on disk. Waiting out a poll
	// interval first would add latency to both for no reason. The push inside it
	// is unconditional in intent and gated only by the persisted backoff, since
	// lastPushAttempt is zero on a fresh process.
	w.tick(ctx)

	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		w.tick(ctx)
	}
}

// tick is one poll: apply any configuration change, classify the working tree,
// advance the debounce clock, snapshot if the burst is due, and attempt the
// remote if it is time.
//
// The reload runs first, before anything reads the working tree, because every
// decision the rest of the poll makes is a decision under a policy. A poll that
// scanned first and reloaded second would spend one whole interval acting on the
// policy the user just replaced, and in the case that matters most — the user
// turning autosave off — that stale interval is exactly where an unwanted commit
// would land.
func (w *Watcher) tick(ctx context.Context) {
	w.reloadPolicy()
	committed := w.scanAndCommit(ctx)
	w.maybePush(ctx, committed)
}

// reloadPolicy polls the configuration source and applies what it finds.
//
// Its shape follows internal/web's configManager.Refresh, which solved the same
// problem for the server: fingerprint the bytes, keep the last valid publication
// when the new ones do not decode, and recover on the next valid read without a
// restart. What differs is what "publish" means here — a floor the scheduler
// enforces rather than a document requests read — and that one transition owes
// the vault a snapshot before it may take effect.
func (w *Watcher) reloadPolicy() {
	if w.policy == nil {
		return
	}
	snapshot, err := w.policy()
	if err != nil {
		w.reportInvalidPolicy(snapshot, err)
		return
	}
	next := resolveAutosavePolicy(snapshot.Policy)
	if w.invalidPolicyKey != "" {
		w.invalidPolicyKey = ""
		w.emit(WatchEvent{
			Kind:    WatchEventConfigRecovered,
			Message: "the vault configuration is readable again",
			Policy:  next.Canonical,
		})
	}
	// An unchanged fingerprint is the overwhelmingly common case and it is
	// answered without deciding anything. A source that publishes no fingerprint
	// is handled by adoptPolicy, which is a no-op for a policy already in force.
	if snapshot.Hash != "" && snapshot.Hash == w.policyHash {
		return
	}
	w.policyHash = snapshot.Hash
	w.adoptPolicy(next)
}

// reportInvalidPolicy emits at most one event per distinct broken version.
//
// The dedup key is the byte fingerprint, so editing a broken file into a
// differently broken file is reported again while leaving it alone is not. A
// source that could not read the bytes at all has no fingerprint to offer, and
// its diagnostic stands in for one: it is derived from the same failure, so it
// is stable exactly as long as the failure is.
func (w *Watcher) reportInvalidPolicy(snapshot PolicySnapshot, err error) {
	detail := agent.SanitizedDiagnostic(err)
	key := snapshot.Hash
	if key == "" {
		key = "unreadable\x00" + detail
	}
	if key == w.invalidPolicyKey {
		return
	}
	w.invalidPolicyKey = key
	w.emit(WatchEvent{
		Kind:    WatchEventConfigInvalid,
		Message: "the vault configuration could not be applied, so the last valid autosave policy stays in force",
		Policy:  w.mgr.AutosavePolicy().Canonical,
		Detail:  detail,
	})
}

// adoptPolicy decides what a newly read policy means for the loop.
//
// There are four transitions and they are genuinely different:
//
//   - to the same policy — the file's bytes moved but its meaning did not, so
//     nothing is published and nothing is logged;
//   - enabled to enabled — published immediately, so the scan later in this same
//     poll sees the new floor. A shortened interval can make an already-ready
//     burst due right now and a lengthened one can postpone it, which is the
//     point: the user asked for a floor, not for the next floor;
//   - enabled to off — recorded as intent only. The vault is owed one final
//     snapshot under the outgoing policy first, and until that lands the policy
//     in force is still the old one;
//   - off to enabled — published immediately with the burst reset, so whatever
//     the working tree accumulated while off opens a fresh burst on the next
//     dirty scan rather than being committed instantly on a deadline that
//     expired hours ago.
func (w *Watcher) adoptPolicy(next config.AutosavePolicy) {
	current := w.mgr.AutosavePolicy()

	if next.Disabled {
		if current.Disabled {
			return
		}
		// Re-recorded rather than ignored when a transition is already pending, so
		// the intent always names the version most recently read.
		w.pendingOff = true
		w.pendingPolicy = next
		return
	}

	// The target is enabled from here down. A pending transition that never
	// landed is simply abandoned: `off` was never published, so there is nothing
	// to undo and the burst it would have closed is still the live one.
	cancelled := w.pendingOff
	w.pendingOff = false
	w.pendingPolicy = config.AutosavePolicy{}

	switch {
	case current.Disabled:
		w.publishPolicy(next, true, "external autosave is on again; the next scan of a dirty working tree opens a fresh burst")
	case next.Canonical != current.Canonical:
		w.publishPolicy(next, false, "the external autosave floor changed")
	case cancelled:
		w.publishPolicy(next, false, "external autosave stays on; the pending switch to off was withdrawn before it took effect")
	}
}

// publishPolicy makes a policy the one the engine enforces and says so once.
//
// freshBurst asks for the debounce clock to be discarded along with the change,
// which is right for both ends of an off transition and wrong for a change
// between two enabled floors — there, the open burst is exactly what the new
// floor is supposed to reschedule.
func (w *Watcher) publishPolicy(next config.AutosavePolicy, freshBurst bool, message string) {
	if err := w.mgr.SetAutosavePolicy(next); err != nil {
		// The policy is in force either way; only its publication to watch.json
		// failed, and the next state write republishes it.
		w.emit(WatchEvent{
			Kind:    WatchEventError,
			Message: "the new autosave policy could not be published to the vault's watch state",
			Policy:  next.Canonical,
			Detail:  agent.SanitizedDiagnostic(err),
		})
	}
	if freshBurst {
		w.lastFingerprint = ""
		if err := w.mgr.ResetExternalBurst(); err != nil {
			w.emit(WatchEvent{
				Kind:    WatchEventError,
				Message: "the debounce clock could not be reset for the new autosave policy",
				Policy:  next.Canonical,
				Detail:  agent.SanitizedDiagnostic(err),
			})
		}
	}
	w.emit(WatchEvent{Kind: WatchEventConfigChanged, Message: message, Policy: next.Canonical})
}

// finishOffTransition publishes `off` once the vault has been left whole.
//
// It is called from the two places that prove nothing is owed: the scan found an
// empty working tree, and the final snapshot succeeded. Every other path leaves
// the intent standing, which is what makes contention a retry rather than a lost
// transition and a safety failure a refusal rather than a silent switch.
func (w *Watcher) finishOffTransition() {
	if !w.pendingOff {
		return
	}
	target := w.pendingPolicy
	w.pendingOff = false
	w.pendingPolicy = config.AutosavePolicy{}
	w.publishPolicy(target, true, "external autosave is off; the vault is still scanned, protected, and pushed")
}

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
		return false
	}

	// Read once: the rest of this poll must act under one policy, not under
	// whichever one each step happens to observe.
	off := w.mgr.AutosavePolicy().Disabled

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

	if w.pendingOff {
		return w.commitTransitionSnapshot(ctx)
	}
	if off {
		// Everything above this line still runs while off — the scan, the deletion
		// check it performs, the pending event — and everything below it does not.
		// Turning autosave off asks BrainNotes to stop writing history on its own
		// initiative; it does not ask it to stop watching, and a note deleted by a
		// sync client while off must still latch the block marker.
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
// a snapshot checks still applies — the lock, the deletion proof, the block
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

func (w *Watcher) emit(event WatchEvent) {
	event.At = w.now().UTC()
	event.Vault = w.mgr.Root()
	w.notify(event)
}

// fingerprint summarizes the pending set so that two consecutive polls of an
// unchanged working tree compare equal.
//
// It stats only the paths that already differ from HEAD, so the cost is
// proportional to what is in flight rather than to the size of the vault. Size
// and modification time are the same evidence Git's own index staleness check
// uses; the digest exists so the comparison is one string rather than a nested
// walk, and it is never logged or persisted — a note's path is not secret, but
// there is no reason for a scheduling detail to carry one.
//
// Its one blind spot is a second write to the same path with the same length
// inside the same nanosecond, which would let the quiet period start from the
// first write rather than the second. The cost of that is a snapshot taken up
// to one poll interval early, of bytes that are current either way.
func (w *Watcher) fingerprint(summary PendingSummary) string {
	sum := sha256.New()
	for _, rel := range summaryPaths(summary) {
		fmt.Fprintf(sum, "%s\x00", rel)
		info, err := os.Lstat(filepath.Join(w.mgr.Root(), filepath.FromSlash(rel)))
		if err != nil {
			// A pending deletion has nothing to stat, and so does a path that
			// vanished between the scan and this call. Both are "absent", and both
			// are a change from a poll that found the file present.
			fmt.Fprint(sum, "absent\x00")
			continue
		}
		fmt.Fprintf(sum, "%d/%d\x00", info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func summaryPaths(summary PendingSummary) []string {
	paths := make([]string, 0, summary.Count())
	paths = append(paths, summary.Added...)
	paths = append(paths, summary.Modified...)
	paths = append(paths, summary.Deleted...)
	sort.Strings(paths)
	return paths
}

func capPaths(paths []string) []string {
	if len(paths) <= eventPathCap {
		return paths
	}
	return paths[:eventPathCap]
}
