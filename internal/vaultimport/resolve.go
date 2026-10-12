package vaultimport

import (
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// Choice is what somebody decided to do about one contested path.
//
// The four are the whole vocabulary, and they are named for the act rather than
// for the branch: "keep the current one" is a sentence a person can check
// against what they see, where "local" and "remote" swap meaning depending on
// which replica is asking.
type Choice string

const (
	// KeepCurrent keeps what this vault holds and rejects the peer's version.
	//
	// The rejected version is not discarded. It reaches this vault's content
	// history as the applied operation and is then revised away, so it stays in
	// the recovery history and in Git, and undoing the resolution brings it back.
	KeepCurrent Choice = "keep-current"
	// AcceptIncoming takes the peer's version. It is the only choice that revises
	// nothing, because applying the operation is already the whole of it.
	AcceptIncoming Choice = "accept-incoming"
	// KeepBoth takes the peer's version at the contested path and writes this
	// vault's version beside it under a derived name, so neither is decided
	// against and both are visible in one place.
	KeepBoth Choice = "keep-both"
	// ManualMerge installs content the caller supplies, which is what a
	// configuration conflict needs and what a person editing two versions
	// together produces.
	ManualMerge Choice = "manual-merge"
)

// Choices is what a caller decided, before it is checked against what the vault
// holds.
//
// The zero value decides nothing and is refused: the empty Choice is not one of
// the four, so every contested path previews as ErrResolutionUnavailable.
// Accepting everything incoming is a decision somebody makes, and it is spelled
// Default: AcceptIncoming.
type Choices struct {
	// Default applies to every contested path Paths does not name.
	Default Choice
	// Paths overrides the default for individual paths, keyed by the contested
	// path as PathConflict records it.
	Paths map[string]Choice
	// Merged carries the content for a ManualMerge, keyed the same way. A nil
	// entry is the same as an absent one; empty content is a legitimate merge and
	// is distinguished from both.
	Merged map[string][]byte
}

// choice is what was decided about one path.
func (c Choices) choice(rel string) Choice {
	if chosen, named := c.Paths[rel]; named {
		return chosen
	}
	return c.Default
}

// Plan is a resolution decided but not yet carried out: every affected path,
// both branches of each, and the exact steps the revision will take.
//
// It is the read-only half of resolving, and it exists as a value a caller can
// show somebody before anything happens. It is also what the vault records
// durably once the resolution starts, which is why it names the journal entry
// the revision will be written under: an interrupted resolution is finished
// from this record, and a revision whose identity was not written down first
// could not be told from one that never ran.
type Plan struct {
	// PackageID is the held package being resolved.
	PackageID string `json:"package_id"`
	// OperationID is the peer operation it carries, which the revision names as
	// the predecessor it resolves.
	OperationID string `json:"operation_id"`
	// OriginDevice is the peer that produced it.
	OriginDevice string `json:"origin_device"`
	// Sequence is its position in that peer's stream.
	Sequence uint64 `json:"sequence"`
	// JournalEntry is the recovery entry the revision commit will be written
	// under. It is empty in a preview and minted when the resolution starts.
	JournalEntry string `json:"journal_entry,omitempty"`
	// Paths are the contested paths in the order the hold records them, each with
	// what resolving it would do.
	Paths []PathPlan `json:"paths"`
	// carried is the content each write step installs, held beside the plan
	// rather than inside it: the plan is written to a state file and a note's
	// content does not belong there. Preview fills it; a plan that has been
	// through a state file re-reads what it can from the vault and the hold's
	// retained copies, which is every branch except a merge only its author has.
	carried map[string][]byte
}

// PathPlan is one contested path with the decision made about it.
//
// Both branches are recorded rather than referenced, and the local one is read
// from the disk at preview time rather than taken from the hold. The hold's
// reading is as old as the hold, and the point of pinning is to describe the
// version the person is actually looking at.
type PathPlan struct {
	// Path is the contested path.
	Path string `json:"path"`
	// Kind is why it was contested.
	Kind ConflictKind `json:"kind"`
	// Choice is what was decided.
	Choice Choice `json:"choice"`
	// Remote is what the package installs there, which is what the path holds
	// once the operation has been applied.
	Remote Branch `json:"remote"`
	// Local is what this vault holds there now.
	Local Branch `json:"local"`
	// Steps are the revisions this choice makes after the operation is applied,
	// and are empty for a choice the applied operation already satisfies.
	Steps []Step `json:"steps,omitempty"`
}

// Step is one write or removal the revision makes.
//
// The content is named by digest rather than carried, because the store it is
// staged in is content-addressed for the same reason the retained branches are:
// two paths resolved to the same bytes cost one file, and re-driving an
// interrupted revision rewrites the same names.
type Step struct {
	// Path is the vault-relative path it changes, which is the contested path for
	// most choices and a derived one for KeepBoth.
	Path string `json:"path"`
	// Action is vaultsync.ActionWrite or vaultsync.ActionDelete.
	Action vaultsync.Action `json:"action"`
	// SHA256 is the content a write installs, and names its staged copy.
	SHA256 string `json:"sha256,omitempty"`
	// Expect is what the path must hold for this step to run: the state the
	// applied operation leaves it in. It becomes the transaction's precondition,
	// so a resolution that races an edit is refused under the path's own lock.
	Expect Branch `json:"expect"`
}

// redundant reports a step that would install what its own precondition already
// requires. Such a step is dropped at preview time rather than staged: a
// transaction that writes a path the content it holds captures a journal entry
// and produces a commit that says nothing.
func (s Step) redundant() bool {
	if s.Action == vaultsync.ActionDelete {
		return !s.Expect.Exists
	}
	return s.Expect.Exists && s.Expect.SHA256 == s.SHA256
}

// result is the state the step leaves its path in, which is how a re-driven
// revision recognises a step that has already run.
func (s Step) result() pathState {
	if s.Action == vaultsync.ActionDelete {
		return pathState{}
	}
	return pathState{present: true, regular: true, sha256: s.SHA256}
}

// landed is the precondition for undoing a step that already ran: the path
// still holds exactly what the step put there. It is the mirror of Expect, and
// it is what an unwind pins so an edit landing after the interrupted commit
// refuses the reversal instead of being lost to it.
func (s Step) landed() vault.FileExpectation {
	if s.Action == vaultsync.ActionDelete {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(s.SHA256)
}

// expectation is the branch as the precondition a transaction verifies under
// the path's own lock.
func (b Branch) expectation() vault.FileExpectation {
	if !b.Exists {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(b.SHA256)
}

// same compares two readings of one branch by what they say the path holds,
// ignoring where the bytes were kept. A hold re-detected on a later pass
// retains the same content under the same name, but a branch whose bytes could
// not be kept and one whose bytes could are the same branch.
func (b Branch) same(other Branch) bool {
	return b.Exists == other.Exists && b.SHA256 == other.SHA256
}

// steps are every step the plan makes, across all paths.
func (p *Plan) steps() []Step {
	steps := make([]Step, 0, len(p.Paths))
	for _, decided := range p.Paths {
		steps = append(steps, decided.Steps...)
	}
	return steps
}

// Revises reports a plan that has anything to record after the operation is
// applied. A plan that revises nothing is a resolution that accepted everything
// incoming, and it is carried out by applying the package and no more.
//
// It is exported because the difference is a sentence somebody reads. A resolution
// finishes with an empty JournalEntry in two unrelated situations — a plan that had
// no second commit to make, and a plan whose second commit has not been made yet —
// and a caller reporting what durably happened has nothing else to tell them apart
// with. Saying "your decision has not been recorded yet" about the first one sends a
// person to wait for a commit that is never coming.
func (p *Plan) Revises() bool { return len(p.steps()) > 0 }

// validate checks a promised resolution read back out of the state file.
//
// There is no cross-check against the other two ledgers here, and that is not an
// omission: a promise is recorded while the package is still held and is cleared
// after it is applied, so it legitimately coexists with either. What it must
// never be is empty, since a promise with no steps is a revision that can never
// be recognised as finished.
func (p *Plan) validate(packageID string) error {
	switch {
	case p == nil:
		return fmt.Errorf("%w: promised resolution for %s has no plan", ErrCorruptState, packageID)
	case p.PackageID != packageID:
		return fmt.Errorf("%w: promised resolution for %s carries a plan for %s",
			ErrCorruptState, packageID, p.PackageID)
	case p.JournalEntry == "":
		return fmt.Errorf("%w: promised resolution for %s names no recovery entry",
			ErrCorruptState, packageID)
	case !p.Revises():
		return fmt.Errorf("%w: promised resolution for %s revises nothing", ErrCorruptState, packageID)
	}
	return nil
}

// pin is the precondition this plan puts in the producer's place for one path,
// and reports whether the path is one the plan decided.
//
// It is the local branch, which is to say the version somebody was looking at.
// The producer's expectation is the baseline both replicas last agreed on, and
// that is exactly what this vault no longer holds — pinning it is what refuses
// the package in the first place. Pinning the local branch instead makes the
// application land over the version the decision was made about and nothing
// else: a note edited in between fails the precondition under its own lock,
// with the edit intact.
func (p *Plan) pin(rel string) (vault.FileExpectation, bool) {
	for _, decided := range p.Paths {
		if decided.Path == rel {
			return decided.Local.expectation(), true
		}
	}
	return vault.FileExpectation{}, false
}

// covers checks that the conflicts standing in the way of an application are
// exactly the ones this plan decided, and is the resolution's answer to the
// refusal an ordinary import would raise.
//
// Every path, and every path's kind and local version. A plan is permission to
// write over specific content for a specific reason, so a conflict that was not
// decided, one whose kind has changed — a concurrent edit that is now a
// deletion — or one whose local version has moved all mean the decision was
// about a vault that no longer exists.
func (p *Plan) covers(found []PathConflict) error {
	if len(found) != len(p.Paths) {
		return fmt.Errorf("%w: %s now contests %d paths rather than the %d that were decided",
			ErrStalePreview, p.PackageID, len(found), len(p.Paths))
	}
	decided := make(map[string]PathPlan, len(p.Paths))
	for _, one := range p.Paths {
		decided[one.Path] = one
	}
	for _, contested := range found {
		one, planned := decided[contested.Path]
		switch {
		case !planned:
			return fmt.Errorf("%w: %s is contested and no decision was made about it",
				ErrStalePreview, contested.Path)
		case one.Kind != contested.Kind:
			return fmt.Errorf("%w: %s is now a %s rather than the %s that was decided",
				ErrStalePreview, contested.Path, contested.Kind, one.Kind)
		case !one.Local.same(contested.Local):
			return fmt.Errorf("%w: %s has changed here since the decision was made",
				ErrStalePreview, contested.Path)
		}
	}
	return nil
}

// Preview decides what resolving a held package under these choices would do,
// without touching the vault.
//
// Everything a caller needs to show somebody is in the returned plan: every
// affected path — including the ones a choice derives, which are not in the
// hold — with the version this vault holds and the version the peer sent, and
// the exact writes and removals that would follow. A choice that does not fit
// the conflict it was made about is refused here as ErrResolutionUnavailable
// rather than halfway through.
//
// The reading is of this instant and says so: Resolve checks the same facts
// again and refuses a plan the vault has moved past. See ErrStalePreview.
func Preview(root, packageID string, choices Choices) (*Plan, error) {
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
	return preview(root, held, choices)
}

// preview is the planning itself, with the hold already in hand.
func preview(root string, held *ConflictRecord, choices Choices) (*Plan, error) {
	planned := &Plan{
		PackageID:    held.PackageID,
		OperationID:  held.OperationID,
		OriginDevice: held.OriginDevice,
		Sequence:     held.Sequence,
		Paths:        make([]PathPlan, 0, len(held.Paths)),
		carried:      map[string][]byte{},
	}
	for _, contested := range held.Paths {
		decided, content, err := previewPath(root, held, contested, choices)
		if err != nil {
			return nil, err
		}
		planned.Paths = append(planned.Paths, decided)
		for digest, data := range content {
			planned.carried[digest] = data
		}
	}
	if err := planned.distinct(); err != nil {
		return nil, err
	}
	return planned, nil
}
