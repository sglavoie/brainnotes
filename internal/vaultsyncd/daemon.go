package vaultsyncd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// DefaultInterval paces the loop.
//
// It is three times internal/vaultgit's poll interval on purpose. That loop
// watches a working tree a person is typing into and its latency is felt
// directly; this one waits on a file-synchronization transport and on another
// machine, where fifteen seconds is inside the noise. The cost of a pass is a
// directory listing, two git calls, and a state file read, so the interval is
// chosen for how fresh the *status* should be rather than for throughput.
const DefaultInterval = 15 * time.Second

// LockRelPath is the single-instance lock, beside the per-note locks under
// `.bnotes/locks/` that the vault's own ignore rules already exclude — so the
// companion's coordination file can never become a pending change the content
// history would try to commit, and can never become a file the exporter would
// try to send.
const LockRelPath = vaultsync.CompanionLockRelPath

// ErrAlreadyRunning is the refusal a second companion on one vault gets.
var ErrAlreadyRunning = errors.New("vaultsyncd: another companion already holds this vault")

// LockPath reports where the single-instance lock for root lives.
func LockPath(root string) string {
	return vaultsync.CompanionLockPath(root)
}

// Options configure a companion. The zero value is usable and is what the
// command runs with; every field here exists for a test or for an operator
// override, and none of them is reachable from `brainnotes.toml`.
type Options struct {
	// Now supplies the clock.
	Now func() time.Time
	// Interval overrides DefaultInterval.
	Interval time.Duration
	// Command is what an applied package is recorded as having been applied by,
	// in this vault's own recovery history. It defaults to the companion's name
	// so an operator reading `bn undo` can tell a synchronized change from one
	// they made.
	Command string
	// GitExecutable overrides the git found on PATH.
	GitExecutable string
	// MaxPackages bounds one export pass. Zero takes internal/vaultexport's own
	// bound.
	MaxPackages int
	// DisconnectedAfter overrides DefaultDisconnectedAfter.
	DisconnectedAfter time.Duration
	// AuthorizeDeletions is passed through for compatibility with older callers.
	// Deprecated: committed regular-file deletions are exported automatically.
	AuthorizeDeletions []string
	// Observe receives every event. A nil Observe discards them, which is only
	// ever right in a test.
	Observe func(Event)
}

const defaultCommand = "bn-vault-sync"

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now().UTC()
	}
	return o.Now().UTC()
}

func (o Options) interval() time.Duration {
	if o.Interval <= 0 {
		return DefaultInterval
	}
	return o.Interval
}

func (o Options) command() string {
	if o.Command == "" {
		return defaultCommand
	}
	return o.Command
}

func (o Options) readOptions() ReadOptions {
	return ReadOptions{
		Now:               o.Now,
		GitExecutable:     o.GitExecutable,
		DisconnectedAfter: o.DisconnectedAfter,
	}
}

// UnitResult is what one worker did in one pass.
type UnitResult struct {
	Worker WorkerName `json:"worker"`
	// Ran reports whether the worker was invoked at all.
	Ran bool `json:"ran"`
	// Skipped is why it was not, in the words an operator would use. A skip is
	// never a failure and never lengthens a backoff.
	Skipped string `json:"skipped,omitempty"`
	// Exactly one of these is set on a worker that ran, unless it failed before
	// producing anything.
	Import *ImportOutcome `json:"import,omitempty"`
	Export *ExportOutcome `json:"export,omitempty"`
	Retire *RetireOutcome `json:"retire,omitempty"`
	// Error is the sanitized failure. The unsanitized one is not carried: a Pass
	// is rendered as JSON into a log, and nothing in this package needs to
	// inspect a worker's error after the pass that recorded it.
	Error string `json:"error,omitempty"`
}

// Pass is one bounded sweep over the three workers.
type Pass struct {
	Root       string       `json:"root"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	Enrolled   bool         `json:"enrolled"`
	Units      []UnitResult `json:"units,omitempty"`
	// Status is the vault as it stood when the pass finished. It is read after
	// the work rather than before it, so a report and the vault it describes
	// cannot disagree about what the pass just did.
	Status *Status `json:"status,omitempty"`
}

// Failed reports whether any worker in the pass failed.
func (p *Pass) Failed() bool {
	for _, unit := range p.Units {
		if unit.Error != "" {
			return true
		}
	}
	return false
}

// Run drives the companion until ctx is cancelled.
//
// It holds the single-instance lock for its whole lifetime and returns
// ErrAlreadyRunning rather than queueing behind another companion: two of these
// on one vault would take turns failing each other's import guard and doubling
// every git call for nothing.
//
// A cancelled context is a clean stop and not an error. Shutdown is graceful in
// the only sense this loop can offer and needs: the unit in flight finishes and
// the loop stops between units. Every unit is one call into a package that is
// already crash-consistent — internal/vaultimport writes an fsynced attempt
// record before the first byte moves, internal/vaultexport reserves a sequence
// before it publishes, and retirement removes only what a verified receipt has
// already made redundant — so a companion killed outright loses no more than one
// that was asked politely.
func Run(ctx context.Context, root string, opts Options) error {
	return locked(root, func(c *companion) error {
		c.emit(Event{Kind: EventStarted, Message: "watching the vault synchronization channels"})
		defer c.emit(Event{Kind: EventStopped, Message: "stopped watching the vault synchronization channels"})

		// The first pass runs before the first tick, for internal/vaultgit's
		// reason: a companion that is starting is usually one that was restarted,
		// and a restart is exactly when there is most likely to be an interrupted
		// import to resolve, a package that arrived while nothing was running, and
		// a backoff that has long since elapsed.
		c.pass(ctx)

		ticker := time.NewTicker(opts.interval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			c.pass(ctx)
		}
	}, opts)
}

// Once runs a single bounded pass and returns what it did.
//
// It takes the same single-instance lock as Run, so it refuses while a companion
// is running rather than racing it. That is the point of the mode: it is what an
// operator runs on a machine where the companion is *not* installed, and what a
// test drives instead of a loop.
func Once(ctx context.Context, root string, opts Options) (*Pass, error) {
	var pass *Pass
	err := locked(root, func(c *companion) error {
		pass = c.pass(ctx)
		return nil
	}, opts)
	return pass, err
}

// locked resolves the root, takes the single-instance lock, and runs body.
func locked(root string, body func(*companion) error, opts Options) error {
	root, err := resolveRoot(root)
	if err != nil {
		return err
	}
	release, acquired, err := vaultTryLock(root)
	if err != nil {
		return err
	}
	defer release()
	if !acquired {
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, root)
	}
	return body(newCompanion(root, opts))
}
