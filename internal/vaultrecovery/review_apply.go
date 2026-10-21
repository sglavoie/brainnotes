package vaultrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultimport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

func ApplyPlan(root string, plan *Plan, now func() time.Time) (err error) {
	if err := plan.Validate(); err != nil {
		return err
	}
	if len(plan.Differences) > 0 {
		return fmt.Errorf("vault recovery: inventories still differ on %d path(s); reconcile them offline and build a new review plan", len(plan.Differences))
	}
	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "restore re-enrollment review"})
	if err != nil {
		return fmt.Errorf("vault recovery: hold checkpoint: %w", err)
	}
	// As in backup.Create, a release failure is reported only when nothing else
	// went wrong, so it never hides the failure that ended the apply.
	defer func() {
		if releaseErr := held.Release(); releaseErr != nil && err == nil {
			err = fmt.Errorf("vault recovery: release checkpoint: %w", releaseErr)
		}
	}()
	return applyHeld(held.Context(context.Background()), root, plan, now)
}

// applyHeld is the acceptance itself, inside the checkpoint ApplyPlan holds.
func applyHeld(ctx context.Context, root string, plan *Plan, now func() time.Time) error {
	done, err := completedApply(root, plan)
	if err != nil || done {
		return err
	}
	// Before prepareApply, so a refused survivor is not left quarantined by a
	// marker it created for a decision it could not take, and on every resume,
	// because the record is archived only once the decision is applying.
	if err := requireResolvedImports(root); err != nil {
		return err
	}
	review, err := prepareApply(ctx, root, plan, now)
	if err != nil {
		return err
	}
	if err := validateReviewedFiles(root, plan.Local.Files); err != nil {
		return err
	}
	return advanceApply(ctx, root, plan, review, now)
}

func advanceApply(ctx context.Context, root string, plan *Plan, review *vaultsync.RestoreReview, now func() time.Time) error {
	var err error
	if review.Phase == vaultsync.ReviewApplying {
		review, err = applyEnrollment(ctx, root, plan, review, now)
		if err != nil {
			return err
		}
	}
	if review.Phase == vaultsync.ReviewEnrolled {
		review, err = resetCausalState(ctx, root, review)
		if err != nil {
			return err
		}
	}
	if review.Phase != vaultsync.ReviewReset {
		return fmt.Errorf("vault recovery: unsupported apply phase %q", review.Phase)
	}
	if err := publishCompletion(root, plan, review); err != nil {
		return err
	}
	applyCrash(crashCompleted)
	return vaultsync.ClearRestoreReviewContext(ctx, root)
}

func completedApply(root string, plan *Plan) (bool, error) {
	review, err := vaultsync.LoadRestoreReview(root)
	if err != nil || review != nil {
		return false, err
	}
	data, err := os.ReadFile(completionPath(root, plan.SHA256))
	if errors.Is(err, fs.ErrNotExist) {
		return rejectSupersededPlan(root, plan)
	}
	if err != nil {
		return false, fmt.Errorf("vault recovery: read completion: %w", err)
	}
	record, err := decodeCompletion(data, plan)
	if err != nil {
		return false, err
	}
	return completionMatchesEnrollment(root, record)
}

func decodeCompletion(data []byte, plan *Plan) (*completion, error) {
	var record completion
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("vault recovery: decode completion: %w", err)
	}
	want := completion{Version: completionVersion, PlanSHA256: plan.SHA256, TargetEpoch: plan.TargetEpoch,
		DeviceID: plan.Local.EffectiveDeviceID, PeerDeviceID: plan.Peer.EffectiveDeviceID}
	if record != want {
		return nil, fmt.Errorf("vault recovery: completion record does not match the review plan")
	}
	return &record, nil
}

func completionMatchesEnrollment(root string, record *completion) (bool, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return false, err
	}
	peer, enrolled := local.Peer(record.PeerDeviceID)
	if local.DeviceID != record.DeviceID || local.Epoch != record.TargetEpoch || !enrolled || peer.JoinedEpoch != record.TargetEpoch {
		return false, fmt.Errorf("vault recovery: completed review no longer matches enrollment")
	}
	return true, nil
}

func rejectSupersededPlan(root string, plan *Plan) (bool, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return false, err
	}
	if local.Epoch >= plan.TargetEpoch {
		return false, fmt.Errorf("vault recovery: review plan target epoch has already been reached or superseded")
	}
	return false, nil
}

func publishCompletion(root string, plan *Plan, review *vaultsync.RestoreReview) error {
	record := completion{Version: completionVersion, PlanSHA256: plan.SHA256, TargetEpoch: plan.TargetEpoch,
		DeviceID: review.ProposedDeviceID, PeerDeviceID: plan.Peer.EffectiveDeviceID}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("vault recovery: encode completion: %w", err)
	}
	data = append(data, '\n')
	path := completionPath(root, plan.SHA256)
	if err := vault.WriteStateFile(path, data, 0o600); err != nil {
		return fmt.Errorf("vault recovery: write completion: %w", err)
	}
	return nil
}

func archiveDir(root, planSHA string) string {
	return filepath.Join(root, filepath.FromSlash(vaultsync.StateDir()), archiveName, planSHA)
}

func completionPath(root, planSHA string) string {
	return filepath.Join(archiveDir(root, planSHA), completionName)
}

func validateReviewedFiles(root string, reviewed []File) error {
	current, err := portableFiles(root)
	if err != nil {
		return err
	}
	if differences := compareFiles(current, reviewed); len(differences) > 0 {
		return fmt.Errorf("vault recovery: portable content changed after review; rebuild both inventories")
	}
	return nil
}

func prepareApply(ctx context.Context, root string, plan *Plan, now func() time.Time) (*vaultsync.RestoreReview, error) {
	review, err := vaultsync.LoadRestoreReview(root)
	if err != nil {
		return nil, err
	}
	if review == nil {
		review, err = vaultsync.PreparePeerReviewContext(ctx, root, now)
		if err != nil {
			return nil, err
		}
	}
	if review.PlanSHA256 == "" {
		review, err = recordDecision(ctx, root, plan, review)
		if err != nil {
			return nil, err
		}
	}
	if !reviewMatchesPlan(review, plan) {
		return nil, fmt.Errorf("vault recovery: a different review plan is already being applied")
	}
	return review, nil
}

func recordDecision(ctx context.Context, root string, plan *Plan, review *vaultsync.RestoreReview) (*vaultsync.RestoreReview, error) {
	current, err := BuildInventory(root)
	if err != nil {
		return nil, err
	}
	if current.Digest != plan.Local.Digest {
		return nil, fmt.Errorf("vault recovery: local inventory changed after review")
	}
	if review.ProposedDeviceID != plan.Local.EffectiveDeviceID || review.VaultID != plan.Local.VaultID {
		return nil, fmt.Errorf("vault recovery: review marker does not match the plan")
	}
	return vaultsync.UpdateRestoreReviewContext(ctx, root, func(record *vaultsync.RestoreReview) error {
		record.PlanSHA256 = plan.SHA256
		record.TargetEpoch = plan.TargetEpoch
		record.PeerDeviceID = plan.Peer.EffectiveDeviceID
		if record.ProposedDeviceID == record.PreviousDeviceID {
			record.PreservedSequence = plan.Local.ExportSequence
		}
		record.Phase = vaultsync.ReviewApplying
		return nil
	})
}

func reviewMatchesPlan(review *vaultsync.RestoreReview, plan *Plan) bool {
	return review.PlanSHA256 == plan.SHA256 && review.TargetEpoch == plan.TargetEpoch && review.PeerDeviceID == plan.Peer.EffectiveDeviceID
}

func applyEnrollment(ctx context.Context, root string, plan *Plan, review *vaultsync.RestoreReview, now func() time.Time) (*vaultsync.RestoreReview, error) {
	if err := archiveCausalState(root, plan.SHA256); err != nil {
		return nil, err
	}
	peer := vaultsync.Peer{DeviceID: plan.Peer.EffectiveDeviceID, Label: plan.Peer.Label}
	if _, err := vaultsync.ReplaceEnrollmentForReviewContext(ctx, root, review.ProposedDeviceID, peer, plan.TargetEpoch, now); err != nil {
		return nil, err
	}
	applyCrash(crashEnrolled)
	return vaultsync.UpdateRestoreReviewContext(ctx, root, func(record *vaultsync.RestoreReview) error {
		record.Phase = vaultsync.ReviewEnrolled
		return nil
	})
}

func resetCausalState(ctx context.Context, root string, review *vaultsync.RestoreReview) (*vaultsync.RestoreReview, error) {
	if err := vaultexport.RebaselineAfterReviewContext(ctx, root, review.PreservedSequence); err != nil {
		return nil, err
	}
	applyCrash(crashRebaselined)
	if err := vaultimport.ResetAfterReviewContext(ctx, root); err != nil {
		return nil, err
	}
	return vaultsync.UpdateRestoreReviewContext(ctx, root, func(record *vaultsync.RestoreReview) error {
		record.Phase = vaultsync.ReviewReset
		return nil
	})
}

func archiveCausalState(root, planSHA string) error {
	if len(planSHA) != 64 || strings.Trim(planSHA, "0123456789abcdef") != "" {
		return fmt.Errorf("vault recovery: invalid plan identity")
	}
	base := archiveDir(root, planSHA)
	if err := vault.EnsureDirDurable(base, 0o700); err != nil {
		return fmt.Errorf("vault recovery: create causal archive: %w", err)
	}
	sources := []string{
		vaultsync.OutgoingDir(root),
		vaultsync.IncomingDir(root),
		vaultexport.StatePath(root),
		vaultimport.StatePath(root),
		vaultsync.BackupCoveragePath(root),
	}
	for _, source := range sources {
		if err := archiveOne(source, base); err != nil {
			return err
		}
		applyCrash(crashArchivedPrefix + filepath.Base(source))
	}
	return nil
}

// archiveOne moves one source into the plan's archive, idempotently.
//
// A source present beside its archived copy is not the restored state: the
// rename is atomic, and nothing in this flow writes a source after archiving it.
// It is something written since — the transport delivering into a recreated
// incoming channel while an interrupted acceptance waited for its retry is the
// ordinary case, and a deployment backup publishing coverage is another. The
// archived copy is the one this plan reviewed and is kept; the newcomer is left
// where it is, because it is no part of the old causal state the archive exists
// to quarantine — packages from the old epoch fail by stale epoch or unknown
// origin, and one from the reviewed epoch is exactly what the new enrollment
// should read. Refusing here instead made the acceptance unfinishable: no retry
// could ever find the source gone.
func archiveOne(source, base string) error {
	destination := filepath.Join(base, filepath.Base(source))
	_, sourceErr := os.Lstat(source)
	_, destinationErr := os.Lstat(destination)
	if errors.Is(sourceErr, fs.ErrNotExist) || (sourceErr == nil && destinationErr == nil) {
		if destinationErr == nil {
			return errors.Join(vault.SyncDir(filepath.Dir(source)), vault.SyncDir(base))
		}
		if errors.Is(destinationErr, fs.ErrNotExist) {
			return nil
		}
		return destinationErr
	}
	if sourceErr != nil {
		return sourceErr
	}
	if !errors.Is(destinationErr, fs.ErrNotExist) {
		return destinationErr
	}
	//bn:direct-write moves only synchronization channels and ledgers into the plan-named quarantine while the exclusive checkpoint and companion lock are held
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("vault recovery: archive %s: %w", filepath.Base(source), err)
	}
	if err := vault.SyncDir(filepath.Dir(source)); err != nil {
		return err
	}
	return vault.SyncDir(base)
}
