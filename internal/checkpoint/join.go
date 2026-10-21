package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/restorestate"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// DefaultWriterWait bounds how long a writer waits for a checkpoint before it
// gives up. It is generous relative to copying a vault's metadata and short
// enough that an interactive command refuses with an explanation instead of
// appearing to hang.
const DefaultWriterWait = 15 * time.Second

// joinPollInterval paces the wait for an announced checkpoint to clear.
const joinPollInterval = 10 * time.Millisecond

// ErrBarrierHeld is what a writer gets when a checkpoint held the vault for
// longer than the writer was willing to wait. It is retryable and means nothing
// was attempted: the writer joined nothing, locked nothing, and wrote nothing.
var ErrBarrierHeld = errors.New("checkpoint: a checkpoint is holding this vault")

// Join enlists the caller in the vault's checkpoint barrier and returns the
// release function.
//
// Every durable change to a vault goes through here first, before any vault
// lock, and holds the join until the change is complete — which for a journaled
// mutation means until its content-history snapshot has landed, not until its
// bytes have. release is idempotent and safe to defer, and callers are expected
// to defer it: a join that is only released on the success path leaks the
// barrier on every error return, and in bn serve a leaked join blocks every
// later checkpoint in the process for as long as it runs.
//
// Join starts a new logical write. Code that calls another durable writer while
// already joined must use JoinContext and pass the returned context through to
// that writer. Keeping logical nesting explicit is what lets a checkpoint stop
// a new request in this process while an older request is still draining.
func Join(root string) (func(), error) { return JoinWithin(root, DefaultWriterWait) }

// JoinWithin is Join with an explicit wait budget. A non-positive wait means
// DefaultWriterWait.
func JoinWithin(root string, wait time.Duration) (func(), error) {
	_, release, err := JoinContextWithin(context.Background(), root, wait)
	return release, err
}

// JoinContext enlists a logical write and marks the returned context as covered
// by that join. Passing the context to a nested durable writer makes its join a
// no-op; using a fresh context starts a fresh admission even when another
// goroutine in this process is already writing the same vault.
func JoinContext(ctx context.Context, root string) (context.Context, func(), error) {
	return JoinContextWithin(ctx, root, DefaultWriterWait)
}

// JoinContextWithin is JoinContext with an explicit wait budget.
func JoinContextWithin(ctx context.Context, root string, wait time.Duration) (context.Context, func(), error) {
	noop := func() {}
	paths, err := resolve(root)
	if err != nil {
		return ctx, noop, err
	}
	if contextCovers(ctx, paths.root) {
		return ctx, noop, nil
	}
	if wait <= 0 {
		wait = DefaultWriterWait
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(wait)
	refs := slotFor(paths.root)
	for {
		handle, turn, admitErr := refs.admit(ctx, paths, deadline)
		if admitErr != nil {
			return ctx, noop, admitErr
		}
		if handle != nil {
			return markActiveContext(ctx, paths.root, handle)
		}
		if err := awaitTurn(ctx, turn, deadline); err != nil {
			return ctx, noop, err
		}
	}
}

// admit makes one attempt to count a fresh logical write against this root and
// returns its release handle. Without a handle, turn is what to wait on before
// trying again: another goroutine's in-flight first acquisition, or nil when a
// fresh in-process writer must poll for an announced checkpoint to clear.
func (r *references) admit(ctx context.Context, paths vaultPaths, deadline time.Time) (func(), <-chan struct{}, error) {
	r.mu.Lock()
	if r.depth > 0 {
		defer r.mu.Unlock()
		return r.admitNested(paths)
	}
	if r.acquiring != nil {
		turn := r.acquiring
		r.mu.Unlock()
		return nil, turn, nil
	}
	turn := make(chan struct{})
	r.acquiring = turn
	r.mu.Unlock()
	return r.acquireFirst(ctx, paths, deadline, turn)
}

// acquireFirst takes the process's shared flock outside r.mu; see references
// for why the mutex is not held across the wait.
func (r *references) acquireFirst(ctx context.Context, paths vaultPaths, deadline time.Time, turn chan struct{}) (func(), <-chan struct{}, error) {
	var release func()
	err := errors.New("checkpoint: shared acquisition did not complete")
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.acquiring = nil
		close(turn)
		if err == nil {
			r.depth = 1
			r.release = release
		}
	}()
	release, err = acquireShared(ctx, paths, remaining(deadline))
	if err != nil {
		return nil, nil, err
	}
	return r.handle(), nil, nil
}

// admitNested counts one more admission against a flock the process already
// owns. It is safe only while no checkpoint has announced. Once announced, this
// is a new writer and must stand aside just like a writer in another process;
// true nested work has been identified by contextCovers. The caller holds r.mu.
func (r *references) admitNested(paths vaultPaths) (func(), <-chan struct{}, error) {
	blocked, err := announced(paths)
	if err != nil || blocked {
		return nil, nil, err
	}
	r.depth++
	return r.handle(), nil, nil
}

// awaitTurn waits for turn to close — or one poll interval when turn is nil —
// for the deadline, or for ctx, and reports why the admission must give up.
func awaitTurn(ctx context.Context, turn <-chan struct{}, deadline time.Time) error {
	left := time.Until(deadline)
	if left <= 0 {
		return fmt.Errorf("%w: it is copying the vault; retry once it finishes", ErrBarrierHeld)
	}
	if turn == nil {
		return pollPause(ctx)
	}
	timer := time.NewTimer(left)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("checkpoint: wait for checkpoint to clear: %w", ctx.Err())
	case <-turn:
	case <-timer.C:
	}
	return nil
}

type joinedContext struct {
	root   string
	parent *joinedContext
	active *atomic.Bool
}

type joinedContextKey struct{}

func contextCovers(ctx context.Context, root string) bool {
	if ctx == nil {
		return false
	}
	for joined, _ := ctx.Value(joinedContextKey{}).(*joinedContext); joined != nil; joined = joined.parent {
		if joined.root == root && joined.active != nil && joined.active.Load() {
			return true
		}
	}
	return false
}

func markContext(ctx context.Context, root string, active *atomic.Bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	parent, _ := ctx.Value(joinedContextKey{}).(*joinedContext)
	return context.WithValue(ctx, joinedContextKey{}, &joinedContext{root: root, parent: parent, active: active})
}

func markActiveContext(ctx context.Context, root string, release func()) (context.Context, func(), error) {
	active := &atomic.Bool{}
	active.Store(true)
	var once sync.Once
	wrapped := func() {
		once.Do(func() {
			active.Store(false)
			release()
		})
	}
	return markContext(ctx, root, active), wrapped, nil
}

// Joined reports whether this process is currently inside a join of the vault at
// root. It is what lets Hold refuse a self-deadlock with a sentence instead of a
// drain timeout.
func Joined(root string) bool {
	paths, err := resolve(root)
	if err != nil {
		return false
	}
	refs := slotFor(paths.root)
	refs.mu.Lock()
	defer refs.mu.Unlock()
	return refs.depth > 0
}

// acquireShared waits for any announced checkpoint to clear and then takes the
// barrier shared.
//
// The announcement is checked before the flock rather than after it. Checking
// after would make an already-admitted writer back out, which is exactly the
// writer the checkpoint is waiting for; checking before is what stops new
// writers from joining so the shared holders can drain to zero. A writer that
// slips in between the check and the acquisition is admitted, and the
// checkpoint's drain budget covers it — the record is written once, so at most
// the writers already in flight at that instant can win the race.
//
// Only a zero wait means the default. A negative wait is what remaining hands
// over once the caller's deadline has passed, and it buys exactly one look and
// one lock attempt: other writers in the process queue behind this acquisition,
// so turning an expired budget into a fresh DefaultWriterWait would stall them
// for that long.
func acquireShared(ctx context.Context, paths vaultPaths, wait time.Duration) (func(), error) {
	if wait == 0 {
		wait = DefaultWriterWait
	}
	deadline := time.Now().Add(wait)
	for {
		pending, err := restorestate.Pending(paths.root)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: inspect pending restore before write: %w", err)
		}
		if pending {
			return nil, ErrRestorePending
		}
		blocked, err := announced(paths)
		if err != nil {
			return nil, err
		}
		if !blocked {
			return takeShared(ctx, paths, remaining(deadline))
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w: it is copying the vault; retry once it finishes", ErrBarrierHeld)
		}
		if err := pollPause(ctx); err != nil {
			return nil, err
		}
	}
}

// pollPause waits one poll interval for an announced checkpoint to clear, or
// returns early when the caller's context ends. A request whose client has gone
// away must not keep holding the per-root mutex for the whole writer budget.
func pollPause(ctx context.Context) error {
	timer := time.NewTimer(joinPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("checkpoint: wait for checkpoint to clear: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// remaining turns a deadline into a lock wait budget. A deadline already behind
// us becomes a single attempt rather than zero, which vault.LockFile would read
// as "use the default budget" and turn an expired wait into a fresh one.
func remaining(deadline time.Time) time.Duration {
	left := time.Until(deadline)
	if left <= 0 {
		return -1
	}
	return left
}

// takeShared is the flock half, with a contended acquisition reported as the
// same refusal an announced checkpoint gets. The two are the same situation seen
// from either side of the race above, and a caller that distinguished them would
// be reasoning about which one it lost.
//
// The flock wait is the long one — it lasts as long as a checkpoint's copy — so
// it is the one that most needs to honour ctx.
func takeShared(ctx context.Context, paths vaultPaths, wait time.Duration) (func(), error) {
	release, err := vault.LockFileContext(ctx, paths.barrier, false, wait)
	if err == nil {
		return release, nil
	}
	if vault.IsTxnLockBusy(err) {
		return nil, errors.Join(ErrBarrierHeld, err)
	}
	return nil, err
}

// announced reports whether a live checkpoint client has claimed this vault.
//
// Both halves are needed and neither is sufficient. Without the record a writer
// cannot tell it should stand aside, and the exclusive acquisition starves;
// without the liveness test a client that died holding the record would refuse
// every write in the vault until somebody deleted a file by hand.
func announced(paths vaultPaths) (bool, error) {
	rec, err := readRecord(paths.record)
	if err != nil || rec == nil {
		return false, err
	}
	return clientLive(paths)
}

// clientLive reports whether some process still holds the claim lock.
//
// A free claim lock proves the client is gone, because the kernel drops an flock
// when the process holding it dies, however it died. The record it left is not
// removed here: this caller is a writer, and a writer that deleted an
// announcement could delete a fresh one a new client had just written. The next
// Hold owns the claim lock and clears it, which is the one place that cannot
// race.
func clientLive(paths vaultPaths) (bool, error) {
	release, acquired, err := vault.TryLockPath(paths.claim)
	if err != nil {
		return false, fmt.Errorf("checkpoint: test checkpoint liveness: %w", err)
	}
	if acquired {
		release()
		return false, nil
	}
	return true, nil
}

// references counts one vault root's joins inside this process.
//
// The invariant the count depends on is that the flock is taken exactly once
// per process per vault, so the process contends with other processes and never
// with itself. The first acquisition can wait out a whole checkpoint, so it
// runs outside mu: the goroutine taking the flock publishes acquiring, and any
// other goroutine joining the same vault meanwhile waits for that channel to
// close — under its own context and deadline — and then tries again, rather
// than queueing uncancellably on mu. mu itself guards only bookkeeping and the
// wait-free announcement probe of a nested admission.
type references struct {
	mu      sync.Mutex
	depth   int
	release func()
	// acquiring is non-nil while one goroutine is taking the shared flock for a
	// depth of zero, and is closed when that attempt ends either way.
	acquiring chan struct{}
}

var (
	registryMu sync.Mutex
	registry   = map[string]*references{}
)

// slotFor returns the reference count for a resolved vault root, creating it on
// first use. Entries are never removed: there are as many as there are vaults
// one process has written to, and removing one would race with a goroutine
// holding its mutex.
func slotFor(realRoot string) *references {
	registryMu.Lock()
	defer registryMu.Unlock()
	refs, ok := registry[realRoot]
	if !ok {
		refs = &references{}
		registry[realRoot] = refs
	}
	return refs
}

// handle returns a release function that decrements exactly once however many
// times it is called, so a caller may both defer it and call it early.
func (r *references) handle() func() {
	var once sync.Once
	return func() { once.Do(r.drop) }
}

func (r *references) drop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.depth == 0 {
		return
	}
	r.depth--
	if r.depth > 0 {
		return
	}
	if r.release != nil {
		r.release()
		r.release = nil
	}
}
