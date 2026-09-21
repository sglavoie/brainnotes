package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// ConflictKind names why one path could not be applied, in the vocabulary a
// person resolving it thinks in rather than in the transaction's.
//
// internal/vault answers one question about a path — does it hold what the
// package expected — and that answer is the same word for six situations whose
// remedies have nothing in common. Re-creating a note the peer moved somewhere
// else is a different act from merging two edits, and picking a declarations
// file is a different act again because it governs notes that are not in the
// package at all. The kind is what lets a resolution offer the right choice, and
// it is recorded rather than recomputed because the disk moves on.
type ConflictKind string

const (
	// ConflictConcurrentEdit is the ordinary one: both replicas changed the same
	// note from the same starting point.
	ConflictConcurrentEdit ConflictKind = "concurrent-edit"
	// ConflictDeleteEdit is the peer removing a note this vault has since
	// changed. Applying the tombstone would delete an edit nothing else holds.
	ConflictDeleteEdit ConflictKind = "delete-edit"
	// ConflictEditDelete is the mirror: the peer changed a note this vault no
	// longer has. Applying the write would resurrect a note somebody deleted on
	// purpose.
	ConflictEditDelete ConflictKind = "edit-delete"
	// ConflictDivergentRename is an operation moving a note this vault no longer
	// holds at the source, to a name that is free here.
	//
	// The signature is the whole of what is knowable without a scan: the source
	// is gone and the destination is empty, so this vault moved or removed that
	// note for a reason the package cannot see. Writing the content under the
	// peer's chosen name would leave two copies of one note if this vault renamed
	// it elsewhere, and resurrect it if this vault deleted it — and the two are
	// indistinguishable from here, which is exactly why this is held for a person
	// rather than decided.
	ConflictDivergentRename ConflictKind = "divergent-rename"
	// ConflictFilenameCollision is a name this vault already holds under a
	// different spelling that is the same file under the portable collision rule,
	// or a directory standing where the package expects a note.
	//
	// It is the one conflict a case-sensitive replica cannot see by looking at
	// the path it was given, and the one that does real damage if it is missed:
	// applying it creates a second file here that is one file on the other
	// replica, and the pair then disagrees about how many notes exist.
	ConflictFilenameCollision ConflictKind = "filename-collision"
	// ConflictConfiguration is a change to the vault's portable declarations that
	// this vault has also changed.
	//
	// Separated from a concurrent edit because the stakes are not the file's: the
	// declarations decide which properties exist and what a note must carry, so
	// taking either side wholesale re-governs every note in the vault. A merge is
	// the only honest resolution, and a resolution that could not tell this from
	// a note would offer "keep mine" as though it were cheap.
	ConflictConfiguration ConflictKind = "configuration"
)

// Branch is one side of a conflict: what a path holds, and where the bytes were
// kept.
//
// The hash is always recorded and the bytes are kept whenever they exist, which
// is the distinction that makes this record honest about its own limits. A
// branch whose bytes this vault never had — a baseline that predates the content
// history, most of all — is still named exactly, so a person can recognise it if
// it turns up; it just cannot be shown.
type Branch struct {
	// Exists says whether this branch has content at all. A deletion on either
	// side is a branch, and it is not the same as an unknown one.
	Exists bool `json:"exists"`
	// SHA256 is the content digest, empty when the branch is a deletion.
	SHA256 string `json:"sha256,omitempty"`
	// Bytes names the retained copy inside this package's conflict directory,
	// empty when the bytes could not be kept. It is the digest, because the store
	// is content-addressed: re-detecting the same conflict rewrites identical
	// bytes rather than accumulating copies.
	Bytes string `json:"bytes,omitempty"`
}

// PathConflict is one contested path with every version of it that exists.
type PathConflict struct {
	// Path is the vault-relative path the package names.
	Path string `json:"path"`
	// Kind is why it could not be applied.
	Kind ConflictKind `json:"kind"`
	// Remote is what the package would have installed.
	Remote Branch `json:"remote"`
	// Local is what this vault holds now.
	Local Branch `json:"local"`
	// Baseline is what the package expected to find — the last state both
	// replicas agreed on, and the thing a three-way merge needs.
	Baseline Branch `json:"baseline"`
	// RenamedTo is the destination the operation moves Path to, set only for
	// ConflictDivergentRename.
	RenamedTo string `json:"renamed_to,omitempty"`
	// Collides is the name already in this vault that Path would collide with,
	// set only for ConflictFilenameCollision.
	Collides string `json:"collides,omitempty"`
}

// ConflictRecord is one held operation: every contested path in it, and the
// identity of the package it is still waiting in the channel as.
//
// It is durable because holding has to survive the process that noticed. The
// package stays in the incoming channel and the peer, having had no receipt,
// still holds its own copy — but neither of those says *why* nothing is
// happening, and neither keeps the local branch, which is the one version of the
// content that exists nowhere else.
//
// It is deliberately not an Attempt. A held operation has touched nothing, so
// recording it as pending would make every later import refuse — the opposite of
// what this session is for. It is re-evaluated on every pass instead, and clears
// itself the moment the disk stops disagreeing.
type ConflictRecord struct {
	// PackageID is the package being held.
	PackageID string `json:"package_id"`
	// OperationID is the operation it carries. A later package naming this
	// operation as a causal predecessor is a dependent and is held with it.
	OperationID string `json:"operation_id"`
	// OriginDevice is the peer that produced it.
	OriginDevice string `json:"origin_device"`
	// Sequence is its position in that peer's stream.
	Sequence uint64 `json:"sequence"`
	// DetectedAt is when the hold was first recorded, from the caller's clock.
	DetectedAt time.Time `json:"detected_at"`
	// Paths are the contested paths in package order.
	Paths []PathConflict `json:"paths"`
}

func (c *ConflictRecord) validate(packageID string) error {
	switch {
	case c == nil:
		return fmt.Errorf("%w: held package %s has no record", ErrCorruptState, packageID)
	case c.PackageID != packageID:
		return fmt.Errorf("%w: held package %s carries a record for %s",
			ErrCorruptState, packageID, c.PackageID)
	case len(c.Paths) == 0:
		return fmt.Errorf("%w: held package %s names no contested path", ErrCorruptState, packageID)
	}
	return nil
}

// conflictsName is the retained-branch store inside the synchronization state
// directory, beside the import record and out of the content history's scope for
// the same reason: a copy of a note that is not the note must never be committed
// as one.
const conflictsName = "conflicts"

// conflictsRoot is the store itself, derived from vaultsync.StateDir for the
// reason StatePath is: a literal `.bnotes/sync/` beside a derived one would let
// the two drift apart.
func conflictsRoot(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), conflictsName)))
}

// conflictDir is where one held package's retained branches live. The identity
// is checked before it is joined onto a directory, the rule internal/vaultsync
// states for every path derived from one: containment rests on the name being a
// checked identity, and an unchecked one is refused with an empty path rather
// than reaching the filesystem.
func conflictDir(root, packageID string) (string, error) {
	if !vaultsync.PackageName(packageID) {
		return "", fmt.Errorf("%w: %q is not a package identity", ErrCorruptState, packageID)
	}
	return filepath.Join(conflictsRoot(root), packageID), nil
}

// verdict is what inspecting one change decided.
type verdict int

const (
	// verdictApply means the path holds what the package expected.
	verdictApply verdict = iota
	// verdictConverged means it already holds what the package installs.
	verdictConverged
	// verdictConflict means it holds neither.
	verdictConflict
)

// disposition is one package's changes sorted into those three answers.
type disposition struct {
	// converged are the paths already at the result, by path.
	converged map[string]bool
	// conflicts are the contested paths in package order.
	conflicts []PathConflict
	// staged counts the changes that would actually be written.
	staged int
}

// settled reports a package that asks for nothing this vault has not already
// done. It is distinct from an empty package, which Manifest.Validate refuses.
func (d *disposition) settled() bool { return d.staged == 0 && len(d.conflicts) == 0 }

// refusal is the error a held package is reported as. It names every contested
// path with its kind, because the remedy differs per kind and a caller that only
// saw "conflict" would have to re-derive them.
func (d *disposition) refusal(packageID string) error {
	names := make([]string, 0, len(d.conflicts))
	for _, contested := range d.conflicts {
		names = append(names, fmt.Sprintf("%s (%s)", contested.Path, contested.Kind))
	}
	return fmt.Errorf("%w: %s is held: %s; both branches and the baseline are kept and the package stays in the channel",
		ErrConflict, packageID, strings.Join(names, ", "))
}

// inspect decides every change against this disk, before the transaction exists.
//
// It runs here rather than being read off a failed commit, and that ordering is
// the whole of the guarantee. A transaction reports the first precondition that
// failed and has nothing to say about the rest, so a conflict discovered there
// could only ever hold one path of an operation that contests three. More
// importantly it reports *that* a path disagreed and never *how*, and the how is
// what a resolution acts on. Deciding first also means the contested content is
// never staged over, which is what lets the local branch be read at leisure
// rather than rescued from a rollback.
//
// internal/vault still checks every one of these again under the path's own
// lock, and that is not redundancy: this reads the disk without holding
// anything, so a package that passes here can still meet an edit that landed in
// between. This decides *which* refusal, and the transaction remains the thing
// that decides whether a write is allowed to happen. See applier.retain for the
// race's other end.
func (a *applier) inspect() (*disposition, error) {
	states, err := a.diskStates()
	if err != nil {
		return nil, err
	}
	scan := &inspection{
		states:  states,
		changes: changeIndex(a.operation),
		renames: renamePairs(a.operation),
		dirs:    map[string][]string{},
		root:    a.root,
	}
	found := &disposition{converged: make(map[string]bool, len(a.changes))}
	for _, planned := range a.changes {
		contested, err := scan.judge(planned.change)
		if err != nil {
			return nil, err
		}
		switch {
		case contested != nil:
			found.conflicts = append(found.conflicts, *contested)
		case states[planned.change.Path] == afterState(planned.change):
			found.converged[planned.change.Path] = true
		default:
			found.staged++
		}
	}
	return found, nil
}

// diskStates reads every path the operation names once, because the rename rule
// asks about a path other than the one it is judging.
func (a *applier) diskStates() (map[string]pathState, error) {
	states := make(map[string]pathState, len(a.changes))
	for _, planned := range a.changes {
		state, err := diskState(a.abs(planned.change.Path))
		if err != nil {
			return nil, err
		}
		states[planned.change.Path] = state
	}
	return states, nil
}

// inspection is the per-package context judging one change needs.
type inspection struct {
	states  map[string]pathState
	changes map[string]vaultsync.Change
	renames map[string]string
	// dirs caches one listing per destination directory, since a package
	// typically changes several notes in one folder and the collision question is
	// about the folder rather than the file.
	dirs map[string][]string
	root string
}

// judge returns the conflict one change raises, or nil when it may proceed.
func (s *inspection) judge(change vaultsync.Change) (*PathConflict, error) {
	state := s.states[change.Path]
	collides, err := s.collision(change, state)
	if err != nil {
		return nil, err
	}
	kind, contested := s.classify(change, state, collides)
	if !contested {
		return nil, nil
	}
	return &PathConflict{
		Path:      change.Path,
		Kind:      kind,
		Remote:    remoteBranch(change),
		Local:     localBranch(state),
		Baseline:  baselineBranch(change),
		RenamedTo: s.renames[change.Path],
		Collides:  collides,
	}, nil
}

// classify is the decision itself, with the disk already read.
//
// The order of the arms is the rule. A collision comes first because it is true
// whatever the expectation says — the package can be perfectly right about the
// path it named and still be about to create a duplicate — and a name occupied
// by something that is not a note comes with it, since the remedy is the same
// act of clearing the name. The declarations file comes next, ahead of the
// content comparison it would otherwise be reported by, because its remedy is
// not a choice between two files. Only then do the three content answers apply,
// and the last of them separates the two asymmetric ones — a deletion meeting an
// edit, and an edit meeting a deletion — from a plain concurrent edit.
func (s *inspection) classify(change vaultsync.Change, state pathState, collides string) (ConflictKind, bool) {
	switch {
	case collides != "":
		return ConflictFilenameCollision, true
	case state.present && !state.regular:
		return ConflictFilenameCollision, true
	case state == beforeState(change):
		return "", false
	case state == afterState(change):
		return s.converged(change)
	case isConfiguration(change.Path):
		return ConflictConfiguration, true
	case change.Tombstone():
		return ConflictDeleteEdit, true
	case !state.present:
		return ConflictEditDelete, true
	default:
		return ConflictConcurrentEdit, true
	}
}

// converged decides a change whose path already holds the result.
//
// It is the identical-outcome case and is almost always exactly that: both
// replicas made the same edit, and there is nothing to do. The exception is the
// source of a rename, where "already at the result" means only that the note is
// gone — which is equally what this vault moving it somewhere else looks like.
// So a rename source is judged against its destination.
//
// The question asked of the destination is whether anything is there at all,
// not whether it holds the peer's bytes, and that is the whole of what
// ConflictDivergentRename is defined by: the source gone and the destination
// free. A destination holding *different* content is a real disagreement, but it
// is the destination's own change that says so — reporting the source as a
// divergent rename as well would hold one operation twice for one event, and
// tell a person this vault moved a note somewhere unknown while the note is
// plainly sitting at the name the package chose.
//
// The destination is always in states, and that is a fact about where renames
// come from rather than a lookup that might miss: renamePairs derives a
// destination only from a creation in this same operation, and diskStates reads
// every path the operation names. A miss would answer "absent" and report a
// divergent rename over a name this vault may well be holding.
func (s *inspection) converged(change vaultsync.Change) (ConflictKind, bool) {
	destination, renamed := s.renames[change.Path]
	if !renamed {
		return "", false
	}
	if s.states[destination].present {
		return "", false
	}
	return ConflictDivergentRename, true
}

// collision reports a name this vault already holds that is the same file as the
// change's path under the portable collision rule, spelled differently.
//
// It only asks when the path itself resolves to nothing, and that condition is
// what keeps it from firing on the replica it matters least to. A
// case-insensitive or normalization-insensitive filesystem answers the original
// path with the colliding file, so the ordinary content comparison has already
// seen it and there is nothing here to add. A case-sensitive one answers
// "absent" and would happily create the second file — which is one file on the
// other replica, and the pair would then disagree about how many notes exist.
//
// Tombstones are exempt: a delete that finds nothing at its path is judged by
// its expectation like any other change, and a similarly-named neighbour is not
// evidence about the note being removed.
func (s *inspection) collision(change vaultsync.Change, state pathState) (string, error) {
	if change.Tombstone() || state.present {
		return "", nil
	}
	dir, base := path.Split(change.Path)
	dir = strings.TrimSuffix(dir, "/")
	names, err := s.listing(dir)
	if err != nil {
		return "", err
	}
	key := vaultsync.CollisionKey(base)
	for _, name := range names {
		if name != base && vaultsync.CollisionKey(name) == key {
			return path.Join(dir, name), nil
		}
	}
	return "", nil
}

// listing returns the names in one vault-relative directory, cached, with a
// directory that does not exist reported as empty.
func (s *inspection) listing(dir string) ([]string, error) {
	if names, cached := s.dirs[dir]; cached {
		return names, nil
	}
	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(dir)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("vaultimport: list %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	s.dirs[dir] = names
	return names, nil
}

// isConfiguration reports a path that is one of the vault's configuration
// layers, compared through the collision rule for the reason applier.plan
// compares that way: a folding replica writes one file under either spelling.
//
// The machine-local overlay cannot reach here today — vaultsync.Classify puts it
// in the private scope, so no package carries it — and it is named anyway. The
// alternative is a predicate that is true of one configuration layer and silently
// false of the other, which would be wrong the day the portable scope changes and
// wrong in a way nothing would notice.
func isConfiguration(rel string) bool {
	key := vaultsync.CollisionKey(rel)
	return key == vaultsync.CollisionKey(config.FileName) ||
		key == vaultsync.CollisionKey(config.LocalFileName)
}

// renamePairs returns the rename destinations one operation carries, by source
// path.
//
// A rename reaches the wire as a tombstone and a creation, because that is what
// it is on both disks; the pairing is recovered here rather than carried in the
// package, since a producer that labelled it would be making a claim a consumer
// could not check. The signature is exact: a tombstone whose content is created,
// in the same operation, at a path the producer believed was free. Two notes
// that merely happen to hold identical bytes cannot fake it, because the
// operation would have to delete one and create the other as one event.
func renamePairs(operation *vaultsync.Operation) map[string]string {
	creations := uniqueCreations(operation)
	pairs := make(map[string]string, len(creations))
	for _, change := range operation.Changes {
		if !change.Tombstone() || !change.ExpectExists {
			continue
		}
		if destination, moved := creations[change.ExpectSHA256]; moved {
			pairs[change.Path] = destination
		}
	}
	return pairs
}

// uniqueCreations maps the content one operation creates to the single path it
// creates it at, dropping any digest created at more than one path.
//
// The drop is the point. A rename is recovered by matching a tombstone's content
// to a creation of the same content, and an operation that creates identical
// bytes at two names — a note moved and also duplicated in one commit — makes
// that match a coin toss decided by the manifest's ordering. An unpaired
// tombstone is judged as the deletion it plainly is, which is the answer that is
// merely incomplete rather than the answer that is wrong about which note moved
// where.
func uniqueCreations(operation *vaultsync.Operation) map[string]string {
	creations := make(map[string]string, len(operation.Changes))
	duplicated := make(map[string]struct{})
	for _, change := range operation.Changes {
		if change.Tombstone() || change.ExpectExists {
			continue
		}
		if _, seen := creations[change.ResultSHA256]; seen {
			duplicated[change.ResultSHA256] = struct{}{}
			continue
		}
		creations[change.ResultSHA256] = change.Path
	}
	for digest := range duplicated {
		delete(creations, digest)
	}
	return creations
}

func changeIndex(operation *vaultsync.Operation) map[string]vaultsync.Change {
	index := make(map[string]vaultsync.Change, len(operation.Changes))
	for _, change := range operation.Changes {
		index[change.Path] = change
	}
	return index
}

// remoteBranch is what the package would have installed.
func remoteBranch(change vaultsync.Change) Branch {
	if change.Tombstone() {
		return Branch{}
	}
	return Branch{Exists: true, SHA256: change.ResultSHA256}
}

// localBranch is what this vault holds. A path occupied by something that is not
// a regular file exists and has no content, which is exactly the shape Branch
// records.
func localBranch(state pathState) Branch {
	if !state.present {
		return Branch{}
	}
	return Branch{Exists: true, SHA256: state.sha256}
}

// baselineBranch is the state both replicas last agreed on, which is what the
// package expected to find.
func baselineBranch(change vaultsync.Change) Branch {
	if !change.ExpectExists {
		return Branch{}
	}
	return Branch{Exists: true, SHA256: change.ExpectSHA256}
}

// hold records the conflict durably and keeps every version of every contested
// path that this vault can still reach.
//
// The state is re-read rather than taken from the session, because this is
// reached from two places and one of them has just been through a rollback: an
// applier that declared an attempt and had it resolved underneath it holds a
// record that still names the attempt, and writing that back would re-declare an
// application that is over. Reading again costs one file and removes the whole
// class.
func (a *applier) hold(found *disposition) error {
	state, err := loadState(a.root, a.local)
	if err != nil {
		return err
	}
	store := &retention{root: a.root, packageID: a.manifest.PackageID, payloads: a.payloadIndex()}
	paths := make([]PathConflict, 0, len(found.conflicts))
	for _, contested := range found.conflicts {
		kept, err := store.keep(contested)
		if err != nil {
			return err
		}
		paths = append(paths, kept)
	}
	next := state.clone()
	next.Conflicts[a.manifest.PackageID] = &ConflictRecord{
		PackageID:    a.manifest.PackageID,
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		Sequence:     a.manifest.Sequence,
		DetectedAt:   a.opts.now(),
		Paths:        paths,
	}
	if err := a.saveState(next); err != nil {
		return err
	}
	a.state = next
	return store.prune(paths)
}

// payloadIndex maps a content digest to the package bytes carrying it, which is
// what lets the remote branch — and, for a rename, the baseline — be retained
// without opening the package again.
func (a *applier) payloadIndex() map[string][]byte {
	index := make(map[string][]byte, len(a.changes))
	for _, planned := range a.changes {
		if planned.data != nil {
			index[vaultsync.HashBytes(planned.data)] = planned.data
		}
	}
	return index
}

// retention writes the contested content into this package's conflict directory.
//
// The store is content-addressed, so the three branches of a rename conflict —
// where the remote bytes and the baseline bytes are the same bytes — cost one
// file, and re-detecting an unchanged conflict on every pass rewrites the same
// names rather than growing the directory. Everything goes through
// vault.WriteStateFile, which creates the directories, guards the location, and
// fsyncs; nothing here opens or removes a file itself.
type retention struct {
	root      string
	packageID string
	payloads  map[string][]byte
	// history is opened once, lazily, and only when a baseline has to be looked
	// for in the content history.
	history *committed
}

// keep fills in where each of a conflict's three branches was retained.
func (s *retention) keep(contested PathConflict) (PathConflict, error) {
	remote, err := s.write(s.payloads[contested.Remote.SHA256], contested.Remote)
	if err != nil {
		return contested, err
	}
	local, err := s.write(s.read(contested.Path, contested.Local), contested.Local)
	if err != nil {
		return contested, err
	}
	baseline, err := s.write(s.baseline(contested), contested.Baseline)
	if err != nil {
		return contested, err
	}
	contested.Remote, contested.Local, contested.Baseline = remote, local, baseline
	return contested, nil
}

// write retains one branch's bytes, and is a no-op for a branch that is a
// deletion or whose bytes could not be found.
func (s *retention) write(data []byte, branch Branch) (Branch, error) {
	if !branch.Exists || data == nil {
		return branch, nil
	}
	if vaultsync.HashBytes(data) != branch.SHA256 {
		return branch, nil
	}
	dir, err := conflictDir(s.root, s.packageID)
	if err != nil {
		return branch, err
	}
	dest := filepath.Join(dir, branch.SHA256)
	if err := vault.WriteStateFile(dest, data, statePerm); err != nil {
		return branch, fmt.Errorf("vaultimport: retain %s for %s: %w", branch.SHA256, s.packageID, err)
	}
	branch.Bytes = branch.SHA256
	return branch, nil
}

// prune removes retained bytes this package's record has stopped naming.
//
// It runs after the record is saved, which is release's ordering one size
// smaller and holds for the same reason: removing first would invite a
// resolution from versions that are already gone, where removing after leaves,
// at worst, bytes nothing points at until the next pass over this package.
//
// It is needed because a hold is re-decided on every pass and the local branch
// is re-read from the vault each time. A person editing a contested note while
// they think about it would otherwise leave one dead copy of it per edit, inside
// a directory collectRetained will never touch — the record still names the
// package, so the sweep correctly skips it — and the only bound on that is how
// long the conflict stays unresolved.
//
// A failure here joins the refusal its caller is already raising, rather than
// going to a cleanup channel the way every other unremovable file in this
// package does. Both of hold's callers are refusing, so there is no Result to
// carry it on and the alternative is silence; the refusal still wraps
// ErrConflict, so a drain still classifies the package as held rather than
// failed. A caller that ever holds without refusing would need the other
// treatment.
func (s *retention) prune(kept []PathConflict) error {
	dir, err := conflictDir(s.root, s.packageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(s.root, dir); err != nil {
		return err
	}
	named := retainedNames(kept)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list retained branches for %s: %w", s.packageID, err)
	}
	var failures []error
	for _, entry := range entries {
		if _, still := named[entry.Name()]; still {
			continue
		}
		// Not vault content: one retained copy of a note inside this package's own
		// scratch directory under `.bnotes/sync`, which the content history and the
		// portable scope both exclude wholesale.
		//bn:direct-write removes one retained branch the state file written just above has stopped naming
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			failures = append(failures, fmt.Errorf("vaultimport: discard retained branch %s of %s: %w",
				entry.Name(), s.packageID, err))
		}
	}
	return errors.Join(failures...)
}

// retainedNames is the set of retained files one record names, which is what
// prune keeps.
func retainedNames(kept []PathConflict) map[string]struct{} {
	named := make(map[string]struct{}, len(kept)*3)
	for _, contested := range kept {
		for _, branch := range []Branch{contested.Remote, contested.Local, contested.Baseline} {
			if branch.Bytes != "" {
				named[branch.Bytes] = struct{}{}
			}
		}
	}
	return named
}

// read reads the local branch off the disk, reporting nothing rather than
// failing: the local side is the version that exists nowhere else, so a read
// that loses a race with the person editing it must not turn a held conflict
// into a refused import. write re-checks the digest, so bytes that changed under
// this are dropped rather than recorded as the branch they are not.
func (s *retention) read(rel string, branch Branch) []byte {
	if !branch.Exists {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return data
}

// readRetained reads one branch back out of the store, or reports nothing.
//
// It is how a resolution reaches a version that is no longer on the disk — the
// peer's, most of all, which exists here only as the copy the hold kept. The
// digest is checked against the branch rather than against the file name, so a
// record naming a retained copy that has since been replaced by something else
// answers nothing rather than the wrong content. Bytes being empty is the
// ordinary case for a branch that is a deletion or one this vault never had.
func readRetained(root, packageID string, branch Branch) []byte {
	if !branch.Exists || branch.Bytes == "" {
		return nil
	}
	dir, err := conflictDir(root, packageID)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, branch.Bytes))
	if err != nil || vaultsync.HashBytes(data) != branch.SHA256 {
		return nil
	}
	return data
}

// Version is how a surface outside this package reaches one version of a
// contested path.
//
// It exists because "show me both versions" cannot be answered from anywhere
// else. The peer's bytes live nowhere but the copy the hold kept, and this
// vault's own version is on the disk right up until somebody edits it, after
// which the retained copy is the only place the version the hold recorded still
// exists. Which of the two places answers is decided by the digest rather than
// by the caller, so passing the branch a resolution was planned against always
// shows the version that resolution is about — see branchContent, which is the
// one implementation of that rule and is also what the revision installs content
// from.
//
// It is a wrapper rather than a second reader on purpose: the digest check is the
// thing that makes either copy trustworthy, and a second implementation of it is
// a second chance to leave it out.
//
// Nothing is returned for a branch that is a deletion, one this vault never had,
// or one whose bytes are in neither place — Branch.Exists distinguishes the first
// two from the third, and a caller offering a merge has to treat "no bytes" as a
// version it cannot show rather than as an empty file.
func Version(root, packageID string, contested PathConflict, branch Branch) []byte {
	if !branch.Exists {
		return nil
	}
	return branchContent(root, packageID, contested, branch)
}

// baseline finds the bytes both replicas last agreed on, and is best-effort by
// construction.
//
// Two places can still hold them. The package itself does when the operation is
// a rename, since the content it creates is the content it moved. And the
// content history does whenever the local divergence is an uncommitted edit —
// which is the common case this session is about, because a committed local
// change would have been exported and the two would be arguing through
// predecessors rather than over a path.
//
// A baseline older than this vault's history, or one from a vault with no
// history at all, is recorded by digest and not by content. That is the honest
// answer: the identity is always kept, so a person who has the bytes elsewhere
// can recognise them, and a three-way merge that cannot find its base says so
// rather than merging against the wrong one.
func (s *retention) baseline(contested PathConflict) []byte {
	if !contested.Baseline.Exists {
		return nil
	}
	if data, carried := s.payloads[contested.Baseline.SHA256]; carried {
		return data
	}
	if s.history == nil {
		s.history = openCommitted(s.root)
	}
	return s.history.bytes(contested.Path, contested.Baseline.SHA256)
}

// committed is the content history read as a source of baseline bytes, opened
// once per held package and silent about every failure.
//
// Silent is deliberate and is the same judgement Result.Cleanup encodes. This
// runs while a conflict is being written down; a vault with no Git, a branch
// with no commits, or a tree listing that fails are all reasons the baseline
// cannot be shown, and none of them is a reason to refuse an import that was
// going to be held anyway.
type committed struct {
	ctx     context.Context
	history *vaultgit.History
	tip     string
	tree    map[string]vaultgit.TreeFile
	ready   bool
}

func openCommitted(root string) *committed {
	ctx := context.Background()
	source := &committed{ctx: ctx}
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{})
	if err != nil {
		return source
	}
	tip, err := history.Tip(ctx)
	if err != nil {
		return source
	}
	files, err := history.Tree(ctx, tip)
	if err != nil {
		return source
	}
	source.tree = make(map[string]vaultgit.TreeFile, len(files))
	for _, file := range files {
		source.tree[file.Path] = file
	}
	source.history, source.tip, source.ready = history, tip, true
	return source
}

// bytes returns the committed content of rel if it is the content sha256 names.
//
// The digest is checked before the bytes are read, which is what makes this
// affordable on a path whose committed version is not the baseline: BlobDigest
// hashes without holding the blob, so the common miss costs no memory.
func (c *committed) bytes(rel, sha256 string) []byte {
	if !c.ready {
		return nil
	}
	file, held := c.tree[rel]
	if !held {
		return nil
	}
	digest, _, err := c.history.BlobDigest(c.ctx, file.OID)
	if err != nil || digest != sha256 {
		return nil
	}
	blobs, err := c.history.Blobs(c.ctx, []string{file.OID})
	if err != nil {
		return nil
	}
	return blobs[file.OID]
}

// release drops a package's hold from the record a caller is building, and
// reports whether there was one to drop. The caller saves that record and then
// calls discard.
//
// The two are separate because the order between them is the guarantee. The
// record is what names the retained bytes, so it stops naming them first: a
// crash in between leaves files nothing points at, which the next hold on the
// same package overwrites and a sweep collects. The other order leaves a durable
// record naming content that is gone, which is a conflict a person is invited to
// resolve from versions that no longer exist.
func (a *applier) release(next *State) bool {
	if _, held := next.Conflicts[a.manifest.PackageID]; !held {
		return false
	}
	delete(next.Conflicts, a.manifest.PackageID)
	return true
}

// discard removes the branches a released hold had retained.
//
// The failure is reported rather than returned, because by the time this runs
// the package has been applied or has stopped conflicting — the vault is right
// either way, and refusing over a directory that is safe to delete would undo a
// real application. See Result.Cleanup.
func (a *applier) discard(released bool) {
	if !released {
		return
	}
	a.cleanup = errors.Join(a.cleanup, discardRetained(a.root, a.manifest.PackageID))
}

// discardRetained removes one package's retained branches. The caller must have
// already written the state that stopped naming them.
func discardRetained(root, packageID string) error {
	dir, err := conflictDir(root, packageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(root, dir); err != nil {
		return err
	}
	// Nothing in internal/vault removes a tree, and this is not vault content: it
	// is this package's own scratch directory under `.bnotes/sync`, which the
	// content history and the portable scope both exclude wholesale.
	//bn:direct-write removes only this package's own retained-branch directory, after the state file that named it was rewritten without it
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("vaultimport: discard retained branches for %s: %w", packageID, err)
	}
	return nil
}

// retain records the conflict behind a refusal the transaction raised rather
// than inspect.
//
// It is the other end of the race inspect documents. Every expectation is read
// again under its path's own lock, so an edit that lands between the inspection
// and the staging check is refused there — correctly, and with the contested
// content untouched, but with nothing written down about it. Inspecting again
// here recovers the record: the rollback has already run by the time this is
// called, so the disk is back to what it was and the second reading is about the
// same disagreement.
//
// A second inspection that finds nothing is not an error. It means the edit that
// caused the refusal was itself undone in the meantime, and the package is
// simply refused with no hold — which the next delivery or the next drain will
// apply.
func (a *applier) retain(cause error) error {
	if !errors.Is(cause, ErrConflict) {
		return cause
	}
	found, err := a.inspect()
	if err != nil {
		return errors.Join(cause, err)
	}
	if len(found.conflicts) == 0 {
		return cause
	}
	if err := a.hold(found); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
