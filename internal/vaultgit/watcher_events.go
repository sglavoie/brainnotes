package vaultgit

import (
	"sort"
	"time"
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

func (w *Watcher) emit(event WatchEvent) {
	event.At = w.now().UTC()
	event.Vault = w.mgr.Root()
	w.notify(event)
}
