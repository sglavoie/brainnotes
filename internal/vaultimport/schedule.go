package vaultimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
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
	// hold that could not be removed. Refusing every import over a directory that
	// is safe to delete would be the wrong trade, and saying nothing about it would
	// be the other wrong one.
	Cleanup error
	// Unfinished names the packages whose content landed and whose bookkeeping did
	// not, which is the one outcome that is invisible from everything else here.
	//
	// Apply returns the Result *with* the error in that case, and a drain records
	// the package as applied because it is. The error has nowhere else to go: the
	// package is not held, Cleanup is about files nothing points at, and the
	// consequence is neither of those. There are two shapes and the message
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
	release, acquired, err := lockImports(root)
	if err != nil {
		return nil, err
	}
	defer release()
	if !acquired {
		return nil, fmt.Errorf("%w: no package was applied", ErrImportBusy)
	}
	recovery, err := recoverLocked(root, opts)
	if err != nil {
		return nil, err
	}
	pending, channel, err := scanChannel(root)
	if err != nil {
		return nil, err
	}
	run := &drain{root: root, opts: opts, pending: pending, channel: channel}
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

// sweep drops holds for packages that are no longer in the channel, and removes
// every retained branch directory no hold names.
//
// Both halves are needed and they answer different failures. A package that was
// deleted from the channel while it was held leaves a record describing a
// conflict nobody can act on, since the remote branch it names is gone. And a
// hold that was dropped by a process that died before it could remove the
// retained bytes leaves a directory nothing points at. Neither is dangerous;
// both grow without bound, and the record is the thing a person reads.
//
// It does nothing at all when the channel directory is absent, because every
// package is then "not present" and one sweep would drop every hold in the vault
// — over a directory that says only that nothing has ever been delivered here.
func (d *drain) sweep() error {
	if !d.channel {
		return nil
	}
	state, err := d.load()
	if err != nil {
		return err
	}
	present := make(map[string]struct{}, len(d.pending))
	for _, found := range d.pending {
		present[found.name()] = struct{}{}
	}
	next := state.clone()
	for packageID := range state.Conflicts {
		if _, stillThere := present[packageID]; !stillThere {
			delete(next.Conflicts, packageID)
		}
	}
	if len(next.Conflicts) != len(state.Conflicts) {
		if err := saveState(d.root, next); err != nil {
			return err
		}
	}
	// A directory that could not be collected is not a reason to refuse every
	// import in the vault — see Drained.Cleanup.
	d.cleanup = errors.Join(d.cleanup, collectRetained(d.root, next.Conflicts))
	return nil
}

// passes applies what it can, repeatedly, until a pass changes nothing.
//
// The loop is what makes a chain arrive in one drain rather than in one drain
// per link: applying a package can satisfy the dependency another was held on,
// and re-deciding is cheap next to leaving the second one in the channel until
// somebody runs the command again. It stops the moment a pass applies nothing,
// which is the only termination condition that does not depend on the shape of
// the dependency graph — a cycle, which no honest producer can emit, stalls
// instead of spinning.
func (d *drain) passes() error {
	for {
		progress, err := d.pass()
		if err != nil {
			return err
		}
		if !progress {
			return nil
		}
	}
}

func (d *drain) pass() (bool, error) {
	state, err := d.load()
	if err != nil {
		return false, err
	}
	progress := false
	for _, found := range d.pending {
		if found.done {
			continue
		}
		found.reason, found.detail, found.blocker, found.conflict = "", "", "", nil
		if held := d.hold(found, state); held {
			continue
		}
		applied, err := d.attempt(found)
		if err != nil {
			return progress, err
		}
		if applied {
			progress = true
		}
	}
	return progress, nil
}

// attempt applies one package and classifies what came back.
//
// Only conditions about the *vault* end the whole drain; everything else,
// including a conflict and a package this vault cannot accept, is a hold on that
// package and nothing more. Two of those conditions are sentinels the
// application returns (fatalToDrain), and the third is a state the application
// can leave behind without naming it, which is what unresolved asks about.
func (d *drain) attempt(found *candidate) (bool, error) {
	result, err := applyLocked(d.root, found.dir, d.opts)
	switch {
	case fatalToDrain(err):
		// The package was attempted and it failed; what stops the drain is what the
		// failure says about the vault, not about this package. Saying so here is
		// what keeps it out of the not-attempted bucket the rest of the pass lands in.
		found.reason, found.detail, found.conflict = HoldFailed, err.Error(), nil
		return false, err
	case result != nil:
		// A result is the one fact that outranks whatever the error says. The package
		// landed — committed, journaled, snapshotted — so it is recorded as applied
		// and the bookkeeping failure is carried out separately; see
		// Drained.Unfinished. Classifying on the error first would be the expensive
		// mistake, because some of the errors below can arrive *after* the content is
		// durable, and the package would then be reported as still in the channel.
		found.done = true
		d.applied = append(d.applied, result)
		d.unfinished = errors.Join(d.unfinished, unfinishedWork(result, err))
	case errors.Is(err, ErrConflict):
		found.reason, found.detail = HoldConflict, err.Error()
		found.conflict = d.recordedConflict(found)
	case errors.Is(err, vaultsync.ErrIncompletePackage):
		// The scan read a manifest, so this package looked whole; Accept then found a
		// payload the transport has not finished copying. That is the same situation
		// as a package with no manifest yet and gets the same answer — calling it a
		// failure would send somebody looking for a broken vault.
		found.reason, found.detail, found.conflict = HoldUnreadable, err.Error(), nil
	case rejected(err):
		found.reason, found.detail, found.conflict = HoldRejected, err.Error(), nil
	default:
		// The conflict this package was last held on, if it was, is over: it failed
		// for another reason entirely, and reporting the stale record would name
		// contested paths and retained branches that no longer describe anything.
		found.reason, found.detail, found.conflict = HoldFailed, err.Error(), nil
	}
	if unresolved := d.unresolved(); unresolved != nil {
		return found.done, errors.Join(err, unresolved)
	}
	return found.done, nil
}

// rejected reports the refusals no amount of waiting fixes: the package is not
// one this build understands, not one this vault is addressed by, or not one
// whose paths are portable vault content at all.
//
// It is asked in two places — of what the scan could not read, and of what the
// application refused — because the same refusal surfaces in either depending on
// whether it is a fact about the manifest or a fact about the enrollment. What
// it decides is only the *class of remedy*; vaultsync keeps these sentinels
// deliberately fine-grained because their individual remedies differ, and
// Held.Detail carries the one that fired, so nothing here collapses them.
func rejected(err error) bool {
	for _, refusal := range []error{
		vaultsync.ErrUnsupportedFormat, vaultsync.ErrMalformedPackage,
		vaultsync.ErrPathTraversal, vaultsync.ErrPrivatePath, vaultsync.ErrPathCollision,
		vaultsync.ErrWrongVault, vaultsync.ErrUnknownPeer,
		vaultsync.ErrStaleEpoch, vaultsync.ErrEpochAhead,
		ErrMultipleOperations,
	} {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

// unfinishedWork names the package behind an application whose content landed
// and whose bookkeeping did not, and answers nothing for the ordinary case.
//
// The identity is added because the error comes from the receipt and state
// machinery and says nothing about which of the pass's packages it was about.
// Which of the two shapes it is, is read from the receipt rather than guessed
// from the error: a Result carries a Receipt exactly when this vault has an
// acknowledgment for the package, whether it minted it now or is answering a
// redelivery from one it minted before. So a Result carrying one has a failure
// somewhere after the acknowledgment — the import record, or putting the
// acknowledgment back in the channel — and the error itself says which. Calling
// that one unacknowledged would send a person looking for a receipt that
// already exists, and away from the step that actually failed.
func unfinishedWork(result *Result, err error) error {
	if err == nil {
		return nil
	}
	if result.Receipt != nil {
		return fmt.Errorf("vaultimport: %s was applied and acknowledged and the work after that did not finish: %w",
			result.PackageID, err)
	}
	return fmt.Errorf("vaultimport: %s was applied and not acknowledged: %w", result.PackageID, err)
}

// fatalToDrain reports the refusals that are about this vault rather than about
// one package: another import holds the guard — impossible here, since this
// holds it, but reported rather than swallowed if it ever is — an interrupted
// application that cannot be resolved without losing content, and a record that
// cannot be read or does not belong to this vault. None of them gets better by
// trying the next package.
func fatalToDrain(err error) bool {
	switch {
	case errors.Is(err, ErrImportBusy), errors.Is(err, ErrStrandedImport):
		return true
	case errors.Is(err, ErrCorruptState), errors.Is(err, ErrUnsupportedVersion), errors.Is(err, ErrIdentityChanged):
		return true
	}
	return false
}

// unresolved stops a drain that has left an application pending, and it is the
// one check here that exists because *this* caller is a loop.
//
// Apply resolves an interrupted attempt before every single application, so it
// can never hold two. A drain applies many under one guard and resolves once, at
// the top — so an application that fails in a way its own in-process recovery
// also cannot finish (a snapshot the autosave watcher's lock refuses twice, a
// provenance write that fails twice) leaves State.Pending set and returns an
// error that is none of the fatal sentinels. Carrying on would be destructive
// rather than merely wrong: the next package's declare overwrites State.Pending,
// and the record naming the first package's journal entry and manifest is the
// only route back to a change that is already on the disk. Stopping leaves it
// exactly where the next drain's recovery pass will find it.
func (d *drain) unresolved() error {
	state, err := d.load()
	if err != nil {
		return err
	}
	if state.Pending == nil {
		return nil
	}
	return fmt.Errorf("vaultimport: the application of %s is unresolved; no further package was attempted",
		state.Pending.PackageID())
}

// recordedConflict reads back the hold the refusal just wrote, so the report
// carries the retained branches rather than only the message.
func (d *drain) recordedConflict(found *candidate) *ConflictRecord {
	state, err := d.load()
	if err != nil {
		return nil
	}
	return state.Conflicts[found.packageID()]
}

// scanVerdict splits what the scan could not read into the two answers a person
// acts on differently.
//
// The scan reads a manifest, so most of what it cannot read is a directory the
// transport is still filling and the answer is to wait. The rest is a package
// this build will never read — a format token it does not know, a manifest that
// contradicts itself, a path that is not portable vault content, more than one
// operation in one package — and calling that a wait is how a peer that has
// moved to a newer format goes unnoticed for as long as nobody looks. Anything
// else, an I/O failure on a directory the transport owns, reads as a wait,
// because that is usually what it is.
func scanVerdict(err error) HoldReason {
	if rejected(err) {
		return HoldRejected
	}
	return HoldUnreadable
}

// waits is one scheduling rule's answer: why this package must wait, and which
// package in the channel it is waiting on when the rule knows one.
//
// The blocker is carried rather than left in the message because a reader acts on
// it — inverted over the channel it is what a held conflict's dependents are —
// and parsing it back out of a sentence would make the wording load-bearing. An
// empty reason is a rule that does not hold this package.
type waits struct {
	reason  HoldReason
	detail  string
	blocker string
}

// hold decides whether a package must wait, and records why.
//
// The four rules are checked in order of how much they explain. An unreadable
// package is not a scheduling decision at all. A stated dependency is the
// producer's own claim about what must come first. A causal predecessor is the
// per-path version of the same claim, and is what makes a held operation hold
// its dependents. A path shared with an earlier package is the backstop for the
// case the first three miss: two packages that both change one note, where the
// later one's baseline is the earlier one's result.
func (d *drain) hold(found *candidate, state *State) bool {
	if found.unreadable != nil {
		found.reason, found.detail = scanVerdict(found.unreadable), found.unreadable.Error()
		return true
	}
	for _, rule := range []func(*candidate, *State) waits{
		d.dependency, d.predecessor, d.order,
	} {
		if answer := rule(found, state); answer.reason != "" {
			found.reason, found.detail, found.blocker, found.conflict = answer.reason, answer.detail, answer.blocker, nil
			return true
		}
	}
	return false
}

// dependency holds a package whose stated Requires name a position this vault
// has not applied.
//
// The position is matched exactly rather than against a high-water mark, and
// that is the conservative reading of what a dependency names: one package, at
// one place in one device's stream. A frontier would have to assume the stream
// has no gaps, and a vault that has applied 1 and 3 has no way to know whether 2
// was skipped or has not arrived.
// The blocker is left empty on purpose: a dependency names a position in a
// device's stream, and the package that would satisfy it is precisely the one
// this vault has never seen.
func (d *drain) dependency(found *candidate, state *State) waits {
	for _, required := range found.manifest.Requires {
		if appliedPosition(state, required.OriginDevice, required.Sequence) {
			continue
		}
		return waits{reason: HoldDependency, detail: fmt.Sprintf(
			"requires %s position %d, which this vault has not applied",
			required.OriginDevice, required.Sequence)}
	}
	return waits{}
}

// appliedPosition reports whether this vault has applied the package at one
// position in one device's stream. A converged package counts: the changes are
// on this disk, which is what a dependent needs, and the only thing it lacks is
// a receipt the peer is owed.
func appliedPosition(state *State, origin string, sequence uint64) bool {
	for _, record := range state.Applied {
		if record.OriginDevice == origin && record.Sequence == sequence {
			return true
		}
	}
	return false
}

// predecessor holds a package one of whose changes names a causal predecessor
// carried by another package still in the channel.
//
// Only packages *in the channel* count, and that restriction is load-bearing
// rather than an optimization. A predecessor names the operation that last
// touched the path on the producer, and that is just as often one of this
// vault's own operations, which the peer imported — an identity that appears in
// no import record here and never will. Holding on every unknown predecessor
// would deadlock the first package a peer ever sends back. Holding on a known
// one that has not been applied is the case that is both decidable and worth
// deciding.
func (d *drain) predecessor(found *candidate, _ *State) waits {
	waiting := d.carriers()
	for _, change := range found.operation.Changes {
		for _, ancestor := range change.Predecessors {
			blocker, held := waiting[ancestor]
			if !held || blocker == found {
				continue
			}
			return waits{
				reason: HoldPredecessor,
				detail: fmt.Sprintf("%q follows operation %s, which %s still holds",
					change.Path, ancestor, blocker.manifest.PackageID),
				blocker: blocker.manifest.PackageID,
			}
		}
	}
	return waits{}
}

// carriers maps the operation identity of every package still in the channel to
// the package carrying it.
func (d *drain) carriers() map[string]*candidate {
	waiting := make(map[string]*candidate, len(d.pending))
	for _, other := range d.pending {
		if other.done || other.operation == nil {
			continue
		}
		waiting[other.operation.OperationID] = other
	}
	return waiting
}

// follows reports whether other is causally downstream of found, through the
// predecessor edges carried by the packages still in the channel.
//
// It exists because the drain's total order knows nothing about causality and
// the predecessor rule knows nothing about the total order, and the pair that
// makes them disagree is an ordinary one: a peer that imported another peer's
// operation and then edited the same note publishes a package that *follows* an
// operation whose own package may sort after it, since origin device sorts
// first and sorts lexically. The follower is then held on its predecessor, the
// predecessor is held on the follower for changing a path an "earlier" package
// changes, and neither can ever leave — a stall no delivery and no resolution
// clears, which is precisely what this scheduler promises cannot happen.
//
// Causality wins, because it is a claim the producer actually made about two
// events, where the total order is a tie-break this package invented to make one
// channel drain the same way twice. The walk is transitive for the same reason
// it has to be: a chain of three across three origins puts the same stall one
// link further away.
// The carriers map is passed in rather than built here: this is asked once per
// pair of packages, and rebuilding an index over the whole channel inside it
// would make one pass cubic in the number of deliveries.
func follows(carriers map[string]*candidate, other, found *candidate) bool {
	seen := make(map[string]struct{}, len(carriers))
	stack := []*candidate{other}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, been := seen[current.operation.OperationID]; been {
			continue
		}
		seen[current.operation.OperationID] = struct{}{}
		for _, ancestor := range ancestorsOf(current.operation) {
			if ancestor == found.operation.OperationID {
				return true
			}
			if next, carried := carriers[ancestor]; carried {
				stack = append(stack, next)
			}
		}
	}
	return false
}

// ancestorsOf is every causal predecessor one operation names, across all of its
// changes.
func ancestorsOf(operation *vaultsync.Operation) []string {
	ancestors := make([]string, 0, len(operation.Changes))
	for _, change := range operation.Changes {
		ancestors = append(ancestors, change.Predecessors...)
	}
	return ancestors
}

// order holds a package that changes a path an earlier unapplied package also
// changes.
//
// Compared through CollisionKey rather than literally, because two spellings
// that are one file on a folding replica are one path for this purpose too —
// and the one place that matters is exactly the one this backstop exists for.
//
// "Earlier" is the drain's own total order, which is strict, so two packages
// that overlap cannot hold each other: the earlier one goes first and the later
// one waits for it. Without that the pair would stall and the loop would report
// both as held forever.
//
// A package that causally *follows* this one is never earlier, whatever the
// total order says, and that exception is the other half of the same promise —
// see follows, which is where the stall this rule cannot see comes from.
func (d *drain) order(found *candidate, _ *State) waits {
	carriers := d.carriers()
	for _, other := range d.pending {
		if other == found || other.done || other.operation == nil || !before(other, found) {
			continue
		}
		if follows(carriers, other, found) {
			continue
		}
		if shared := overlap(found.operation, other.operation); shared != "" {
			return waits{
				reason:  HoldOrder,
				detail:  fmt.Sprintf("changes %q, which %s changes first", shared, other.manifest.PackageID),
				blocker: other.manifest.PackageID,
			}
		}
	}
	return waits{}
}

// overlap returns a path both operations change, or the empty string.
func overlap(a, b *vaultsync.Operation) string {
	keys := make(map[string]struct{}, len(b.Changes))
	for _, change := range b.Changes {
		keys[vaultsync.CollisionKey(change.Path)] = struct{}{}
	}
	for _, change := range a.Changes {
		if _, shared := keys[vaultsync.CollisionKey(change.Path)]; shared {
			return change.Path
		}
	}
	return ""
}

// holds reports what is still in the channel, in the order it was decided in.
//
// A package with no verdict was never reached, which happens only on the partial
// report a mid-pass refusal returns. It is named as such rather than left blank,
// because a blank reason renders as a hold whose explanation went missing, and
// what it actually means is that the drain stopped before this package's turn —
// the opposite of a package that was looked at and set aside.
func (d *drain) holds() []Held {
	held := make([]Held, 0, len(d.pending))
	for _, found := range d.pending {
		if found.done {
			continue
		}
		entry := Held{
			PackageID: found.packageID(),
			Dir:       found.dir,
			Reason:    found.reason,
			Detail:    found.detail,
			Conflict:  found.conflict,
		}
		if entry.Reason == "" {
			entry.Reason = HoldNotAttempted
			entry.Detail = "the drain stopped before this package was decided"
		}
		if found.manifest != nil {
			entry.OriginDevice = found.manifest.OriginDevice
			entry.Sequence = found.manifest.Sequence
		}
		held = append(held, entry)
	}
	return held
}

// collectRetained removes every conflict directory the record does not name.
//
// It is the other half of release's ordering: a hold stops naming its bytes
// before they are removed, so a crash in between leaves a directory nothing
// points at. This is what eventually takes it away, and it is a sweep rather
// than a retry because the process that would have retried is the one that died.
func collectRetained(root string, keep map[string]*ConflictRecord) error {
	dir := conflictsRoot(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list retained conflicts %q: %w", dir, err)
	}
	var failures []error
	for _, entry := range entries {
		if _, named := keep[entry.Name()]; named || !vaultsync.PackageName(entry.Name()) {
			continue
		}
		failures = append(failures, discardRetained(root, entry.Name()))
	}
	return errors.Join(failures...)
}
