// Package vaultrecovery coordinates the human-reviewed boundary after a
// complete checkpoint restore. It compares both replicas without changing
// either, then applies one explicit decision while synchronization is stopped.
package vaultrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"time"

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
