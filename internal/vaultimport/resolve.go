package vaultimport

import (
	"fmt"
	"os"
	"strings"

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

// previewPath decides one contested path, and returns the content its steps
// install alongside the plan for it.
func previewPath(root string, held *ConflictRecord, contested PathConflict, choices Choices) (PathPlan, map[string][]byte, error) {
	state, err := diskState(absPath(root, contested.Path))
	if err != nil {
		return PathPlan{}, nil, err
	}
	decided := PathPlan{
		Path:   contested.Path,
		Kind:   contested.Kind,
		Choice: choices.choice(contested.Path),
		Remote: contested.Remote,
		Local:  localBranch(state),
	}
	if state.present && !state.regular {
		return PathPlan{}, nil, fmt.Errorf(
			"%w: %s is not a regular file, so no resolution can name what this vault holds there",
			ErrResolutionUnavailable, contested.Path)
	}
	steps, content, err := resolutionSteps(root, held, contested, decided, choices)
	if err != nil {
		return PathPlan{}, nil, err
	}
	decided.Steps = steps
	return decided, content, nil
}

// resolutionSteps is the table: what each choice does to each kind of conflict.
//
// A name collision is refused for every choice, and it is the one refusal that
// is about the applied operation rather than about the revision. Applying the
// package is the first half of every resolution, and applying a colliding
// creation is exactly what must not happen: on this replica it would make a
// second file that is one file on the peer, and no revision afterwards can
// unmake the disagreement about how many notes exist. The name has to be
// cleared first, after which the next drain applies the package with no
// conflict left to resolve.
func resolutionSteps(root string, held *ConflictRecord, contested PathConflict, decided PathPlan, choices Choices) ([]Step, map[string][]byte, error) {
	if contested.Kind == ConflictFilenameCollision {
		return nil, nil, fmt.Errorf(
			"%w: %s is the same name as %s here; clear the name and the package applies with nothing to decide",
			ErrResolutionUnavailable, contested.Path, collidesWith(contested))
	}
	switch decided.Choice {
	case AcceptIncoming:
		return nil, nil, nil
	case KeepCurrent:
		return keepCurrentSteps(root, held, contested, decided)
	case KeepBoth:
		return keepBothSteps(root, held, contested, decided)
	case ManualMerge:
		return manualMergeSteps(contested, decided, choices.Merged[contested.Path])
	default:
		return nil, nil, fmt.Errorf("%w: %q is not a resolution, for %s or anything else",
			ErrResolutionUnavailable, decided.Choice, contested.Path)
	}
}

// collidesWith names the other side of a collision, which is a directory rather
// than a note when the name is occupied by something that is not a file.
func collidesWith(contested PathConflict) string {
	if contested.Collides == "" {
		return "something that is not a note"
	}
	return contested.Collides
}

// keepCurrentSteps revises the applied operation back to what this vault holds.
func keepCurrentSteps(root string, held *ConflictRecord, contested PathConflict, decided PathPlan) ([]Step, map[string][]byte, error) {
	if contested.Kind == ConflictDivergentRename {
		return keepCurrentRename(contested)
	}
	if !decided.Local.Exists {
		// The peer edited a note this vault deleted on purpose. Applying the
		// operation resurrects it, and keeping the current version means deleting
		// it again — from a path that now holds the peer's content, which is what
		// puts the rejected version in the history.
		return trim([]Step{{Path: contested.Path, Action: vaultsync.ActionDelete, Expect: decided.Remote}}), nil, nil
	}
	data, err := localContent(root, held, contested, decided.Local)
	if err != nil {
		return nil, nil, err
	}
	step := Step{
		Path:   contested.Path,
		Action: vaultsync.ActionWrite,
		SHA256: decided.Local.SHA256,
		Expect: decided.Remote,
	}
	return trim([]Step{step}), map[string][]byte{decided.Local.SHA256: data}, nil
}

// keepCurrentRename rejects a move this vault had already made differently.
//
// The contested path is the *source* of the rename — the name the note used to
// have, which is gone here for a reason the package cannot see. Applying the
// operation writes the content under the peer's chosen destination; keeping the
// current arrangement means removing that destination, leaving whatever this
// vault did with the note alone. The destination is required to hold exactly
// the content the operation moved there, so a resolution that races somebody
// editing it is refused rather than deleting an edit.
func keepCurrentRename(contested PathConflict) ([]Step, map[string][]byte, error) {
	if contested.RenamedTo == "" || !contested.Baseline.Exists {
		return nil, nil, fmt.Errorf("%w: the divergent rename of %s names no destination content",
			ErrCorruptState, contested.Path)
	}
	step := Step{
		Path:   contested.RenamedTo,
		Action: vaultsync.ActionDelete,
		Expect: Branch{Exists: true, SHA256: contested.Baseline.SHA256},
	}
	return trim([]Step{step}), nil, nil
}

// keepBothSteps writes this vault's version beside the peer's.
//
// It is refused wherever there is no second version to keep or a copy would be
// a lie, and a missing branch on *either* side is such a case. The declarations
// govern every note in the vault and a second copy of them governs nothing, so
// offering it as a resolution would leave the conflict unresolved under a name
// that looks resolved. A divergent rename has nothing at the contested path to
// copy — the note is already somewhere else here. A path this vault deleted has
// no current version at all. And a path the peer deleted has no incoming
// version: the application removes the note, so "both" would be one file under
// a derived name and nothing under the name the note had, which is a rename
// dressed up as keeping everything.
func keepBothSteps(root string, held *ConflictRecord, contested PathConflict, decided PathPlan) ([]Step, map[string][]byte, error) {
	switch {
	case contested.Kind == ConflictConfiguration:
		return nil, nil, fmt.Errorf("%w: a second copy of %s would govern nothing; merge them instead",
			ErrResolutionUnavailable, contested.Path)
	case contested.Kind == ConflictDivergentRename:
		return nil, nil, fmt.Errorf("%w: %s was moved away from here, so there is no version of it at that name to keep",
			ErrResolutionUnavailable, contested.Path)
	case !decided.Local.Exists:
		return nil, nil, fmt.Errorf("%w: %s does not exist here, so there is no version of it to keep beside the incoming one",
			ErrResolutionUnavailable, contested.Path)
	case !decided.Remote.Exists:
		return nil, nil, fmt.Errorf("%w: the peer deleted %s rather than editing it, so there is no incoming version to keep this one beside",
			ErrResolutionUnavailable, contested.Path)
	}
	data, err := localContent(root, held, contested, decided.Local)
	if err != nil {
		return nil, nil, err
	}
	beside, err := besidePath(root, contested.Path, decided.Local.SHA256)
	if err != nil {
		return nil, nil, err
	}
	step := Step{
		Path:   beside,
		Action: vaultsync.ActionWrite,
		SHA256: decided.Local.SHA256,
		Expect: Branch{},
	}
	return trim([]Step{step}), map[string][]byte{decided.Local.SHA256: data}, nil
}

// manualMergeSteps installs content the caller produced from both versions.
//
// A rename is refused because the merge would have to decide a question about
// names rather than content: there is no pair of versions to merge, only a note
// that is in two places. Everything else accepts a merge, including a deletion
// on either side, where supplying content is the caller saying the note should
// exist and hold this.
func manualMergeSteps(contested PathConflict, decided PathPlan, merged []byte) ([]Step, map[string][]byte, error) {
	if contested.Kind == ConflictDivergentRename {
		return nil, nil, fmt.Errorf("%w: %s was moved rather than edited, so there is nothing to merge",
			ErrResolutionUnavailable, contested.Path)
	}
	if merged == nil {
		return nil, nil, fmt.Errorf("%w: no merged content was supplied for %s",
			ErrResolutionUnavailable, contested.Path)
	}
	digest := vaultsync.HashBytes(merged)
	step := Step{
		Path:   contested.Path,
		Action: vaultsync.ActionWrite,
		SHA256: digest,
		Expect: decided.Remote,
	}
	return trim([]Step{step}), map[string][]byte{digest: merged}, nil
}

// trim drops the steps that would change nothing. See Step.redundant.
func trim(steps []Step) []Step {
	kept := make([]Step, 0, len(steps))
	for _, step := range steps {
		if step.redundant() {
			continue
		}
		kept = append(kept, step)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// besidePath is the name this vault's version is kept under when both are kept.
//
// The digest makes it deterministic and idempotent: re-driving an interrupted
// revision writes the same name rather than a second copy, and two conflicts
// over the same content in one package collapse onto one name — which distinct
// then refuses rather than letting one silently overwrite the other. It is
// derived from the path's own stem so the copy sorts beside the note it came
// from, and the name is refused if anything already holds it.
func besidePath(root, rel, digest string) (string, error) {
	if len(digest) < besideDigestLength {
		return "", fmt.Errorf("%w: the current version of %s has no content to name a copy after",
			ErrResolutionUnavailable, rel)
	}
	stem, ext := splitName(rel)
	beside := fmt.Sprintf("%s (conflicted copy %s)%s", stem, digest[:besideDigestLength], ext)
	state, err := diskState(absPath(root, beside))
	if err != nil {
		return "", err
	}
	if state.present {
		return "", fmt.Errorf("%w: %s already exists, so this vault's version cannot be kept beside %s",
			ErrResolutionUnavailable, beside, rel)
	}
	return beside, nil
}

// splitName is a portable path split into the part a derived name extends and
// the suffix it keeps.
//
// It is filepath.Ext with one correction, and the correction is the whole
// reason it exists: a dotfile is *all* extension by that rule —
// filepath.Ext(".hidden") is ".hidden" — so a copy derived from it would lose
// the note's name entirely and put the conflicted-copy marker where the name
// should be. A dot at the front of the basename is part of the name rather than
// a suffix. The separator is a slash because these are vaultsync's portable
// paths, which are slash-separated on every platform.
func splitName(rel string) (stem, ext string) {
	base := rel[strings.LastIndex(rel, "/")+1:]
	dot := strings.LastIndex(base, ".")
	if dot <= 0 {
		return rel, ""
	}
	return rel[:len(rel)-len(base)+dot], base[dot:]
}

// besideDigestLength is how much of the digest the kept copy's name carries.
// Long enough to distinguish the versions of one note that a person is looking
// at, short enough to read, and it never has to be unique on its own: the name
// it produces is checked against the disk and against every other name in the
// plan.
const besideDigestLength = 8

// distinct refuses a plan whose steps would fight each other.
//
// Two rules, and both are about names rather than content. No two steps may
// name the same file, since a transaction cannot write a path twice and the
// second decision would silently win. And a step may not name a path that some
// *other* contested path's resolution is about, because the applied operation
// is what puts that path in the state its own step expects — a step landing
// there first would make the other precondition false and refuse the whole
// revision after half of it was decided.
//
// Names are compared the way vaultsync compares them everywhere else, folded,
// so a plan that is two files here and one file on the peer is refused here
// rather than discovered as a collision on the next replica.
func (p *Plan) distinct() error {
	contested := make(map[string]string, len(p.Paths))
	for _, decided := range p.Paths {
		contested[vaultsync.CollisionKey(decided.Path)] = decided.Path
	}
	claimed := make(map[string]string, len(p.Paths))
	for _, decided := range p.Paths {
		for _, step := range decided.Steps {
			key := vaultsync.CollisionKey(step.Path)
			if earlier, taken := claimed[key]; taken {
				return fmt.Errorf("%w: resolving %s and %s would both write %s",
					ErrResolutionUnavailable, earlier, decided.Path, step.Path)
			}
			claimed[key] = decided.Path
			if other, is := contested[key]; is && other != decided.Path {
				return fmt.Errorf("%w: resolving %s would write %s, which is contested in its own right",
					ErrResolutionUnavailable, decided.Path, other)
			}
		}
	}
	return nil
}

// localContent is the bytes of the version this vault holds.
//
// It is read from the vault first and from the hold's retained copy second, and
// the digest decides which answer is used rather than the order. The retained
// copy is as old as the hold and the plan pins the reading taken now, so a
// retained copy that does not hash to the pinned branch is not this version and
// is not used. A local branch whose bytes are in neither place is refused: it
// is the one version of the content that exists nowhere else, and a resolution
// that could not name it would quietly resolve to something else.
func localContent(root string, held *ConflictRecord, contested PathConflict, branch Branch) ([]byte, error) {
	if !branch.Exists {
		return nil, nil
	}
	if data, err := os.ReadFile(absPath(root, contested.Path)); err == nil && vaultsync.HashBytes(data) == branch.SHA256 {
		return data, nil
	}
	if data := readRetained(root, held.PackageID, contested.Local); data != nil &&
		vaultsync.HashBytes(data) == branch.SHA256 {
		return data, nil
	}
	return nil, fmt.Errorf("%w: the version of %s this vault holds (%s) can no longer be read",
		ErrResolutionUnavailable, contested.Path, branch.SHA256)
}

// content is the bytes one write step installs.
//
// A plan that is still the one Preview returned carries them. A plan read back
// out of the state file — which is how an interrupted resolution is finished —
// carries nothing, and every branch except a merge is still recoverable from
// the vault or from the hold's retained copies. A merge that cannot be
// recovered is refused rather than guessed at, and that case cannot arise for a
// resolution that has started: the bytes are staged before anything else about
// it is durable.
func (p *Plan) content(root string, held *ConflictRecord, digest string) ([]byte, error) {
	if data, carried := p.carried[digest]; carried && vaultsync.HashBytes(data) == digest {
		return data, nil
	}
	return recoverContent(root, held, digest)
}

// recoverContent looks for content by digest among every branch the hold knows.
func recoverContent(root string, held *ConflictRecord, digest string) ([]byte, error) {
	for _, contested := range held.Paths {
		for _, branch := range []Branch{contested.Local, contested.Remote, contested.Baseline} {
			if !branch.Exists || branch.SHA256 != digest {
				continue
			}
			if data := branchContent(root, held.PackageID, contested, branch); data != nil {
				return data, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: the content %s the resolution installs is no longer anywhere this vault can read",
		ErrResolutionUnavailable, digest)
}

// branchContent reads one branch from the vault or from its retained copy,
// reporting nothing rather than failing: a miss here is a reason to keep
// looking, and the caller is the one that decides there is nowhere left.
func branchContent(root, packageID string, contested PathConflict, branch Branch) []byte {
	if data, err := os.ReadFile(absPath(root, contested.Path)); err == nil &&
		vaultsync.HashBytes(data) == branch.SHA256 {
		return data
	}
	if data := readRetained(root, packageID, branch); data != nil {
		return data
	}
	return nil
}
