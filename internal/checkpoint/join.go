package checkpoint

import (
	"errors"
	"fmt"
	"sync"
	"time"

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
// It is re-entrant per vault root within one process. A nested join is counted,
// not taken again.
func Join(root string) (func(), error) { return JoinWithin(root, DefaultWriterWait) }

// JoinWithin is Join with an explicit wait budget. A non-positive wait means
// DefaultWriterWait.
func JoinWithin(root string, wait time.Duration) (func(), error) {
	noop := func() {}
	paths, err := resolve(root)
	if err != nil {
		return noop, err
	}
	refs := slotFor(paths.root)
	refs.mu.Lock()
	defer refs.mu.Unlock()
	if refs.depth > 0 {
		refs.depth++
		return refs.handle(), nil
	}
	release, err := acquireShared(paths, wait)
	if err != nil {
		return noop, err
	}
	refs.depth = 1
	refs.release = release
	return refs.handle(), nil
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
func acquireShared(paths vaultPaths, wait time.Duration) (func(), error) {
	if wait <= 0 {
		wait = DefaultWriterWait
	}
	deadline := time.Now().Add(wait)
	for {
		blocked, err := announced(paths)
		if err != nil {
			return nil, err
		}
		if !blocked {
			return takeShared(paths, remaining(deadline))
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w: it is copying the vault; retry once it finishes", ErrBarrierHeld)
		}
		time.Sleep(joinPollInterval)
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
func takeShared(paths vaultPaths, wait time.Duration) (func(), error) {
	release, err := vault.LockFile(paths.barrier, false, wait)
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
// The mutex is held across acquisition, so a second goroutine joining while the
// first is waiting out a checkpoint waits for it. That costs latency during a
// checkpoint, which is the only time the wait is non-trivial, and it buys the
// invariant the count depends on: the flock is taken exactly once per process
// per vault, so the process contends with other processes and never with itself.
type references struct {
	mu      sync.Mutex
	depth   int
	release func()
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
