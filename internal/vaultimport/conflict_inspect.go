package vaultimport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

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
