package vaultimport

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// Pending is one package the incoming channel still holds, as a read-only
// inspection sees it.
//
// It is deliberately not Held. A Held is the verdict a drain reached while it was
// applying, which is the only place a conflict can be *discovered*; this is what
// a reader can say about the same channel without touching the vault, and the two
// differ in exactly one way that matters. A conflict here is read from the
// durable record a previous drain wrote, because nothing short of attempting the
// application can find a new one — so a package this inspection reports with no
// reason at all is one the next drain will attempt, not one it will accept.
type Pending struct {
	// PackageID is the package, empty when it could not be read.
	PackageID string
	// Dir is where it sits in the incoming channel.
	Dir string
	// OriginDevice and Sequence are its position in the producer's stream, zero
	// when they could not be read.
	OriginDevice string
	Sequence     uint64
	// Reason is why the next drain would not apply it, empty when nothing this
	// inspection can see would hold it back.
	Reason HoldReason
	// Detail is the human-readable specifics.
	Detail string
	// Blocker is the package in this channel that this one waits on, set only for
	// the two reasons that name one: a causal predecessor another delivery carries,
	// and a path an earlier delivery changes first. A stated dependency names a
	// position in a stream rather than a package, and leaves this empty.
	//
	// It is what makes "resolve this conflict and these three arrive with it"
	// sayable: invert it over the channel and a held conflict knows its dependents.
	Blocker string
	// Conflict is the durable hold a previous drain recorded, nil unless Reason is
	// HoldConflict. It carries every contested path with the remote branch, the
	// local branch, and the baseline.
	Conflict *ConflictRecord
}

// Inspect reports what this vault's incoming channel is still holding.
//
// It applies nothing, writes nothing, and takes no lock, which is what makes it
// safe for a status surface to call on a vault a synchronization daemon is
// working in. The cost of that is stated in Pending: the answer is a snapshot
// that a drain running concurrently may already have moved past, so a caller
// acting on it — a resolution, most of all — still has to be refused by the
// engine rather than trusted because this said so. That refusal exists: Preview
// pins the contested set and Resolve re-checks it.
//
// The channel is read in the same total order a drain uses, so a status page and
// a drain describe one channel the same way round.
func Inspect(root string) ([]Pending, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	found, _, err := scanChannel(root)
	if err != nil {
		return nil, err
	}
	// Built with the channel it is deciding over, because the predecessor and
	// order rules are facts about the set rather than about one package.
	run := &drain{root: root, pending: found}
	out := make([]Pending, 0, len(found))
	for _, candidate := range found {
		out = append(out, run.inspected(candidate, state))
	}
	return out, nil
}

// inspected is one package's read-only verdict.
//
// The durable conflict is asked about first and outranks the scheduling rules,
// because it is the one a person can act on: a package whose contested paths are
// recorded is waiting for a decision, and reporting instead that it also happens
// to change a path some later delivery touches would send them to the wrong
// surface. A drain re-decides in the other order for the opposite reason — it is
// about to apply, and ordering decides whether it may.
func (d *drain) inspected(found *candidate, state *State) Pending {
	entry := Pending{PackageID: found.packageID(), Dir: found.dir}
	if found.manifest != nil {
		entry.OriginDevice, entry.Sequence = found.manifest.OriginDevice, found.manifest.Sequence
	}
	if record := recordedFor(state, entry.PackageID); record != nil {
		entry.Reason, entry.Detail, entry.Conflict = HoldConflict, contestedDetail(record), record
		return entry
	}
	if d.hold(found, state) {
		entry.Reason, entry.Detail, entry.Blocker = found.reason, found.detail, found.blocker
	}
	return entry
}

// recordedFor is the durable conflict this package is held on, if any. The empty
// identity is never looked up: a package whose manifest could not be read has no
// identity to match a record with, and the empty key would match a record the
// state file cannot hold anyway.
func recordedFor(state *State, packageID string) *ConflictRecord {
	if packageID == "" {
		return nil
	}
	return state.Conflicts[packageID]
}

// Offer is one contested path with the decisions it can actually take.
//
// It is the answer to the question a review surface has to ask before it draws
// anything: this path is contested, so which of the four choices may it be
// offered? A surface that offered all four and reported the refusal afterwards
// would put a person in front of a button whose only outcome is an error — and
// the refusals are not incidental. A configuration conflict cannot be kept twice,
// a rename cannot be merged, a path this vault deleted has no current version to
// keep, and a filename collision cannot be resolved here at all.
type Offer struct {
	// Contested is the path as the hold recorded it: the kind, the peer's branch,
	// the baseline, and the rename or collision partner where there is one.
	Contested PathConflict
	// Local is what this vault holds at that path *now*, which is not necessarily
	// what the hold recorded. It is the same reading Preview pins, so it is the
	// version a decision made from this offer will be measured against.
	Local Branch
	// Available are the choices this path can take, in presentation order.
	Available []Choice
	// Refused are the ones it cannot, each with the reason, so a surface can show
	// a disabled choice that says why rather than hiding it.
	Refused map[Choice]string
}

// choiceOrder is every choice, in the order a surface should present them: the
// two that decide against a version first, then the two that keep both sides of
// the disagreement.
var choiceOrder = []Choice{KeepCurrent, AcceptIncoming, KeepBoth, ManualMerge}

// Offers reports what can be decided about each contested path of a held
// package, and writes nothing.
//
// Each choice is answered by planning it rather than by consulting a table, so
// there is exactly one statement in this package of what fits which conflict and
// this cannot drift from what Preview will accept. That is also its limit: an
// offer is about one path, and two paths whose resolutions would collide with
// each other are refused by Preview when the whole plan is built. A surface that
// takes an offer as a promise is wrong in the same way one that takes Inspect as
// a promise is wrong — the engine refuses, and it refuses late on purpose.
func Offers(root, packageID string) ([]Offer, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	held, holding := state.Conflicts[packageID]
	if !holding {
		return nil, fmt.Errorf("%w: %s", ErrNoConflict, packageID)
	}
	out := make([]Offer, 0, len(held.Paths))
	for _, contested := range held.Paths {
		offer, err := offerFor(root, held, contested)
		if err != nil {
			return nil, err
		}
		out = append(out, offer)
	}
	return out, nil
}

// offerFor plans every choice for one path and sorts them by which ones worked.
//
// The local branch is read here rather than taken from whichever probe succeeded,
// because a path where every choice is refused — a filename collision, or a name
// now held by a directory — still has a version this vault holds, and a surface
// that could not name it would have nothing to show beside the peer's.
func offerFor(root string, held *ConflictRecord, contested PathConflict) (Offer, error) {
	disk, err := diskState(absPath(root, contested.Path))
	if err != nil {
		return Offer{}, err
	}
	offer := Offer{Contested: contested, Local: localBranch(disk), Refused: map[Choice]string{}}
	for _, choice := range choiceOrder {
		_, _, err := previewPath(root, held, contested, probeFor(contested.Path, choice))
		switch {
		case errors.Is(err, ErrResolutionUnavailable):
			offer.Refused[choice] = unavailableReason(err)
		case err != nil:
			// Not a refusal of this choice but a fact about the vault — an unreadable
			// path, a corrupt hold — and it would be the same answer for all four.
			return Offer{}, err
		default:
			offer.Available = append(offer.Available, choice)
		}
	}
	return offer, nil
}

// probeFor is the decision a probe makes: this one path, this one choice.
//
// The empty merge matters. manualMergeSteps refuses a merge with no content
// supplied, which is a fact about the caller rather than about the conflict, so a
// probe that left it out would report every path as unmergeable. Empty content is
// a legitimate merge — see Choices.Merged — so this asks only whether the kind
// can be merged at all.
func probeFor(rel string, choice Choice) Choices {
	return Choices{Default: choice, Merged: map[string][]byte{rel: {}}}
}

// unavailableReason is the refusal without its sentinel, which is the half a
// person reads. The sentinel says "that resolution cannot be carried out", which
// is what the disabled control already says.
func unavailableReason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrResolutionUnavailable.Error()+": ")
}

// contestedDetail says how much is contested, and never which paths.
//
// The paths are in the record, where a caller reads them with the branches that
// give them meaning. Naming one of several here would make the summary read as
// the whole disagreement.
func contestedDetail(record *ConflictRecord) string {
	if len(record.Paths) == 1 {
		return fmt.Sprintf("this vault disagrees about %q", record.Paths[0].Path)
	}
	return fmt.Sprintf("this vault disagrees about %d paths this package changes", len(record.Paths))
}
