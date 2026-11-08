package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultrecovery"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
	"github.com/sglavoie/brainnotes/internal/vaultsyncd"
)

func runInventory(root string, stdout, stderr io.Writer) int {
	inventory, err := vaultrecovery.BuildInventory(root)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if err := writeJSON(stdout, inventory); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return exitOK
}

func runPrepareRestore(ctx context.Context, root string, stdout, stderr io.Writer) int {
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(root))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if !acquired {
		fmt.Fprintln(stderr, "bn-vault-sync: stop the synchronization companion before preparing a restore review")
		return exitNeedsPerson
	}
	defer release()
	operationID, err := vaultsync.NewRestoreOperationID()
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	if _, err := vaultsync.PrepareRestoreReviewContext(ctx, root, operationID, nil); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	return runInventory(root, stdout, stderr)
}

func runReview(root, path string, stdout, stderr io.Writer) int {
	peer, err := vaultrecovery.ReadInventory(path)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	plan, err := vaultrecovery.BuildPlan(root, peer, nil)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	if err := writeJSON(stdout, plan); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return exitOK
}

func runAcceptReview(root, path string, stdout, stderr io.Writer) int {
	plan, err := vaultrecovery.ReadPlan(path)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(root))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if !acquired {
		fmt.Fprintln(stderr, "bn-vault-sync: stop the synchronization companion before accepting a restore review")
		return exitNeedsPerson
	}
	defer release()
	if err := vaultrecovery.ApplyPlan(root, plan, nil); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	result, err := vaultrecovery.BuildInventory(root)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return exitOK
}

// runRecoverImport runs import recovery alone, for a vault quarantined by a
// restore review: a companion pass runs no worker under the marker, and
// re-enrollment refuses while an interrupted import is pending. It holds the
// companion lock like every review mode, and exits 1 when work recovery cannot
// finish by itself — a held conflict — is still left.
func runRecoverImport(root string, stdout, stderr io.Writer) int {
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(root))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if !acquired {
		fmt.Fprintln(stderr, "bn-vault-sync: stop the synchronization companion before recovering an import")
		return exitNeedsPerson
	}
	defer release()
	report, err := vaultrecovery.RecoverImport(root)
	if report != nil {
		if writeErr := writeJSON(stdout, report); writeErr != nil {
			err = errors.Join(err, writeErr)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	if report.Remaining != "" {
		fmt.Fprintf(stderr, "bn-vault-sync: %s\n", report.Remaining)
		return exitNeedsPerson
	}
	return exitOK
}
