package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/brainnotes/internal/agent"
)

// PushTimeout bounds one remote push. The watcher runs its push in the same
// poll loop that takes snapshots, so a remote that accepts the connection and
// then never answers would otherwise stop external autosave for as long as the
// guard runs. Two minutes is generous for any real remote and still short next
// to the five-minute push interval; a push that runs out of it is recorded as
// an ordinary push failure and retried on the backoff schedule.
const PushTimeout = 2 * time.Minute

// PushResult describes one remote attempt.
type PushResult struct {
	// Attempted reports whether a push was actually run. It is false when no
	// remote is configured or the branch has no commits yet.
	Attempted bool
	// Pushed reports whether the remote accepted the branch.
	Pushed bool
	// Branch is the full ref that was pushed.
	Branch string
	// Remote is the remote name.
	Remote string
	// Retry carries the recorded backoff state after a failure.
	Retry PushState
	// RetryStateErr reports that the push succeeded but the previous failure
	// streak's record could not be removed. The push itself is not failed by it.
	RetryStateErr error
}

// Push publishes the current branch.
//
// It is deliberately not reachable from any mutation path. A push is a network
// round trip whose duration is bounded by nothing the user controls, and the
// content history is already durable on disk the moment the commit lands — so
// making a note save wait for a remote would buy no safety and cost every
// second the remote is slow. The two are separated by their own lock as well as
// by call site, so a push that hangs for its full [PushTimeout] cannot delay a
// snapshot another process takes at the same moment. The guard's own watcher
// pushes between polls, so for it the timeout is also what bounds how long one
// unresponsive remote can postpone the next external snapshot.
//
// A push failure is operational, never a safety failure: it records retry state
// and returns a KindPushFailed error, and it never writes the block marker. The
// local history is complete whether or not the remote ever answers.
func (m *Manager) Push(ctx context.Context) (PushResult, error) {
	const op = "push"
	release, err := m.acquire(pushLockName, op, ctx)
	if err != nil {
		return PushResult{}, err
	}
	defer release()

	result := PushResult{Remote: m.remote}
	configured, err := m.git.remoteConfigured(ctx, m.remote)
	if err != nil {
		return result, safetyErrorf(KindPushFailed, op, "the vault's remotes could not be listed: %s", agent.SanitizedDiagnostic(err))
	}
	if !configured {
		// A vault with no remote is a supported configuration: the local history
		// is the backup. Reporting this as a failure would produce a permanent
		// error state for a vault that is working exactly as intended.
		return result, nil
	}

	ref, err := m.git.symbolicHead(ctx)
	if err != nil || ref == "" {
		return result, safetyErrorf(KindPushFailed, op, "there is no branch to push")
	}
	result.Branch = ref
	if _, err := m.git.revParse(ctx, ref); err != nil {
		// An unborn branch has nothing to publish yet.
		return result, nil
	}

	result.Attempted = true
	pushCtx, cancel := context.WithTimeout(ctx, m.pushTimeout)
	defer cancel()
	if err := m.git.pushBranch(pushCtx, m.remote, ref); err != nil {
		detail := agent.SanitizedDiagnostic(err)
		state, saveErr := m.recordPushFailure(m.now(), detail)
		result.Retry = state
		return result, &SafetyError{
			Kind:   KindPushFailed,
			Op:     op,
			Reason: fmt.Sprintf("the remote %q did not accept branch %s; the local history is unaffected and the push will be retried", m.remote, ref),
			Err:    errors.Join(errors.New(detail), saveErr),
		}
	}
	result.Pushed = true
	if err := m.clearPushState(); err != nil {
		// The push landed, so this is not a push failure. The streak is marked
		// resolved in memory (see Manager.pushStale), so this process neither
		// waits out the old backoff nor continues the old streak on the next
		// failure; the error is reported so the stale file is not a silent one.
		result.RetryStateErr = err
	}
	return result, nil
}
