package vaultsyncd

import (
	"errors"
	"fmt"
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// discard removes one artifact from this device's outgoing channel, under the
// storage-location guard every write in this vault runs first.
//
// The guard is what makes the path checkable at all: it is the same preflight
// internal/vaultsync ran before publishing here, and running it again on the way
// out means a channel that has since moved onto storage this vault refuses to
// mutate is refused rather than half-emptied.
func discard(root, path string) error {
	if err := vault.GuardMutation(root, path); err != nil {
		return err
	}
	//bn:direct-write removes an artifact this device published into its own outgoing channel, after a verified receipt proved the peer no longer needs it, under the location guard run immediately above
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("vaultsyncd: retire %q: %w", path, err)
	}
	return nil
}

// skippable reports the refusals that mean "there is nothing for this worker to
// do right now", as distinct from "this worker failed".
//
// The distinction decides whether a failure streak grows. A vault that is not
// enrolled, has no content history, has no commits yet, or is being drained by a
// `bn sync` command the operator is running by hand is a vault this companion
// should leave alone and look at again next tick — recording those as failures
// would put an idle vault into an ever-lengthening backoff and bury the one
// error that does matter under a streak of ones that do not.
func skippable(err error) bool {
	return vacant(err) || errors.Is(err, vaultimport.ErrImportBusy)
}

// vacant reports the subset of skippable refusals that mean the worker looked
// and there was nothing there, as distinct from one that could not look.
//
// Only these clear a failure streak — see [Record.declined], which a skip
// decided before the worker runs reaches for the same reason. The two are the
// same fact arriving by different doors: a vault with no enrollment, no content
// history, or no commit yet has nothing for this worker to have failed at, so
// carrying StateFailing and `bn-vault-sync -check`'s exit 1 on the evidence of a
// failure nothing is retrying would be a false alarm nobody can clear.
//
// ErrImportBusy is deliberately not here. A `bn sync` holding the import guard
// says nothing about whether the last failure would happen again — the worker
// never got as far as finding out — and clearing the streak on it would also
// drop the backoff, so an import that fails and then meets a busy vault every
// pass would retry at the tick interval forever instead of backing off.
func vacant(err error) bool {
	switch {
	case errors.Is(err, vaultsync.ErrNotEnrolled):
		return true
	case errors.Is(err, vaultgit.ErrUnmanaged), errors.Is(err, vaultgit.ErrUnbornBranch):
		return true
	}
	return false
}

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	return agent.SanitizedDiagnostic(err)
}
