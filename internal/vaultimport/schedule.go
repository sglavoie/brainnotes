package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// HoldReason names why a package in the channel was not applied on this pass.
type HoldReason string

const (
	// HoldConflict means this vault disagrees with the package's expectations.
	// The contested paths, and every version of each, are in Held.Conflict.
	HoldConflict HoldReason = "conflict"
	// HoldDependency means the package states a dependency on a position in
	// another device's stream that this vault has not applied.
	HoldDependency HoldReason = "dependency"
	// HoldPredecessor means one of its changes names a causal predecessor carried
	// by a package that is in the channel and has not been applied. This is what
	// holds the dependents of a held operation.
	HoldPredecessor HoldReason = "predecessor"
	// HoldOrder means it changes a path that an earlier, still-unapplied package
	// also changes. Applying it first would compare the later edit against a
	// baseline this vault has not reached.
	HoldOrder HoldReason = "order"
	// HoldUnreadable means the package is not all here yet — most often one the
	// transport is still copying in. That is a manifest that has not arrived, and
	// equally a manifest whose payloads have not: the scan reads only the manifest,
	// so the second kind is found by Accept rather than by the scan, and it is the
	// same answer either way. Wait; the next drain will pick it up. A package this
	// build would refuse even once it is all here is HoldRejected instead, whichever
	// of the two found it.
	HoldUnreadable HoldReason = "unreadable"
	// HoldRejected means this vault will not accept the package at all: it is in a
	// format this build does not know, its manifest contradicts itself, it names a
	// path that is not portable vault content, it is addressed to another vault, it
	// comes from a device that is not an enrolled peer, or it predates or
	// post-dates the current enrollment epoch. Unlike every other reason, waiting
	// does not help — the detail says which, and each one has its own remedy.
	HoldRejected HoldReason = "rejected"
	// HoldFailed means applying it failed for a reason that is neither a conflict
	// nor a refusal to accept it. The detail carries the error.
	HoldFailed HoldReason = "failed"
	// HoldNotAttempted means the pass ended before this package was decided, which
	// happens only when a refusal about the *vault* stopped the drain part-way.
	// It says nothing about the package: the detail names what stopped the pass.
	HoldNotAttempted HoldReason = "not-attempted"
)

// Held is one package the channel still holds, and why.
type Held struct {
	// PackageID is the package, empty when it could not be read.
	PackageID string
	// Dir is where it sits in the incoming channel.
	Dir string
	// OriginDevice and Sequence are its position in the producer's stream, zero
	// when it could not be read.
	OriginDevice string
	Sequence     uint64
	// Reason is why it was not applied.
	Reason HoldReason
	// Detail is the human-readable specifics: the dependency, the predecessor, or
	// the error.
	Detail string
	// Conflict is the durable hold, set only for HoldConflict, and nil-checked even
	// then. It carries every contested path with the remote branch, the local
	// branch, and the baseline.
	//
	// It is nil for the one conflict that has no record: a precondition the
	// transaction refused and a second inspection could not reproduce, because the
	// concurrent editor put the note back in between. That is a real refusal with
	// nothing left to show, and retain deliberately writes no record for it rather
	// than inventing one.
	Conflict *ConflictRecord
}

// Drained is what one pass over the incoming channel did.
type Drained struct {
	// Recovery is the resolution of any interrupted application, which runs once
	// before anything is applied.
	Recovery *Recovery
	// Applied are the packages the drain answered, in the order it answered them.
	//
	// It is not a count of changes: it carries redeliveries answered from the
	// ledger (Result.Duplicate) and applications where every path already held the
	// package's bytes (Result.Converged), both of which wrote nothing. A caller
	// reporting what arrived must filter on those two flags — a channel that was
	// re-copied reports every package in it here.
	Applied []*Result
	// Held are the packages still in the channel, sorted by origin and sequence.
	// A held package is not a failure: the transport will deliver its dependency,
	// or a person will resolve its conflict, and the next drain will pick it up.
	Held []Held
	// Cleanup is a housekeeping failure that left the vault correct, the same
	// judgement Result.Cleanup encodes: a retained-branch directory belonging to no
	// hold that could not be removed, or an unusable peer retirement record that
	// prevented pruning old applied-ledger entries. Refusing every import over
	// either condition would be the wrong trade, and saying nothing about it would
	// be the other wrong one.
	Cleanup error
	// Unfinished names the packages whose content landed and whose bookkeeping did
	// not, which is the one outcome that is invisible from everything else here.
	//
	// Apply returns the Result *with* the error in that case, and a drain records
	// the package as applied because it is. The error has nowhere else to go: the
	// package is not held, Cleanup is about housekeeping that left durable content
	// correct, and the consequence is neither of those. There are two shapes and
	// the message
	// distinguishes them, because they point at different things to look at — the
	// receipt could not be minted or published, so the peer never learns the
	// package arrived and redelivers forever; or the receipt is out and the import
	// record does not know it, so the redelivery is re-inspected instead of being
	// answered from the ledger. Both are recoverable on redelivery rather than
	// fatal; they are reported so a drain that looks clean cannot hide a stalled
	// synchronization loop.
	Unfinished error
}

// Drain applies every package in this vault's incoming channel that can be
// applied, in causal order, and reports what it held back.
//
// This is the door a synchronization command calls. Apply is the single-package
// primitive underneath it and answers only about the package it is handed;
// nothing that can be said about *ordering* can be said from there, because
// ordering is a fact about the set. Three things follow from that and are the
// whole reason this exists.
//
// A package is applied only once what it depends on has been. Three edges are
// honoured: a stated dependency on a position in some device's stream, a causal
// predecessor naming an operation another package in the channel carries, and a
// plain overlap on a path an earlier package also changes. Without them a
// transport that delivers out of order — which every transport does, since a
// channel is a directory and a directory has no order — would apply the second
// edit of a note against a vault that never saw the first, and report a conflict
// where there is none.
//
// A conflict holds its own operation and everything downstream of it, and
// nothing else. That is the half that is easy to get wrong in the safe
// direction: stopping the whole drain at the first conflict would be simple,
// correct about content, and would let one contested note stop a hundred
// unrelated ones from arriving for as long as nobody looked. Each package is
// decided on its own and a held one only holds what names it.
//
// The whole pass takes the import guard once. Calling Apply in a loop would take
// and release it per package, which is not merely slower — a second process
// could interleave, and the ordering decided at the top of the loop would be
// made against a vault that has moved since.
//
// It returns an error only for conditions that are about the *vault* rather than
// about a package: a lock another import holds, an unresolvable interrupted
// application, or import state that cannot be read. Everything a package can be
// wrong about is reported as a hold, because the other packages are still worth
// applying.
//
// A refusal raised part-way through a pass comes back *with* the report of what
// had already landed, which is Apply's own convention and is here for a sharper
// reason: those packages are applied, their receipts are published, and the
// caller is the only thing that can tell a person where. Returning nothing would
// make a partial drain indistinguishable from one that did nothing, and the
// difference is what the peer has already been told.
func Drain(root string, opts Options) (*Drained, error) {
	return DrainContext(context.Background(), root, opts)
}

// DrainContext is Drain that a caller can stop. ctx is consulted only at package
// boundaries — before the interrupted-application recovery, and before each
// package is attempted — and never while one is being applied: an application
// abandoned half-way would leave exactly the interrupted state the recovery
// pass exists to resolve, so once a package starts it runs to its end whatever
// ctx says. A stop therefore leaves every package either fully applied or
// untouched, and comes back like any other part-way refusal: with the report of
// what had landed and an error wrapping ctx's.
//
// ctx also bounds the wait to join the checkpoint barrier. The context nested
// writers and the content-history queries inside one package see is detached
// from its cancellation, for the reason above.
func DrainContext(ctx context.Context, root string, opts Options) (*Drained, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	joined, release, acquired, err := lockImportsContext(ctx, root)
	if err != nil {
		return nil, err
	}
	defer release()
	if !acquired {
		return nil, fmt.Errorf("%w: no package was applied", ErrImportBusy)
	}
	if err := drainStopped(ctx); err != nil {
		return nil, err
	}
	// Every durable step inside a package runs under this context, and a step
	// abandoned half-way is the one outcome a drain must never produce, so the
	// join marker is kept and the caller's cancellation is not.
	opts.checkpointContext = context.WithoutCancel(joined)
	recovery, err := recoverLocked(root, opts)
	if err != nil {
		return nil, err
	}
	pending, channel, err := scanChannel(root)
	if err != nil {
		return nil, err
	}
	run := &drain{ctx: ctx, root: root, opts: opts, pending: pending, channel: channel}
	if err := run.sweep(); err != nil {
		// Nothing has been applied yet, so there is no partial report to carry: the
		// sweep runs before the first package is looked at.
		return nil, err
	}
	if err := run.passes(); err != nil {
		return run.drained(recovery), err
	}
	return run.drained(recovery), nil
}

// candidate is one package in the channel as the scheduler sees it.
type candidate struct {
	dir       string
	manifest  *vaultsync.Manifest
	operation *vaultsync.Operation
	// unreadable is why the package could not be read, which for the common case
	// — a directory the transport is still filling — is vaultsync's
	// ErrIncompletePackage.
	unreadable error
	// done is set once the package has been applied, so later passes skip it.
	done bool
	// reason and detail are this pass's verdict, reported if the package is still
	// held when the passes stop. They are cleared at the top of every pass, so a
	// pass that ends early leaves no verdict rather than the previous pass's —
	// which would describe a wait that has since been satisfied.
	reason HoldReason
	detail string
	// blocker is the package in the channel this one is waiting on, when the rule
	// that held it named one. Cleared with the verdict it belongs to.
	blocker string
	// conflict is the durable hold, when the last verdict was one.
	conflict *ConflictRecord
}

func (c *candidate) packageID() string {
	if c.manifest == nil {
		return ""
	}
	return c.manifest.PackageID
}

// name is the identity the channel stores this package under, which scanChannel
// has already checked is one.
//
// It is what sweep asks, and the distinction from packageID is the whole reason
// it exists: packageID answers the empty string for a package whose manifest
// could not be read, and a package the transport is halfway through rewriting is
// exactly that. Taking it for a package that has left the channel would drop a
// live hold and remove the local branch, which is the one version of the
// contested content that exists nowhere else.
func (c *candidate) name() string { return filepath.Base(c.dir) }

// scanChannel reads every package in the incoming channel and sorts them into
// the order a producer made them.
//
// Sorted by origin and then by sequence, and never by anything the filesystem
// says. A directory has no order, modification times are the transport's rather
// than the producer's, and CreatedAt is a clock on another machine — the
// protocol documents that nothing orders on it. Origin and sequence are the only
// fields that mean what the ordering needs them to mean, and the package
// identity breaks ties so two passes over one channel agree.
// The second return says whether the channel directory exists, which is not the
// same fact as its being empty: an absent channel is what a vault that has never
// received a package looks like, and it confirms nothing about any package
// having left one.
func scanChannel(root string) ([]*candidate, bool, error) {
	dir := vaultsync.IncomingDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("vaultimport: list the incoming channel %q: %w", dir, err)
	}
	pending := make([]*candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !vaultsync.PackageName(entry.Name()) {
			continue
		}
		pending = append(pending, readCandidate(filepath.Join(dir, entry.Name())))
	}
	sort.Slice(pending, func(i, j int) bool { return before(pending[i], pending[j]) })
	return pending, true, nil
}

func readCandidate(dir string) *candidate {
	found := &candidate{dir: dir}
	manifest, err := vaultsync.ReadManifest(dir)
	if err != nil {
		found.unreadable = err
		return found
	}
	operation, err := singleOperation(manifest)
	if err != nil {
		found.unreadable = err
		return found
	}
	found.manifest, found.operation = manifest, operation
	return found
}

// before is the total order the channel is drained in.
func before(a, b *candidate) bool {
	// An unreadable package sorts last and by name. It has no position to sort
	// by, and putting it first would let a directory that is still being copied
	// in decide where everything else went.
	if (a.manifest == nil) != (b.manifest == nil) {
		return b.manifest == nil
	}
	if a.manifest == nil {
		return a.dir < b.dir
	}
	if a.manifest.OriginDevice != b.manifest.OriginDevice {
		return a.manifest.OriginDevice < b.manifest.OriginDevice
	}
	if a.manifest.Sequence != b.manifest.Sequence {
		return a.manifest.Sequence < b.manifest.Sequence
	}
	return a.manifest.PackageID < b.manifest.PackageID
}

// drain is one pass over the channel.
type drain struct {
	// ctx is the caller's stop signal, consulted between packages only; nil
	// never stops. See DrainContext.
	ctx  context.Context
	root string
	opts Options
	// channel says the incoming directory exists, which is what makes an absent
	// package evidence that it left rather than evidence of nothing.
	channel bool
	pending []*candidate
	applied []*Result
	cleanup error
	// unfinished collects the packages that landed and were not fully booked in.
	// See Drained.Unfinished.
	unfinished error
}

// drained is the report of this pass, built in one place so the refusal path and
// the ordinary one cannot describe the same run differently.
func (d *drain) drained(recovery *Recovery) *Drained {
	return &Drained{
		Recovery:   recovery,
		Applied:    d.applied,
		Held:       d.holds(),
		Cleanup:    d.cleanup,
		Unfinished: d.unfinished,
	}
}

// load reads the import record, which every stage of a drain does against the
// disk rather than against a copy: an application writes it, and a decision made
// from a stale one would schedule against a vault that has moved.
func (d *drain) load() (*State, error) {
	local, err := vaultsync.Load(d.root)
	if err != nil {
		return nil, err
	}
	return loadState(d.root, local)
}
