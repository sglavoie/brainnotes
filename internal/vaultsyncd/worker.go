package vaultsyncd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// ImportOutcome is what one drain of the incoming channel did.
type ImportOutcome struct {
	// Answered is how many packages the drain answered, including redeliveries it
	// answered from the ledger and packages whose changes this vault already had.
	Answered int `json:"answered"`
	// Changed is how many of those actually wrote something. It is the number
	// that means "this vault moved", and it is separate from Answered because a
	// re-copied channel reports every package in it as answered.
	Changed int `json:"changed"`
	// Held is how many packages are still in the channel, and Conflicts is how
	// many of those are waiting for a person.
	Held      int `json:"held"`
	Conflicts int `json:"conflicts"`
	// ConflictPaths are the contested vault-relative paths across those
	// conflicts, sorted and deduplicated. They are note paths and they do reach a
	// log line — unavoidably, since the whole point is to tell a person which
	// notes to look at.
	ConflictPaths []string `json:"conflict_paths,omitempty"`
	// Recovered reports that an interrupted application was resolved on the way
	// in.
	Recovered bool `json:"recovered,omitempty"`
	// Unfinished and Cleanup are the two failures a drain reports without
	// refusing: content that landed whose bookkeeping did not, and housekeeping
	// that could not finish safely, such as removing unreferenced files or pruning
	// from an unreadable peer retirement record. They are carried as sanitized text
	// because they must reach a log line even though the pass succeeded.
	Unfinished string `json:"unfinished,omitempty"`
	Cleanup    string `json:"cleanup,omitempty"`
}

// ExportOutcome is what one export pass published.
type ExportOutcome struct {
	Baseline bool   `json:"baseline,omitempty"`
	Packages int    `json:"packages"`
	Skipped  int    `json:"skipped"`
	Cursor   string `json:"cursor,omitempty"`
	// PendingCommits and PendingPaths are what the pass's own bound left behind,
	// which the next pass picks up.
	PendingCommits int `json:"pending_commits"`
	PendingPaths   int `json:"pending_paths"`
	// Burned are sequence numbers spent on a publication that was interrupted
	// before its manifest landed. They are reported for the reason
	// vaultexport.Result.BurnedSequences states: they leave a permanent gap in
	// this device's stream and nothing else records where it came from.
	Burned []uint64 `json:"burned,omitempty"`
}

// RetireOutcome is what one retirement sweep removed and why it left the rest.
type RetireOutcome struct {
	// Retired are the packages removed from the outgoing channel because the peer
	// acknowledged them.
	Retired []string `json:"retired,omitempty"`
	// Released are the packages whose receipts this device removed from its own
	// outgoing channel, having seen the peer withdraw the package they
	// acknowledge.
	Released []string `json:"released,omitempty"`
	// Awaiting is how many published packages have no verified receipt yet.
	Awaiting int `json:"awaiting"`
	// Incomplete is how many could not be read, which in a channel this device
	// owns means a publication interrupted before its manifest landed.
	Incomplete int `json:"incomplete"`
	// Rejected are receipts that arrived and did not verify. Their packages are
	// kept, which is the safe direction and also the silent one — so they are
	// named here.
	Rejected []ReceiptProblem `json:"rejected,omitempty"`
	// Uncovered packages have a verified peer receipt but are not named by the
	// latest verified private backup. Required packages are backup-covered and
	// acknowledged, but a still-published package depends on their position.
	// Both are retained.
	Uncovered int `json:"uncovered"`
	Required  int `json:"required"`
}

// runImport drains the incoming channel.
//
// The partial report is kept whatever the error is, because internal/vaultimport
// returns one on purpose: a drain that refused part-way has already applied
// packages, published their receipts, and told the peer so. Discarding the
// report would make a partial drain indistinguishable from one that did nothing.
//
// ctx is the pass's: a companion that is stopping ends the drain between
// packages, never inside one (see vaultimport.DrainContext).
func runImport(ctx context.Context, root string, opts Options) (*ImportOutcome, error) {
	drained, err := vaultimport.DrainContext(ctx, root, vaultimport.Options{
		Command: opts.command(),
		Now:     opts.Now,
	})
	if drained == nil {
		return nil, err
	}
	outcome := &ImportOutcome{
		Answered:  len(drained.Applied),
		Held:      len(drained.Held),
		Recovered: drained.Recovery != nil,
	}
	for _, result := range drained.Applied {
		if !result.Duplicate && !result.Converged {
			outcome.Changed++
		}
	}
	outcome.Conflicts, outcome.ConflictPaths = contested(drained.Held)
	outcome.Unfinished = sanitize(drained.Unfinished)
	outcome.Cleanup = sanitize(drained.Cleanup)
	return outcome, err
}

// contested counts the held packages waiting for a person and collects the paths
// they are waiting about.
//
// The record is nil-checked even under HoldConflict, because internal/vaultimport
// documents one conflict that has no record: a precondition the transaction
// refused and a second inspection could not reproduce. That is still a conflict
// and is still counted; it simply has no path to name.
func contested(held []vaultimport.Held) (int, []string) {
	count := 0
	seen := map[string]bool{}
	var paths []string
	for _, entry := range held {
		if entry.Reason != vaultimport.HoldConflict {
			continue
		}
		count++
		if entry.Conflict == nil {
			continue
		}
		for _, path := range entry.Conflict.Paths {
			if seen[path.Path] {
				continue
			}
			seen[path.Path] = true
			paths = append(paths, path.Path)
		}
	}
	sort.Strings(paths)
	return count, paths
}

// runExport publishes as much of the history as one bounded pass can.
//
// Like the drain, the result is kept alongside the error: every package in it is
// already published and every cursor advance in it is already durable, so
// dropping it would only make them unreportable.
func runExport(ctx context.Context, root string, opts Options) (*ExportOutcome, error) {
	result, err := vaultexport.Export(ctx, vaultexport.Options{
		Root:          root,
		GitExecutable: opts.GitExecutable,
		Now:           opts.Now,
		MaxPackages:   opts.MaxPackages,

		AuthorizeDeletions: opts.AuthorizeDeletions,
	})
	if result == nil {
		return nil, err
	}
	return &ExportOutcome{
		Baseline:       result.Baseline,
		Packages:       len(result.Packages),
		Skipped:        len(result.Skipped),
		Cursor:         result.Cursor,
		PendingCommits: result.PendingCommits,
		PendingPaths:   result.PendingPaths,
		Burned:         result.BurnedSequences,
	}, err
}

// runRetire removes what this device has published and no longer needs to keep.
//
// It is the one unit with no package behind it, and it belongs here for the
// reason the package documentation gives: internal/vaultexport publishes and
// never looks at the channel again, and internal/vaultimport applies packages a
// peer owns. Retirement is the *sender's* half of the handshake, and this is the
// only component that runs on the sender's schedule.
//
// Two removals, and every one of them is a file this device wrote into its own
// outgoing channel:
//
//   - A published package whose receipt has arrived and verifies, whose exact
//     manifest is covered by a verified private backup, and whose origin
//     position no still-published package depends on. The peer has the change
//     durably, the backup retains the sender's recovery copy, and the remaining
//     channel no longer needs this dependency.
//   - A receipt for a peer package that has left this vault's incoming channel.
//     The peer removes its copy only after reading that receipt, so its
//     disappearance is the acknowledgment of the acknowledgment. If it turns out
//     to be something else — a person tidying the channel by hand — the peer
//     redelivers and internal/vaultimport republishes the same receipt from its
//     ledger rather than minting a new one, so the mistake costs a round trip
//     and nothing more.
//
// Nothing here removes anything from the incoming channel. Those files belong to
// the peer, and a transport that mirrors directories turns a local deletion into
// a remote one.
//
// It takes no subsystem lock, which is what keeps the "never two locks at once"
// invariant trivially true for this unit. It is safe without one because the
// exporter creates package directories under fresh identities and never
// enumerates the channel, so there is no listing for a removal to invalidate,
// and because every removal is of something already acknowledged and backed up.
// A concurrent remover is tolerated: os.RemoveAll reports a path that is already
// gone as success.
func runRetire(root string) (*RetireOutcome, error) {
	barrier, err := checkpoint.Join(root)
	if err != nil {
		return nil, fmt.Errorf("vaultsyncd: retire: %w", err)
	}
	defer barrier()
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	published, err := listPackages(vaultsync.OutgoingDir(root))
	if err != nil {
		return nil, err
	}
	coverage, err := vaultsync.LoadBackupCoverage(root)
	if err != nil {
		return nil, err
	}
	required, completeGraph := requiredPackages(published, local.DeviceID)
	outcome := &RetireOutcome{}
	reserved := reservedPackage(root)
	for _, pkg := range published {
		if err := outcome.consider(root, local, pkg, reserved, coverage, !completeGraph || required[pkg.name]); err != nil {
			return outcome, err
		}
	}
	if err := publishRetirement(root, local); err != nil {
		return outcome, err
	}
	return outcome, outcome.release(root)
}

// publishRetirement advances the bounded sender-owned evidence a receiver uses
// to prune its applied-package ledger. The export sequence is the upper bound:
// every position at or below it has been consumed and will never be reused. Any
// package still published is named as an exception. An unreadable export record
// or package leaves the existing watermark alone, which retains receiver history
// in the safe direction.
func publishRetirement(root string, local *vaultsync.Enrollment) error {
	state, err := vaultexport.LoadState(root)
	if err != nil || state.Sequence == 0 {
		return nil
	}
	through := state.Sequence
	published, err := listPackages(vaultsync.OutgoingDir(root))
	if err != nil {
		return err
	}
	retained := make([]vaultsync.RetainedPackage, 0, len(published))
	for _, pkg := range published {
		manifest, err := vaultsync.ReadManifest(pkg.dir)
		if err != nil {
			return nil
		}
		if manifest.OriginDevice != local.DeviceID || manifest.Epoch != local.Epoch {
			continue
		}
		if manifest.Sequence <= through {
			retained = append(retained, vaultsync.RetainedPackage{
				PackageID: manifest.PackageID,
				Sequence:  manifest.Sequence,
			})
		}
	}
	_, err = vaultsync.PublishRetirement(vaultsync.OutgoingDir(root), &vaultsync.Retirement{
		Format:       vaultsync.Format,
		VaultID:      local.VaultID,
		OriginDevice: local.DeviceID,
		Epoch:        local.Epoch,
		Through:      through,
		Retained:     retained,
	})
	return err
}

// reservedPackage names the package internal/vaultexport is in the middle of
// publishing, or "" when it is not in the middle of one.
//
// Retirement must not remove it, however good the receipt is. vaultexport
// reserves a sequence and an identity *before* it publishes and resolves that
// reservation on its next pass by asking whether the directory is there: present
// means the publication landed and the cursor may advance, absent means nothing
// was created and the sequence is reused. A pass that published a whole package
// and died before committing its state leaves exactly that reservation — and the
// peer can apply and acknowledge inside that window, because the package is
// complete. Retiring it would make the absent-directory reading a lie: the
// exporter would re-export the same commit under a *fresh* identity reusing the
// same sequence, and the peer's ledger is keyed on package identity, so it would
// not recognize the redelivery and would apply it against preconditions
// describing a vault it has already moved past.
//
// An unreadable export record reports no reservation rather than refusing. The
// exporter is already halted on it and says so, `Reset` is the remedy and drops
// the reservation anyway, and making this worker fail on another's record would
// take the whole retirement sweep down with an export failure it cannot fix.
func reservedPackage(root string) string {
	state, err := vaultexport.LoadState(root)
	if err != nil || state.Pending == nil {
		return ""
	}
	return state.Pending.PackageID
}

// consider decides what one published package is and retires it if it may be.
//
// The reservation is checked before the manifest is read, so a reserved package
// whose manifest never landed counts as awaiting rather than incomplete. That is
// the safe direction — the count it belongs to is the one that keeps it — and it
// costs the sequence gap its explanation for exactly as long as the reservation
// lasts, which is until the next export resolves it.
func (o *RetireOutcome) consider(
	root string,
	local *vaultsync.Enrollment,
	pkg pkgEntry,
	reserved string,
	coverage *vaultsync.BackupCoverage,
	required bool,
) error {
	if pkg.name == reserved {
		o.Awaiting++
		return nil
	}
	manifest, err := vaultsync.ReadManifest(pkg.dir)
	if err != nil {
		o.Incomplete++
		return nil
	}
	switch acknowledged, err := verifyReceipt(root, manifest, local); {
	case err != nil:
		o.Rejected = append(o.Rejected, ReceiptProblem{PackageID: manifest.PackageID, Detail: sanitize(err)})
		o.Awaiting++
		return nil
	case !acknowledged:
		o.Awaiting++
		return nil
	}
	covered, err := coverage.Covers(pkg.dir, manifest)
	if err != nil {
		return err
	}
	if !covered {
		o.Uncovered++
		return nil
	}
	if required {
		o.Required++
		return nil
	}
	if err := discard(root, pkg.dir); err != nil {
		return err
	}
	o.Retired = append(o.Retired, pkg.name)
	return nil
}

// requiredPackages maps an origin position to its package identity, then marks
// every local package a still-published manifest depends on. The boolean says
// whether every manifest was readable. One unreadable directory makes the
// graph incomplete, so the caller retains every package for this sweep rather
// than guessing that an unknown manifest has no dependencies.
func requiredPackages(published []pkgEntry, localDevice string) (map[string]bool, bool) {
	bySequence := make(map[uint64][]string, len(published))
	manifests := make([]*vaultsync.Manifest, 0, len(published))
	complete := true
	for _, pkg := range published {
		manifest, err := vaultsync.ReadManifest(pkg.dir)
		if err != nil {
			complete = false
			continue
		}
		manifests = append(manifests, manifest)
		if manifest.OriginDevice == localDevice {
			bySequence[manifest.Sequence] = append(bySequence[manifest.Sequence], manifest.PackageID)
		}
	}
	required := make(map[string]bool)
	for _, manifest := range manifests {
		for _, dependency := range manifest.Requires {
			if dependency.OriginDevice != localDevice {
				continue
			}
			for _, packageID := range bySequence[dependency.Sequence] {
				if packageID != manifest.PackageID {
					required[packageID] = true
				}
			}
		}
	}
	return required, complete
}

// release drops receipts whose packages have left the incoming channel. See
// runRetire for why their disappearance is the signal.
//
// An unreadable incoming channel stops it rather than making it guess: every
// package would look gone, and every receipt would be released at once.
//
// A channel directory that is *missing* is that same condition wearing the
// clothes of an ordinary one. An empty channel legitimately releases every
// receipt — the peer withdrew the packages, which is what retirement on the
// other side looks like from here — and listPackages reports a missing directory
// as an empty listing, which is the right answer for every other caller and the
// wrong one for this decision. So it is asked separately, and a channel that is
// not there yet leaves the receipts alone.
func (o *RetireOutcome) release(root string) error {
	receipts, err := listReceipts(vaultsync.OutgoingDir(root))
	if err != nil || len(receipts) == 0 {
		return err
	}
	incoming := vaultsync.IncomingDir(root)
	if _, err := os.Stat(incoming); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultsyncd: read the incoming channel %q: %w", incoming, err)
	}
	delivered, err := listPackages(incoming)
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(delivered))
	for _, pkg := range delivered {
		present[pkg.name] = true
	}
	for _, receipt := range receipts {
		if present[receipt.packageID] {
			continue
		}
		if err := discard(root, receipt.path); err != nil {
			return err
		}
		o.Released = append(o.Released, receipt.packageID)
	}
	return nil
}
