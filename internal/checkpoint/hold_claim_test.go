package checkpoint

import (
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// A writer's liveness probe try-locks the claim lock for an instant. A Hold that
// races it must wait the probe out rather than report another checkpoint.
func TestHoldWaitsOutALivenessProbeOnTheClaimLock(t *testing.T) {
	root := newBarrierVault(t)
	paths, err := resolve(root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	probe, acquired, err := vault.TryLockPath(paths.claim)
	if err != nil || !acquired {
		t.Fatalf("take the claim as a probe would: acquired=%v err=%v", acquired, err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		probe()
		close(released)
	}()

	held, err := Hold(root, Options{Reason: "races a probe", Drain: shortWait})
	<-released
	if err != nil {
		t.Fatalf("Hold racing a liveness probe = %v, want it to succeed", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
