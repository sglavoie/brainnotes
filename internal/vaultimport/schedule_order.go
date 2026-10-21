package vaultimport

import (
	"errors"
	"fmt"
	"os"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

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
