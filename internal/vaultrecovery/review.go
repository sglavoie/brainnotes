// Package vaultrecovery coordinates the human-reviewed boundary after a
// complete checkpoint restore. It compares both replicas without changing
// either, then applies one explicit decision while synchronization is stopped.
package vaultrecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

const (
	InventoryFormat   = "brainnotes-restore-inventory-v1"
	PlanFormat        = "brainnotes-restore-plan-v1"
	archiveName       = "restore-archive"
	completionName    = "completed.json"
	completionVersion = 1
)

type completion struct {
	Version      int    `json:"version"`
	PlanSHA256   string `json:"plan_sha256"`
	TargetEpoch  uint64 `json:"target_epoch"`
	DeviceID     string `json:"device_id"`
	PeerDeviceID string `json:"peer_device_id"`
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Inventory is a content and causal snapshot suitable for carrying to the
// disconnected peer. Digest covers every field except itself.
type Inventory struct {
	Format            string            `json:"format"`
	Digest            string            `json:"digest"`
	VaultID           string            `json:"vault_id"`
	DeviceID          string            `json:"device_id"`
	EffectiveDeviceID string            `json:"effective_device_id"`
	Label             string            `json:"label"`
	Epoch             uint64            `json:"epoch"`
	ExportSequence    uint64            `json:"export_sequence"`
	Applied           map[string]uint64 `json:"applied"`
	Restored          bool              `json:"restored"`
	Files             []File            `json:"files"`
}

type Difference struct {
	Path  string `json:"path"`
	Local string `json:"local"`
	Peer  string `json:"peer"`
}

// Plan is deliberately data rather than authority. Applying it re-reads the
// local inventory and refuses if anything changed after the comparison.
type Plan struct {
	Format      string       `json:"format"`
	SHA256      string       `json:"sha256"`
	CreatedAt   time.Time    `json:"created_at"`
	Local       Inventory    `json:"local"`
	Peer        Inventory    `json:"peer"`
	TargetEpoch uint64       `json:"target_epoch"`
	Differences []Difference `json:"differences"`
	Regressions []string     `json:"regressions"`
}

func ReadInventory(path string) (*Inventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vault recovery: read peer inventory: %w", err)
	}
	var inventory Inventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return nil, fmt.Errorf("vault recovery: decode peer inventory: %w", err)
	}
	if err := inventory.Validate(); err != nil {
		return nil, err
	}
	return &inventory, nil
}

func ReadPlan(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vault recovery: read review plan: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("vault recovery: decode review plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return &plan, nil
}

func BuildInventory(root string) (*Inventory, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	exportSequence, applied, err := loadCausalState(root)
	if err != nil {
		return nil, err
	}
	review, err := vaultsync.LoadRestoreReview(root)
	if err != nil {
		return nil, err
	}
	effective, restored, err := effectiveIdentity(local, review)
	if err != nil {
		return nil, err
	}
	files, err := portableFiles(root)
	if err != nil {
		return nil, err
	}
	inventory := &Inventory{
		Format:            InventoryFormat,
		VaultID:           local.VaultID,
		DeviceID:          local.DeviceID,
		EffectiveDeviceID: effective,
		Label:             local.Label,
		Epoch:             local.Epoch,
		ExportSequence:    exportSequence,
		Applied:           applied,
		Restored:          restored,
		Files:             files,
	}
	inventory.Digest, err = inventory.digest()
	return inventory, err
}

func loadCausalState(root string) (uint64, map[string]uint64, error) {
	exportState, err := vaultexport.LoadState(root)
	if err != nil {
		return 0, nil, err
	}
	importState, err := vaultimport.LoadState(root)
	if err != nil {
		return 0, nil, err
	}
	applied := map[string]uint64{}
	for _, record := range importState.Applied {
		if record.Sequence > applied[record.OriginDevice] {
			applied[record.OriginDevice] = record.Sequence
		}
	}
	exportSequence := exportState.Sequence
	if exportState.Pending != nil && exportState.Pending.Sequence > exportSequence {
		exportSequence = exportState.Pending.Sequence
	}
	return exportSequence, applied, nil
}

func effectiveIdentity(local *vaultsync.Enrollment, review *vaultsync.RestoreReview) (string, bool, error) {
	if review == nil {
		return local.DeviceID, false, nil
	}
	if review.VaultID != local.VaultID || review.PreviousDeviceID != local.DeviceID {
		return "", false, fmt.Errorf("vault recovery: restore review does not describe the current enrollment")
	}
	return review.ProposedDeviceID, review.Restored, nil
}

func (i *Inventory) Validate() error {
	if err := i.validateIdentity(); err != nil {
		return err
	}
	if err := i.validateFiles(); err != nil {
		return err
	}
	want, err := i.digest()
	if err != nil {
		return err
	}
	if i.Digest != want {
		return fmt.Errorf("vault recovery: inventory digest mismatch")
	}
	return nil
}

func (i *Inventory) validateIdentity() error {
	if i == nil || i.Format != InventoryFormat || i.VaultID == "" || i.DeviceID == "" || i.EffectiveDeviceID == "" || i.Epoch == 0 {
		return fmt.Errorf("vault recovery: malformed inventory identity")
	}
	if i.Restored && i.DeviceID == i.EffectiveDeviceID {
		return fmt.Errorf("vault recovery: restored inventory reuses its device identity")
	}
	return nil
}

func (i *Inventory) validateFiles() error {
	seen := map[string]struct{}{}
	for _, file := range i.Files {
		if !vaultsync.Portable(file.Path) || file.Size < 0 || !validSHA256(file.SHA256) {
			return fmt.Errorf("vault recovery: malformed inventory file %q", file.Path)
		}
		if _, duplicate := seen[vaultsync.CollisionKey(file.Path)]; duplicate {
			return fmt.Errorf("vault recovery: colliding inventory path %q", file.Path)
		}
		seen[vaultsync.CollisionKey(file.Path)] = struct{}{}
	}
	return nil
}

func (i *Inventory) digest() (string, error) {
	copy := *i
	copy.Digest = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func BuildPlan(root string, peer *Inventory, now func() time.Time) (*Plan, error) {
	if err := peer.Validate(); err != nil {
		return nil, err
	}
	local, err := BuildInventory(root)
	if err != nil {
		return nil, err
	}
	if local.VaultID != peer.VaultID {
		return nil, fmt.Errorf("vault recovery: inventories name different vaults")
	}
	if local.EffectiveDeviceID == peer.EffectiveDeviceID {
		return nil, fmt.Errorf("vault recovery: inventories resolve to the same device identity")
	}
	if !local.Restored && !peer.Restored {
		return nil, fmt.Errorf("vault recovery: neither inventory is awaiting restore review")
	}
	at := time.Now().UTC()
	if now != nil {
		at = now().UTC()
	}
	target := local.Epoch
	if peer.Epoch > target {
		target = peer.Epoch
	}
	plan := &Plan{
		Format:      PlanFormat,
		CreatedAt:   at,
		Local:       *local,
		Peer:        *peer,
		TargetEpoch: target + 1,
		Differences: compareFiles(local.Files, peer.Files),
		Regressions: compareSequences(local, peer),
	}
	plan.SHA256, err = plan.digest()
	return plan, err
}

func (p *Plan) Validate() error {
	if err := p.validateComparison(); err != nil {
		return err
	}
	want, err := p.digest()
	if err != nil {
		return err
	}
	if p.SHA256 != want {
		return fmt.Errorf("vault recovery: review plan digest mismatch")
	}
	return nil
}

func (p *Plan) validateComparison() error {
	if p == nil || p.Format != PlanFormat || p.CreatedAt.IsZero() || p.TargetEpoch == 0 {
		return fmt.Errorf("vault recovery: malformed review plan")
	}
	if err := p.Local.Validate(); err != nil {
		return err
	}
	if err := p.Peer.Validate(); err != nil {
		return err
	}
	return p.validateDerivedComparison()
}

func (p *Plan) validateDerivedComparison() error {
	if p.Local.VaultID != p.Peer.VaultID || p.Local.EffectiveDeviceID == p.Peer.EffectiveDeviceID {
		return fmt.Errorf("vault recovery: review plan identities disagree")
	}
	wantEpoch := max(p.Local.Epoch, p.Peer.Epoch) + 1
	if p.TargetEpoch != wantEpoch {
		return fmt.Errorf("vault recovery: review plan target epoch is not the next shared generation")
	}
	if !reflect.DeepEqual(p.Differences, compareFiles(p.Local.Files, p.Peer.Files)) {
		return fmt.Errorf("vault recovery: review plan file comparison was altered")
	}
	if !reflect.DeepEqual(p.Regressions, compareSequences(&p.Local, &p.Peer)) {
		return fmt.Errorf("vault recovery: review plan sequence comparison was altered")
	}
	return nil
}

func (p *Plan) digest() (string, error) {
	copy := *p
	copy.SHA256 = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func ApplyPlan(root string, plan *Plan, now func() time.Time) error {
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
	defer held.Release()
	ctx := held.Context(context.Background())
	done, err := completedApply(root, plan)
	if err != nil {
		return err
	}
	if done {
		return nil
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
	return vaultsync.UpdateRestoreReviewContext(ctx, root, func(record *vaultsync.RestoreReview) error {
		record.Phase = vaultsync.ReviewEnrolled
		return nil
	})
}

func resetCausalState(ctx context.Context, root string, review *vaultsync.RestoreReview) (*vaultsync.RestoreReview, error) {
	if err := vaultexport.RebaselineAfterReviewContext(ctx, root, review.PreservedSequence); err != nil {
		return nil, err
	}
	if err := vaultimport.ResetAfterReviewContext(ctx, root); err != nil {
		return nil, err
	}
	return vaultsync.UpdateRestoreReviewContext(ctx, root, func(record *vaultsync.RestoreReview) error {
		record.Phase = vaultsync.ReviewReset
		return nil
	})
}

func compareSequences(local, peer *Inventory) []string {
	var regressions []string
	if seen := peer.Applied[local.DeviceID]; seen > local.ExportSequence {
		regressions = append(regressions, fmt.Sprintf("local origin %s restored to sequence %d after peer observed %d", local.DeviceID, local.ExportSequence, seen))
	}
	if seen := local.Applied[peer.DeviceID]; seen > peer.ExportSequence {
		regressions = append(regressions, fmt.Sprintf("peer origin %s is at sequence %d after local observed %d", peer.DeviceID, peer.ExportSequence, seen))
	}
	return regressions
}

func compareFiles(local, peer []File) []Difference {
	left, right := filesByPath(local), filesByPath(peer)
	paths := unionPaths(left, right)
	var differences []Difference
	for _, path := range paths {
		if difference, found := compareFile(path, left, right); found {
			differences = append(differences, difference)
		}
	}
	return differences
}

func filesByPath(files []File) map[string]File {
	indexed := make(map[string]File, len(files))
	for _, file := range files {
		indexed[file.Path] = file
	}
	return indexed
}

func unionPaths(left, right map[string]File) []string {
	paths := make([]string, 0, len(left)+len(right))
	seen := map[string]struct{}{}
	for path := range left {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range right {
		if _, ok := seen[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func compareFile(path string, left, right map[string]File) (Difference, bool) {
	local, localOK := left[path]
	peer, peerOK := right[path]
	switch {
	case !localOK:
		return Difference{Path: path, Local: "missing", Peer: peer.SHA256}, true
	case !peerOK:
		return Difference{Path: path, Local: local.SHA256, Peer: "missing"}, true
	case local.SHA256 != peer.SHA256 || local.Size != peer.Size:
		return Difference{Path: path, Local: local.SHA256, Peer: peer.SHA256}, true
	default:
		return Difference{}, false
	}
}

func portableFiles(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, inventoryWalker(root, &files))
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func inventoryWalker(root string, files *[]File) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return inventoryEntry(path, filepath.ToSlash(rel), entry, files)
	}
}

func inventoryEntry(path, rel string, entry fs.DirEntry, files *[]File) error {
	class := vaultsync.Classify(rel)
	if entry.IsDir() {
		if class != vaultsync.ClassPortable {
			return filepath.SkipDir
		}
		return nil
	}
	if class != vaultsync.ClassPortable {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("vault recovery: portable path %q is not a regular file", rel)
	}
	digest, err := hashFile(path)
	if err != nil {
		return err
	}
	*files = append(*files, File{Path: rel, Size: info.Size(), SHA256: digest})
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validSHA256(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
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
	}
	return nil
}

func archiveOne(source, base string) error {
	destination := filepath.Join(base, filepath.Base(source))
	_, sourceErr := os.Lstat(source)
	_, destinationErr := os.Lstat(destination)
	if errors.Is(sourceErr, fs.ErrNotExist) {
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
	if destinationErr == nil {
		return fmt.Errorf("vault recovery: both active and archived causal state exist for %s", filepath.Base(source))
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
