package noteops

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/backup"
	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/restorestate"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

var restoreCrashHook func(point string)

const (
	restoreCrashIntent  = "intent"
	restoreCrashApplied = "content-applied"
	restoreCrashContent = "content"
	restoreCrashHistory = "history"
)

func restoreCrash(point string) {
	if restoreCrashHook != nil {
		restoreCrashHook(point)
	}
}

// SetRestoreCrashHookForTest installs a process-death seam for end-to-end
// restore recovery tests. Production code must never set it.
func SetRestoreCrashHookForTest(hook func(point string)) func() {
	previous := restoreCrashHook
	restoreCrashHook = hook
	return func() { restoreCrashHook = previous }
}

// RestoreBackup extracts an archive over the vault under the same safety
// contract every other mutation runs under.
//
// It is the one mutation that cannot prove itself the ordinary way. A restore —
// and especially --wipe, which empties the vault before writing anything back —
// removes files that no journal entry ever described, because the entries
// describing them are themselves part of what is being replaced. Requiring
// journal proof here would mean the most destructive operation bn has is the one
// operation it can never snapshot.
//
// So this is the documented exception: an operator running `bn backup restore`
// is the authorization, and the resulting snapshot is taken through
// CommitRestore, which commits the working tree's own pending state rather than
// paths a manifest named. Everything else still applies — the location policy
// and the safety preflight both run before the archive is opened, so a blocked
// vault is refused while it still has its contents, and the snapshot failing
// afterward is reported as the partial success it is.
//
// It joins the checkpoint barrier for the whole operation — preflight,
// extraction, snapshot — and joins it first, ahead of every lock any of those
// take. A restore is the one mutation whose intermediate states are not merely
// inconsistent but empty: --wipe clears the vault before it writes anything
// back, so a checkpoint taken in the middle of one copies a vault with no
// notes in it and a content history that still describes them all.
//
// backup.Restore does not join on its own behalf. It is a leaf and its only
// other caller is a test with no history to protect; this is the production
// door, and it is the layer that already owns the operation's boundaries.
func RestoreBackup(vaultRoot, archivePath string, opts backup.RestoreOptions) (*backup.RestoreResult, error) {
	return RestoreBackupContext(context.Background(), vaultRoot, archivePath, opts)
}

// RestoreBackupContext is RestoreBackup under ctx, so an interrupt cancels the
// restore at its Git preflight and history commit rather than killing it. The
// content swap itself is not interruptible: once it starts it is finished or
// left resumable by the restore intent, never abandoned half-way.
func RestoreBackupContext(ctx context.Context, vaultRoot, archivePath string, opts backup.RestoreOptions) (result *backup.RestoreResult, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(vaultRoot)
	if err != nil {
		return nil, fmt.Errorf("backup restore: resolve vault: %w", err)
	}
	releaseCompanion, acquired, err := vault.TryLockPath(vaultsync.CompanionLockPath(resolvedRoot))
	if err != nil {
		return nil, fmt.Errorf("backup restore: lock synchronization companion: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("backup restore: stop the synchronization companion before restoring")
	}
	defer releaseCompanion()
	held, err := checkpoint.Hold(resolvedRoot, checkpoint.Options{Reason: "backup restore", AllowRestoreRecovery: true})
	if err != nil {
		return nil, classifyBarrierError(err)
	}
	// As in backup.Create, a release failure is reported only when nothing else
	// went wrong: a checkpoint record left behind is a liveness problem for the
	// next writer, and it must not hide the failure that ended the restore.
	defer func() {
		if releaseErr := held.Release(); releaseErr != nil && err == nil {
			err = fmt.Errorf("backup restore: release the checkpoint: %w", releaseErr)
		}
	}()
	return restoreBackupHeld(held.Context(ctx), held.Root(), archivePath, opts)
}

func restoreBackupHeld(ctx context.Context, root, archivePath string, opts backup.RestoreOptions) (*backup.RestoreResult, error) {
	intent, safety, resumed, err := prepareRestore(ctx, root, archivePath, opts)
	if err != nil {
		return nil, err
	}
	result, err := applyRestoreContent(root, intent, resumed)
	if err != nil {
		return nil, err
	}
	if safety != nil {
		if err := commitRestoreHistory(ctx, safety); err != nil {
			return nil, err
		}
	}
	restoreCrash(restoreCrashHistory)
	// Restoring an enrolled vault also restores its old acknowledgments, export
	// cursor, origin sequence, and peer identity. Quarantine those bytes before
	// the outer restore intent is cleared: a process death either leaves the
	// restore resumable or leaves synchronization visibly awaiting review, never
	// an apparently ordinary companion allowed to replay stale causal state.
	if _, err := vaultsync.PrepareRestoreReviewContext(ctx, root, restoreOperationID(intent), nil); err != nil {
		return nil, fmt.Errorf("backup restore: prepare synchronization review: %w", err)
	}
	if err := restorestate.Clear(root); err != nil {
		return nil, fmt.Errorf("backup restore: clear completed recovery record: %w", err)
	}
	return result, nil
}

func restoreOperationID(intent *restorestate.Intent) string {
	identity := intent.Source + "\x00" + intent.Started.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}

func prepareRestore(ctx context.Context, root, archivePath string, opts backup.RestoreOptions) (*restorestate.Intent, Safety, bool, error) {
	intent, err := restorestate.Load(root)
	if err != nil {
		return nil, nil, false, err
	}
	safety, err := SafetyForContext(ctx, root)
	if err != nil {
		return nil, nil, false, err
	}
	if intent == nil && safety != nil {
		if err := safety.Preflight(ctx); err != nil {
			return nil, nil, false, classifySafetyError("git safety preflight", err)
		}
	}
	if intent != nil {
		return intent, safety, true, nil
	}
	resolved, err := backup.ValidateRestore(root, archivePath, opts)
	if err != nil {
		return nil, nil, false, err
	}
	intent, err = restorestate.Begin(root, resolved, opts.Wipe)
	if err != nil {
		return nil, nil, false, err
	}
	restoreCrash(restoreCrashIntent)
	return intent, safety, false, nil
}

func applyRestoreContent(root string, intent *restorestate.Intent, resumed bool) (*backup.RestoreResult, error) {
	// backup.Restore runs the location guard itself, before it extracts, which is
	// where it belongs: it is the layer that knows the vault has not been touched
	// yet.
	opts := backup.RestoreOptions{Wipe: intent.Wipe}
	var result *backup.RestoreResult
	var err error
	if intent.Phase == restorestate.PhaseHistory {
		result, err = backup.InspectValidatedRestore(root, intent.Archive, opts)
	} else if resumed {
		result, err = backup.ResumeRestore(root, intent.Archive, opts)
	} else {
		result, err = backup.Restore(root, intent.Archive, opts)
	}
	if err != nil {
		return nil, err
	}
	result.ArchivePath = intent.Source
	// The restored tree, including archived directory modes, is durable here but
	// the outer intent still says content. A process-death test at this boundary
	// proves a retry can reopen restrictive ancestors and replay idempotently.
	restoreCrash(restoreCrashApplied)
	if err := restorestate.SetPhase(root, intent, restorestate.PhaseHistory); err != nil {
		return nil, err
	}
	restoreCrash(restoreCrashContent)
	return result, nil
}

func commitRestoreHistory(ctx context.Context, safety Safety) error {
	snapshot, err := safety.CommitRestore(ctx, "backup-restore")
	if err != nil {
		return &SnapshotError{
			Operation: "backup restore",
			Blocked:   vaultgitBlocks(err),
			Err:       err,
		}
	}
	// A restore is the operation a person reaches for when things have already
	// gone wrong, so it is the last one that may report covering content the
	// history does not hold. The engine refuses an incomplete restore snapshot
	// outright; confirming it here is what makes that a property of the operation
	// rather than of the engine's current implementation.
	if unrecorded := snapshot.Unrecorded(); len(unrecorded) > 0 {
		return &SnapshotError{
			Operation: "backup restore",
			Err:       fmt.Errorf("the snapshot left out %s", strings.Join(unrecorded, ", ")),
		}
	}
	return nil
}
