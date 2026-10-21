package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sglavoie/brainnotes/internal/restorestate"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	// DefaultDrain bounds how long a checkpoint waits for the writers that were
	// already running when it announced. It is several times the wait budget a
	// single writer gives its locks, because what it waits for is a whole
	// operation — stage, journal, apply, snapshot — rather than one acquisition.
	DefaultDrain = 30 * time.Second
	// DefaultLease is the lifetime a checkpoint records for itself. Nothing
	// enforces it: the flock is what holds the barrier and it is held for exactly
	// as long as the client lives. It is recorded so a person looking at a vault
	// that is refusing writes can tell whether the client is taking longer than it
	// meant to, and so a supervisor has a number to kill it against.
	DefaultLease = 10 * time.Minute
)

var (
	// ErrDrainTimeout means writers that were already running did not finish
	// within the drain budget. Nothing was captured and nothing was disturbed.
	ErrDrainTimeout = errors.New("checkpoint: writers did not drain")
	// ErrCheckpointActive means another live client already holds this vault.
	ErrCheckpointActive = errors.New("checkpoint: another checkpoint is already running")
	// ErrRestorePending means a killed restore still has content/history work to
	// finish. Only RestoreBackup may reacquire the barrier to resume it.
	ErrRestorePending = errors.New("checkpoint: an interrupted backup restore must be resumed")
	// ErrNested means the calling process is inside a Join of the same vault.
	// Proceeding would wait out the drain budget against the process's own shared
	// hold, so it is refused immediately and named for what it is.
	ErrNested = errors.New("checkpoint: this process is inside a write that joined the barrier")
)

// Options configures one Hold. The zero value is the documented default.
type Options struct {
	// Reason is a short human sentence recorded in the announcement, so a person
	// who finds a vault refusing writes learns what is holding it.
	Reason string
	// Drain bounds the wait for already-running writers. Zero means DefaultDrain.
	Drain time.Duration
	// Lease is the lifetime recorded for observers. Zero means DefaultLease.
	Lease time.Duration
	// Now overrides the clock, for tests.
	Now func() time.Time
	// AllowRestoreRecovery is reserved for noteops.RestoreBackup. Ordinary
	// checkpoints fail closed while an interrupted content/history restore is
	// pending; the restore entry point uses this to reacquire the barrier and
	// finish the recorded operation.
	AllowRestoreRecovery bool
}

func (o Options) drain() time.Duration {
	if o.Drain <= 0 {
		return DefaultDrain
	}
	return o.Drain
}

func (o Options) lease() time.Duration {
	if o.Lease <= 0 {
		return DefaultLease
	}
	return o.Lease
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Held is a checkpoint in progress. While it exists the vault is quiet and may
// be copied; releasing it lets writers back in.
type Held struct {
	paths    vaultPaths
	id       string
	deadline time.Time
	barrier  *vault.FileLock
	claim    *vault.FileLock
	once     sync.Once
	err      error
	active   *atomic.Bool
}

// ID reports the checkpoint's identity, which is what a copy taken under it
// should record so the two can be matched later.
func (h *Held) ID() string { return h.id }

// Root reports the resolved vault root the checkpoint covers.
func (h *Held) Root() string { return h.paths.root }

// Deadline reports the instant the announcement said this checkpoint expected to
// be finished by. It is advisory; nothing expires the hold but the client
// releasing or dying.
func (h *Held) Deadline() time.Time { return h.deadline }

// Context marks ctx as covered by this exclusive hold. It is for recovery and
// restore work that must call ordinary durable writers while the vault is held;
// those nested writers must not try to join the barrier they are already under.
func (h *Held) Context(ctx context.Context) context.Context {
	if h == nil {
		return ctx
	}
	return markContext(ctx, h.paths.root, h.active)
}

// InheritanceFiles returns the lock descriptors an exec child must inherit so
// killing the wrapper cannot release the checkpoint while that child continues
// copying. The caller must not close them; Release retains ownership.
func (h *Held) InheritanceFiles() []*os.File {
	if h == nil {
		return nil
	}
	files := make([]*os.File, 0, 2)
	if h.claim != nil && h.claim.File() != nil {
		files = append(files, h.claim.File())
	}
	if h.barrier != nil && h.barrier.File() != nil {
		files = append(files, h.barrier.File())
	}
	return files
}

// Hold drains the vault and holds the checkpoint barrier.
//
// On return the vault is quiet in the sense the package doc describes: every
// acknowledged write is complete, any transaction a dead process left prepared
// has been rolled forward, and no new write can start until Release. The caller
// owns that state until it releases, and must release — a client that returns
// without releasing holds the vault until its process exits.
func Hold(root string, opts Options) (*Held, error) {
	paths, err := resolve(root)
	if err != nil {
		return nil, err
	}
	if Joined(paths.root) {
		return nil, ErrNested
	}
	claim, err := takeClaim(paths)
	if err != nil {
		return nil, err
	}
	held, err := announce(paths, claim, opts)
	if err != nil {
		_ = claim.Release()
		return nil, err
	}
	return held, nil
}

// claimGrace bounds how long Hold keeps asking for a claim lock somebody else
// holds before it refuses as ErrCheckpointActive.
//
// It is not a queue for a second client — the vault can only be quiet for one
// checkpoint at a time, and a caller told to come back later can decide that for
// itself. It exists because the claim lock has one other kind of holder: a
// writer's liveness probe (clientLive) try-locks it for an instant to learn
// whether an announcing client is still alive. Refusing on the first attempt
// would turn that probe into a spurious "another checkpoint is already
// running". A probe holds the lock for microseconds and a live client for the
// whole checkpoint, so a few polls tell the two apart.
const claimGrace = 50 * time.Millisecond

// takeClaim acquires the claim lock within claimGrace.
func takeClaim(paths vaultPaths) (*vault.FileLock, error) {
	claim, err := vault.LockFileHandle(paths.claim, true, claimGrace)
	if err == nil {
		return claim, nil
	}
	if vault.IsTxnLockBusy(err) {
		return nil, ErrCheckpointActive
	}
	return nil, fmt.Errorf("checkpoint: claim vault: %w", err)
}

// announce writes the intent, drains, settles, and promotes the record.
//
// The order is the contract. The announcement is durable before the exclusive
// acquisition starts, because it is what makes the acquisition terminate; the
// settle pass runs after the barrier is held and before the record says held,
// because a transaction rolled forward while writers could still start would not
// stay rolled forward. Every failure after the announcement clears it, so a
// refused Hold leaves the vault exactly as it found it.
func announce(paths vaultPaths, claim *vault.FileLock, opts Options) (*Held, error) {
	rec, err := intent(opts)
	if err != nil {
		return nil, err
	}
	if err := writeRecord(paths.record, rec); err != nil {
		return nil, err
	}
	checkpointCrash(crashAnnounced)
	barrier, err := drain(paths, opts.drain())
	if err != nil {
		_ = clearRecord(paths.record)
		return nil, err
	}
	checkpointCrash(crashDrained)
	active := &atomic.Bool{}
	active.Store(true)
	held := &Held{paths: paths, id: rec.ID, deadline: rec.Deadline, barrier: barrier, claim: claim, active: active}
	if err := promote(paths, rec, opts.AllowRestoreRecovery); err != nil {
		_ = held.Release()
		return nil, err
	}
	checkpointCrash(crashHeld)
	return held, nil
}

// intent builds the announcement this client is about to write.
func intent(opts Options) (*record, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	at := opts.now()
	host, _ := os.Hostname()
	return &record{
		Version:     recordVersion,
		ID:          id,
		Phase:       PhaseDraining,
		Reason:      opts.Reason,
		PID:         os.Getpid(),
		Host:        host,
		AnnouncedAt: at,
		Deadline:    at.Add(opts.lease()),
	}, nil
}

// drain takes the barrier exclusively once the writers already inside have
// finished. A budget that runs out is not a failure of the vault: it means the
// vault is busier than the caller was willing to wait for, and the caller may
// simply try again.
func drain(paths vaultPaths, budget time.Duration) (*vault.FileLock, error) {
	barrier, err := vault.LockFileHandle(paths.barrier, true, budget)
	if err == nil {
		return barrier, nil
	}
	if vault.IsTxnLockBusy(err) {
		return nil, fmt.Errorf("%w within %s: retry when the vault is quieter", ErrDrainTimeout, budget)
	}
	return nil, err
}

// promote runs the settle pass and rewrites the record as held.
func promote(paths vaultPaths, rec *record, allowRestoreRecovery bool) error {
	if err := settle(paths.root); err != nil {
		return err
	}
	pending, err := restorestate.Pending(paths.root)
	if err != nil {
		return fmt.Errorf("checkpoint: inspect pending restore: %w", err)
	}
	if pending && !allowRestoreRecovery {
		return ErrRestorePending
	}
	rec.Phase = PhaseHeld
	return writeRecord(paths.record, rec)
}

// settle finishes any transaction a process died part-way through.
//
// internal/vault rolls a prepared transaction forward rather than back, and does
// it from the chokepoint every writer passes: taking a path lock. Taking an
// empty set of path locks is therefore the way to ask for that pass and nothing
// else — it runs the recovery, acquires no per-path lock, and returns whatever
// the recovery decided. Running it here, with the barrier already held, is what
// makes the difference between a copy that captures a half-applied transaction
// and one that captures a finished one.
//
// A recovery that fails is returned rather than swallowed. A vault holding an
// undecided half-applied transaction refuses further writes, and a backup of it
// would be a backup of that refusal.
func settle(root string) error {
	release, err := vault.LockPaths(root)
	if err != nil {
		return fmt.Errorf("checkpoint: settle interrupted transactions: %w", err)
	}
	release()
	return nil
}

// Release lets writers back in.
//
// The barrier flock is dropped before the announcement is cleared, so a writer
// that sees the announcement gone never finds the flock still taken. The window
// between them is the other way round — the record says held while nothing holds
// it — which costs a writer one poll interval and cannot cost correctness.
//
// It is idempotent, so a caller may both defer it and call it explicitly on the
// success path to release before it does something slow.
func (h *Held) Release() error {
	if h == nil {
		return nil
	}
	h.once.Do(func() {
		if h.active != nil {
			h.active.Store(false)
		}
		if h.barrier != nil {
			_ = h.barrier.Release()
		}
		h.err = clearRecord(h.paths.record)
		checkpointCrash(crashReleased)
		if h.claim != nil {
			_ = h.claim.Release()
		}
	})
	return h.err
}
