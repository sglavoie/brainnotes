package vaultsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewRestoreOperationID identifies one explicit restore preparation. It is
// deliberately random rather than derived from checkpoint bytes: restoring the
// same checkpoint twice is two causal rollbacks and must rotate twice.
func NewRestoreOperationID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("vaultsync: mint restore operation identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

const (
	restoreReviewVersion = 1
	restoreReviewName    = "restore-review.json"
	restoreReviewPerm    = 0o600

	ReviewPending  = "pending"
	ReviewApplying = "applying"
	ReviewEnrolled = "enrolled"
	ReviewReset    = "reset"
)

// RestoreReview is the fail-closed boundary between restoring bytes and
// allowing those bytes to synchronize again. A restored device receives a new
// identity before it can publish, so an old sequence can never be reused under
// an identity a peer has already observed.
type RestoreReview struct {
	Version           int       `json:"version"`
	Restored          bool      `json:"restored"`
	RestoreOperation  string    `json:"restore_operation,omitempty"`
	PreparedAt        time.Time `json:"prepared_at"`
	VaultID           string    `json:"vault_id"`
	PreviousDeviceID  string    `json:"previous_device_id"`
	PreviousEpoch     uint64    `json:"previous_epoch"`
	ProposedDeviceID  string    `json:"proposed_device_id"`
	Phase             string    `json:"phase"`
	PlanSHA256        string    `json:"plan_sha256,omitempty"`
	TargetEpoch       uint64    `json:"target_epoch,omitempty"`
	PeerDeviceID      string    `json:"peer_device_id,omitempty"`
	PreservedSequence uint64    `json:"preserved_sequence,omitempty"`
}

func RestoreReviewPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(StateDir()), restoreReviewName)
}

func LoadRestoreReview(root string) (*RestoreReview, error) {
	path := RestoreReviewPath(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vaultsync: read restore review %q: %w", path, err)
	}
	var review RestoreReview
	if err := json.Unmarshal(data, &review); err != nil {
		return nil, fmt.Errorf("%w: parse restore review %q: %v", ErrCorruptState, path, err)
	}
	if err := review.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &review, nil
}

func (r *RestoreReview) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: no restore review", ErrCorruptState)
	}
	if r.Version != restoreReviewVersion {
		return fmt.Errorf("%w: restore review version %d", ErrUnsupportedVersion, r.Version)
	}
	if err := r.validateIdentity(); err != nil {
		return err
	}
	if err := r.validatePhase(); err != nil {
		return err
	}
	return r.validateRotation()
}

func (r *RestoreReview) validateIdentity() error {
	if err := checkIdentity(ErrCorruptState, "vault_id", vaultPrefix, r.VaultID); err != nil {
		return err
	}
	if err := checkIdentity(ErrCorruptState, "previous_device_id", devicePrefix, r.PreviousDeviceID); err != nil {
		return err
	}
	if err := checkIdentity(ErrCorruptState, "proposed_device_id", devicePrefix, r.ProposedDeviceID); err != nil {
		return err
	}
	if r.PreviousEpoch == 0 || r.PreparedAt.IsZero() {
		return fmt.Errorf("%w: restore review has no epoch or timestamp", ErrCorruptState)
	}
	if r.Restored && !validReviewDigest(r.RestoreOperation) {
		return fmt.Errorf("%w: restored review has no operation identity", ErrCorruptState)
	}
	if !r.Restored && r.RestoreOperation != "" {
		return fmt.Errorf("%w: surviving review carries a restore operation", ErrCorruptState)
	}
	return nil
}

func (r *RestoreReview) validatePhase() error {
	switch r.Phase {
	case ReviewPending:
		return r.validatePendingPhase()
	case ReviewApplying, ReviewEnrolled, ReviewReset:
		return r.validateDecisionPhase()
	default:
		return fmt.Errorf("%w: restore review phase %q", ErrCorruptState, r.Phase)
	}
}

func (r *RestoreReview) validatePendingPhase() error {
	if r.PlanSHA256 != "" || r.TargetEpoch != 0 || r.PeerDeviceID != "" || r.PreservedSequence != 0 {
		return fmt.Errorf("%w: pending restore review carries an applied decision", ErrCorruptState)
	}
	return nil
}

func (r *RestoreReview) validateDecisionPhase() error {
	if !validReviewDigest(r.PlanSHA256) || r.TargetEpoch <= r.PreviousEpoch {
		return fmt.Errorf("%w: restore review decision is incomplete", ErrCorruptState)
	}
	return checkIdentity(ErrCorruptState, "peer_device_id", devicePrefix, r.PeerDeviceID)
}

func (r *RestoreReview) validateRotation() error {
	if r.Restored && r.ProposedDeviceID == r.PreviousDeviceID {
		return fmt.Errorf("%w: restored device would reuse its identity", ErrCorruptState)
	}
	if r.Restored && r.PreservedSequence != 0 {
		return fmt.Errorf("%w: restored device would preserve its old sequence", ErrCorruptState)
	}
	if !r.Restored && r.ProposedDeviceID != r.PreviousDeviceID {
		return fmt.Errorf("%w: surviving device unexpectedly changes identity", ErrCorruptState)
	}
	return nil
}

// PrepareRestoreReviewContext quarantines an enrolled vault after restore. It
// is idempotent for one operationID so restore recovery can cross this boundary
// more than once, while a later restore of the same bytes still rotates again.
func PrepareRestoreReviewContext(ctx context.Context, root, operationID string, now func() time.Time) (*RestoreReview, error) {
	if !validReviewDigest(operationID) {
		return nil, fmt.Errorf("vaultsync: restore operation identity must be 64 lowercase hex characters")
	}
	local, err := Load(root)
	if errors.Is(err, ErrNotEnrolled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	deviceID, err := NewDeviceID()
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC()
	if now != nil {
		at = now().UTC()
	}
	review := &RestoreReview{
		Version:          restoreReviewVersion,
		Restored:         true,
		RestoreOperation: operationID,
		PreparedAt:       at,
		VaultID:          local.VaultID,
		PreviousDeviceID: local.DeviceID,
		PreviousEpoch:    local.Epoch,
		ProposedDeviceID: deviceID,
		Phase:            ReviewPending,
	}
	return replaceRestoreReview(ctx, root, review)
}

func replaceRestoreReview(ctx context.Context, root string, review *RestoreReview) (*RestoreReview, error) {
	_, release, err := checkpoint.JoinContext(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: %w", err)
	}
	defer release()
	path := RestoreReviewPath(root)
	unlock, err := vault.LockPaths(root, path)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: lock restore review: %w", err)
	}
	defer unlock()
	existing, err := LoadRestoreReview(root)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Restored && existing.RestoreOperation == review.RestoreOperation {
		return existing, nil
	}
	if existing != nil && existing.Phase != ReviewPending {
		return nil, fmt.Errorf("vaultsync: cannot replace a restore review already in phase %q", existing.Phase)
	}
	if err := writeRestoreReview(root, review); err != nil {
		return nil, err
	}
	return LoadRestoreReview(root)
}

// PreparePeerReviewContext quarantines the surviving peer while it applies the
// reviewed re-enrollment. Its identity is retained; only restored devices rotate.
func PreparePeerReviewContext(ctx context.Context, root string, now func() time.Time) (*RestoreReview, error) {
	if existing, err := LoadRestoreReview(root); err != nil || existing != nil {
		return existing, err
	}
	local, err := Load(root)
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC()
	if now != nil {
		at = now().UTC()
	}
	review := &RestoreReview{
		Version:          restoreReviewVersion,
		PreparedAt:       at,
		VaultID:          local.VaultID,
		PreviousDeviceID: local.DeviceID,
		PreviousEpoch:    local.Epoch,
		ProposedDeviceID: local.DeviceID,
		Phase:            ReviewPending,
	}
	return createRestoreReview(ctx, root, review)
}

func createRestoreReview(ctx context.Context, root string, review *RestoreReview) (*RestoreReview, error) {
	joined, release, err := checkpoint.JoinContext(ctx, root)
	_ = joined
	if err != nil {
		return nil, fmt.Errorf("vaultsync: %w", err)
	}
	defer release()
	path := RestoreReviewPath(root)
	unlock, err := vault.LockPaths(root, path)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: lock restore review: %w", err)
	}
	defer unlock()
	if existing, err := LoadRestoreReview(root); err != nil || existing != nil {
		return existing, err
	}
	if err := writeRestoreReview(root, review); err != nil {
		return nil, err
	}
	return LoadRestoreReview(root)
}

func UpdateRestoreReviewContext(ctx context.Context, root string, mutate func(*RestoreReview) error) (*RestoreReview, error) {
	_, release, err := checkpoint.JoinContext(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: %w", err)
	}
	defer release()
	path := RestoreReviewPath(root)
	unlock, err := vault.LockPaths(root, path)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: lock restore review: %w", err)
	}
	defer unlock()
	review, err := LoadRestoreReview(root)
	if err != nil {
		return nil, err
	}
	if review == nil {
		return nil, fmt.Errorf("vaultsync: no restore review is pending")
	}
	if err := mutate(review); err != nil {
		return nil, err
	}
	if err := writeRestoreReview(root, review); err != nil {
		return nil, err
	}
	return LoadRestoreReview(root)
}

func writeRestoreReview(root string, review *RestoreReview) error {
	if err := review.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return fmt.Errorf("vaultsync: encode restore review: %w", err)
	}
	data = append(data, '\n')
	path := RestoreReviewPath(root)
	if err := vault.GuardMutation(root, path); err != nil {
		return err
	}
	if err := vault.WriteStateFile(path, data, restoreReviewPerm); err != nil {
		return fmt.Errorf("vaultsync: write restore review: %w", err)
	}
	return nil
}

// ReplaceEnrollmentForReviewContext installs the exact enrollment a reviewed
// comparison selected. Repeating the same replacement is a no-op; any different
// replacement is refused while the review remains in progress.
func ReplaceEnrollmentForReviewContext(ctx context.Context, root, deviceID string, peer Peer, epoch uint64, now func() time.Time) (*Enrollment, error) {
	path := EnrollmentPath(root)
	_, release, err := checkpoint.JoinContext(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: %w", err)
	}
	defer release()
	unlock, err := vault.LockPaths(root, path)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: lock enrollment: %w", err)
	}
	defer unlock()
	current, err := Load(root)
	if err != nil {
		return nil, err
	}
	if err := validateReviewedEnrollment(current, deviceID, peer, epoch); err != nil {
		return nil, err
	}
	peer.JoinedEpoch = epoch
	review, err := LoadRestoreReview(root)
	if err != nil {
		return nil, err
	}
	if !reviewAllowsEnrollment(review, deviceID, peer.DeviceID, epoch) {
		return nil, fmt.Errorf("vaultsync: enrollment replacement is not the pending reviewed decision")
	}
	if enrollmentMatches(current, deviceID, peer, epoch) {
		return current, nil
	}
	at := optionTime(now)
	next := current.clone()
	if next.DeviceID != deviceID {
		next.DeviceID = deviceID
		next.EnrolledAt = at
	}
	next.Epoch = epoch
	next.EpochChangedAt = at
	next.Peers = []Peer{peer}
	return writeEnrollmentLocked(root, next)
}

func validateReviewedEnrollment(current *Enrollment, deviceID string, peer Peer, epoch uint64) error {
	if current.VaultID == "" || epoch == 0 {
		return fmt.Errorf("%w: reviewed enrollment is incomplete", ErrCorruptState)
	}
	if err := checkIdentity(ErrCorruptState, "device_id", devicePrefix, deviceID); err != nil {
		return err
	}
	if err := checkIdentity(ErrCorruptState, "peer.device_id", devicePrefix, peer.DeviceID); err != nil {
		return err
	}
	if deviceID == peer.DeviceID {
		return fmt.Errorf("%w: reviewed peers have the same identity", ErrCorruptState)
	}
	return nil
}

func enrollmentMatches(current *Enrollment, deviceID string, peer Peer, epoch uint64) bool {
	return current.DeviceID == deviceID && current.Epoch == epoch && len(current.Peers) == 1 && current.Peers[0] == peer
}

func reviewAllowsEnrollment(review *RestoreReview, deviceID, peerDeviceID string, epoch uint64) bool {
	return review != nil && review.Phase == ReviewApplying && review.ProposedDeviceID == deviceID && review.TargetEpoch == epoch && review.PeerDeviceID == peerDeviceID
}

func optionTime(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func ClearRestoreReviewContext(ctx context.Context, root string) error {
	_, release, err := checkpoint.JoinContext(ctx, root)
	if err != nil {
		return fmt.Errorf("vaultsync: %w", err)
	}
	defer release()
	path := RestoreReviewPath(root)
	unlock, err := vault.LockPaths(root, path)
	if err != nil {
		return fmt.Errorf("vaultsync: lock restore review: %w", err)
	}
	defer unlock()
	//bn:direct-write removes only the fail-closed review marker after enrollment and both causal ledgers have durably reached the reviewed generation
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("vaultsync: clear restore review: %w", err)
	}
	return vault.SyncDir(filepath.Dir(path))
}

func validReviewDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
