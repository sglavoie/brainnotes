package vaultimport

import (
	"fmt"
	"os"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

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
