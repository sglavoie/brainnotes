package vaultsyncd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
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
	// refusing: content that landed whose bookkeeping did not, and files nothing
	// points at that could not be removed. They are carried as sanitized text
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
}

// runImport drains the incoming channel.
//
// The partial report is kept whatever the error is, because internal/vaultimport
// returns one on purpose: a drain that refused part-way has already applied
// packages, published their receipts, and told the peer so. Discarding the
// report would make a partial drain indistinguishable from one that did nothing.
func runImport(root string, opts Options) (*ImportOutcome, error) {
	drained, err := vaultimport.Drain(root, vaultimport.Options{
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
//   - A published package whose receipt has arrived and verifies. The peer has
//     the change durably, backed by a named recovery entry of its own, so this
//     copy has no reader left.
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
// and because every removal is of something already acknowledged. A concurrent
// remover is tolerated: os.RemoveAll reports a path that is already gone as
// success.
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
	outcome := &RetireOutcome{}
	reserved := reservedPackage(root)
	for _, pkg := range published {
		if err := outcome.consider(root, local, pkg, reserved); err != nil {
			return outcome, err
		}
	}
	return outcome, outcome.release(root)
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
func (o *RetireOutcome) consider(root string, local *vaultsync.Enrollment, pkg pkgEntry, reserved string) error {
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
	if err := discard(root, pkg.dir); err != nil {
		return err
	}
	o.Retired = append(o.Retired, pkg.name)
	return nil
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
