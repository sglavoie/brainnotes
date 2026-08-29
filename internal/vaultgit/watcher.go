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
)

// The watcher is the scheduling half of this package. The Manager knows how to
// take a snapshot safely; the watcher knows when to ask for one.
//
// It deliberately owns no policy of its own. The debounce deadlines live in the
// persisted WatchState, the push backoff lives in the persisted PushState, and
// the refusal to commit an unexplained deletion lives in the Manager. What the
// watcher adds is a loop, a poll interval, and the decision to stop asking once
// the vault has latched a block — none of which changes what a snapshot means.
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
	// Detail is a sanitized single-line diagnostic. It never carries note bytes:
	// everything routed here has already been through agent.SanitizedDiagnostic
	// or is one of this package's own SafetyError reasons.
	Detail string
}

// eventPathCap bounds how many paths one event names. The count stays exact in
// PathCount, so a truncated list never misrepresents the size of a burst.
const eventPathCap = 32

// WatcherOptions configure a Watcher. The zero value is usable and matches the
// plan's fixed policy; every field exists for tests rather than for users, which
// is why nothing here is reachable from `brainnotes.toml` or from a flag.
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
}

// Watcher polls one managed vault and drives its snapshot and push schedules.
type Watcher struct {
	mgr    *Manager
	poll   time.Duration
	push   time.Duration
	notify func(WatchEvent)
	now    func() time.Time

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
}

// NewWatcher builds a watcher over an open Manager.
func NewWatcher(m *Manager, opts WatcherOptions) *Watcher {
	w := &Watcher{
		mgr:    m,
		poll:   opts.PollInterval,
		push:   opts.PushInterval,
		notify: opts.Observe,
		now:    opts.Now,
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

// tick is one poll: classify the working tree, advance the debounce clock,
// snapshot if the burst is due, and attempt the remote if it is time.
func (w *Watcher) tick(ctx context.Context) {
	committed := w.scanAndCommit(ctx)
	w.maybePush(ctx, committed)
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
		return false
	}

	if current := w.fingerprint(summary); current != w.lastFingerprint {
		w.lastFingerprint = current
		if _, err := w.mgr.NoteExternalChange(); err != nil {
			// The clock is a scheduling hint; losing a write to it costs at most one
			// extra poll before the burst is reopened.
			w.emit(WatchEvent{Kind: WatchEventError, Message: "the debounce clock could not be advanced", Detail: agent.SanitizedDiagnostic(err)})
		}
		paths := summaryPaths(summary)
		w.emit(WatchEvent{
			Kind:      WatchEventPending,
			Message:   "the working tree differs from the content history",
			Paths:     capPaths(paths),
			PathCount: len(paths),
		})
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
	recorded := append(append([]string{}, result.Added...), result.Deleted...)
	sort.Strings(recorded)
	w.emit(WatchEvent{
		Kind:      WatchEventCommitted,
		Message:   "external changes were recorded in the content history",
		Commit:    result.Commit,
		Branch:    result.Branch,
		Paths:     capPaths(recorded),
		PathCount: len(recorded),
	})
	return true
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
