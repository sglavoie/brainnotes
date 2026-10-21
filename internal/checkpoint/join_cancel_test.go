package checkpoint

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// holdBarrierExclusive takes the barrier the way a draining checkpoint does,
// without an announcement, so a joining writer reaches the flock wait itself.
func holdBarrierExclusive(t *testing.T, root string) func() {
	t.Helper()
	paths, err := resolve(root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	release, err := vault.LockFile(paths.barrier, true, shortWait)
	if err != nil {
		t.Fatalf("lock barrier exclusively: %v", err)
	}
	return release
}

func waitAcquiring(t *testing.T, root string) {
	t.Helper()
	refs := slotFor(root)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		refs.mu.Lock()
		busy := refs.acquiring != nil
		refs.mu.Unlock()
		if busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no goroutine started acquiring the shared barrier")
}

type joinOutcome struct {
	release func()
	err     error
	took    time.Duration
}

func joinAsync(ctx context.Context, root string, wait time.Duration) <-chan joinOutcome {
	out := make(chan joinOutcome, 1)
	go func() {
		start := time.Now()
		_, release, err := JoinContextWithin(ctx, root, wait)
		out <- joinOutcome{release: release, err: err, took: time.Since(start)}
	}()
	return out
}

// A writer waiting for the barrier flock, and a second writer in the same
// process waiting behind that first acquisition, must both be able to walk away
// when their request is cancelled instead of sleeping out the whole budget.
func TestJoinWaitIsCancellable(t *testing.T) {
	root := newBarrierVault(t)
	releaseBarrier := holdBarrierExclusive(t, root)
	defer releaseBarrier()

	first := joinAsync(context.Background(), root, 10*time.Second)
	waitAcquiring(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	second := joinAsync(ctx, root, 10*time.Second)
	time.Sleep(20 * time.Millisecond)
	cancel()
	got := <-second
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("queued join error = %v, want context.Canceled", got.err)
	}
	if got.took > 2*time.Second {
		t.Fatalf("queued join took %s to notice cancellation", got.took)
	}

	releaseBarrier()
	admitted := <-first
	if admitted.err != nil {
		t.Fatalf("first join after barrier release: %v", admitted.err)
	}
	admitted.release()
}

func TestJoinFlockWaitIsCancellable(t *testing.T) {
	root := newBarrierVault(t)
	releaseBarrier := holdBarrierExclusive(t, root)
	defer releaseBarrier()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	got := <-joinAsync(ctx, root, 10*time.Second)
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("join error = %v, want context.DeadlineExceeded", got.err)
	}
	if got.took > 2*time.Second {
		t.Fatalf("join took %s to notice cancellation", got.took)
	}
	if Joined(root) {
		t.Fatal("a cancelled join left the process counted as joined")
	}

	releaseBarrier()
	release := mustJoin(t, root)
	release()
}
