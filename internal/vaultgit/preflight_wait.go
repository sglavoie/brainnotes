package vaultgit

import (
	"context"
	"time"
)

const preflightWaitBudget = 1500 * time.Millisecond

// acquirePreflight absorbs short background snapshots before a mutation starts.
// Only acquisition is retried: neither a preflight check nor any write is ever
// replayed. Snapshot and push acquisitions remain immediate, and the existing
// lock order is unchanged. The checkpoint is joined once; its refusal is never
// retried. Only the subsequent Git lock can use this wait budget.
func (m *Manager) acquirePreflight(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return m.acquireWithin(safetyLockName, "preflight", ctx, preflightWaitBudget)
}

func waitForPreflight(ctx context.Context, budget time.Duration, acquire func() (func(), error)) (func(), error) {
	wait, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var last error
	for {
		if err := wait.Err(); err != nil {
			if last != nil {
				return nil, last
			}
			return nil, err
		}
		release, err := acquire()
		if !IsBusy(err) {
			return release, err
		}
		last = err
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-wait.Done():
			timer.Stop()
			return nil, last
		case <-timer.C:
		}
	}
}
