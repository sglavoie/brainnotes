package noteops

import (
	"context"
	"strings"
	"sync"

	"github.com/sglavoie/brainnotes/internal/vaultgit"
)

// Safety is the Git safety engine as a mutating operation needs it: refuse
// before, record after.
//
// It is an interface only so a test can stand in for the engine. Production has
// exactly one implementation, resolved by SafetyFor.
type Safety interface {
	// Preflight refuses a mutation that must not start.
	Preflight(ctx context.Context) error
	// CommitJournalEntry records one finalized journal entry as a local snapshot.
	CommitJournalEntry(ctx context.Context, entryID, operation string) (vaultgit.CommitResult, error)
	// CommitPaths records exact paths that have no journal entry behind them.
	CommitPaths(ctx context.Context, operation string, paths []string) (vaultgit.CommitResult, error)
	// CommitRestore records a wholesale restore, the one operator-authorized
	// snapshot that may commit removals no journal entry explains.
	CommitRestore(ctx context.Context, operation string) (vaultgit.CommitResult, error)
}

// safetyResolver is how every mutation path finds its engine. It is a variable
// so a test can install a stub for a vault it did not `git init`; nothing in
// production replaces it.
var safetyResolver = func(ctx context.Context, vaultRoot string) (Safety, error) {
	manager, err := vaultgit.Shared(ctx, vaultRoot, vaultgit.Options{})
	if err != nil {
		return nil, err
	}
	return manager, nil
}

var safetyResolverMu sync.RWMutex

// SafetyFor resolves the Git safety engine for a vault.
//
// A nil Safety with a nil error is the normal answer for a vault that is not
// under content history — a fresh vault, a fixture, a copy — and every caller
// must read it as "there is nothing to snapshot", not as a failure. Anything
// else is a real refusal and is returned classified.
func SafetyFor(vaultRoot string) (Safety, error) {
	return SafetyForContext(context.Background(), vaultRoot)
}

// SafetyForContext resolves the engine without discarding an enclosing
// checkpoint admission used by recovery during Open.
func SafetyForContext(ctx context.Context, vaultRoot string) (Safety, error) {
	safetyResolverMu.RLock()
	resolve := safetyResolver
	safetyResolverMu.RUnlock()

	safety, err := resolve(ctx, vaultRoot)
	if err != nil {
		if vaultgit.IsUnmanaged(err) {
			return nil, nil
		}
		return nil, classifySafetyError("safety preflight", err)
	}
	return safety, nil
}

// SnapshotError reports a mutation that completed on disk and whose safety
// snapshot did not. It is the engine's type, aliased here because it is part of
// this package's contract with the CLI and the web layer: every mutation that
// goes through Finalize can end this way, and there is exactly one type for it
// so a caller cannot be shown two different sentences about the same situation.
type SnapshotError = vaultgit.SnapshotError

// IsSnapshotFailure reports whether err means the mutation landed but its
// snapshot did not. A caller must never present it as "nothing happened".
func IsSnapshotFailure(err error) bool { return vaultgit.IsSnapshotFailure(err) }

// classifySafetyError maps an engine refusal onto the stable operation codes the
// web layer already renders.
//
// A busy engine is a conflict because another process is mid-operation and
// retrying is the right response, which is exactly what 409 already means here.
// Everything else is an internal failure: a blocked vault, a repository state
// the engine will not write to, and a deletion nothing explains are all
// conditions a person has to go and look at, and none of them is caused by the
// request that hit them.
func classifySafetyError(context string, err error) error {
	if err == nil {
		return nil
	}
	if vaultgit.IsBusy(err) {
		return WrapError(CodeConflict, err.Error(), err)
	}
	return WrapError(CodeSafety, prefixContext(context, err), err)
}

// vaultgitBlocks reports whether an engine failure latched persistent blocked
// state, which is what decides whether a partial-success message ends with "and
// further mutations are refused".
func vaultgitBlocks(err error) bool { return vaultgit.Blocks(err) }

func prefixContext(context string, err error) string {
	if strings.TrimSpace(context) == "" {
		return err.Error()
	}
	return context + ": " + err.Error()
}
