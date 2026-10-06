package vaultgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// The watcher is the scheduling half of this package. The Manager knows how to
// take a snapshot safely; the watcher knows when to ask for one.
//
// It deliberately owns no policy of its own. The debounce deadlines live in the
// persisted WatchState, the autosave floor lives in the Manager's resolved
// config.AutosavePolicy, the push backoff lives in the persisted PushState, and
// repository validation and removal checks live in the Manager. What the
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
	// The intent lives here rather than in a file of its own because the vault
	// already carries a durable statement of the policy the last guard was
	// enforcing — see resumeInterruptedOffTransition, which rebuilds this pair
	// from it on the first poll of a new process.
	pendingOff bool
	// pendingPolicy is what will be published once that snapshot lands.
	pendingPolicy config.AutosavePolicy
	// resumeChecked records that the startup question — is this vault mid-way
	// through a switch to `off`? — has been asked. It is asked once per process,
	// because after that this loop is the thing that knows the answer.
	resumeChecked bool
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
	// restarted, and the three things that may be true at that moment are exactly
	// the three this poll handles: a debounce burst may already be past its
	// deadline, a switch to `off` may be owed a snapshot the previous process
	// never took, and the remote may never have seen the commits on disk. Waiting
	// out a poll interval first would add latency to all three for no reason. The
	// push inside it is unconditional in intent and gated only by the persisted
	// backoff, since lastPushAttempt is zero on a fresh process.
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

// tick is one poll: recover an interrupted transition, apply any configuration
// change, classify the working tree, advance the debounce clock, snapshot if the
// burst is due, and attempt the remote if it is time.
//
// The reload runs before anything reads the working tree, because every decision
// the rest of the poll makes is a decision under a policy. A poll that scanned
// first and reloaded second would spend one whole interval acting on the policy
// the user just replaced, and in the case that matters most — the user turning
// autosave off — that stale interval is exactly where an unwanted commit would
// land.
//
// The recovery runs before even that, and only on the first poll of a process,
// because the reload cannot see what it is about: a configuration that already
// says `off` reads identically whether the transition to it finished or was
// interrupted, and only the vault's durable state separates the two.
func (w *Watcher) tick(ctx context.Context) {
	w.resumeInterruptedOffTransition()
	w.reloadPolicy()
	committed := w.scanAndCommit(ctx)
	w.maybePush(ctx, committed)
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
