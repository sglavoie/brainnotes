package vaultexport

import (
	"fmt"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// rivalKind says why two paths cannot both exist on a replica that folds names.
type rivalKind int

const (
	// rivalName is two paths the replica reads as the same file.
	rivalName rivalKind = iota
	// rivalInside is a path this device sent from inside a directory whose
	// folded name is the file the outgoing path sends.
	rivalInside
	// rivalAncestor is a file this device sent under a name the outgoing path
	// needs as one of its directories. It is the one kind whose rival has to
	// still be a *file* to be a clash: a name that was a file and is now a
	// directory is the legitimate case, not the refused one.
	rivalAncestor
)

// stillClashes reports whether a rival the commit still holds as this kind of
// object is a real clash rather than the name's legitimate successor.
func (k rivalKind) stillClashes(kind string) bool {
	if kind == "" {
		return false
	}
	// A rivalAncestor whose name now holds a directory is the file having been
	// replaced by the very folder the outgoing path needs — the tombstone travels
	// first and the peer ends up with one directory. Every other kind is a clash
	// whenever the name is occupied at all, because a directory occupies a name
	// as thoroughly as a file does.
	return k != rivalAncestor || kind != vaultgit.KindTree
}

// rival is a path this device already sent that an outgoing path cannot join on
// the replica, and the reason it cannot.
type rival struct {
	path string
	kind rivalKind
}

// collisionIndex answers "what would the replica already have under this name?"
//
// It is vaultsync's manifestScan rule restated over the path ledger rather than
// over one manifest. Two indexes rather than one, because a name can be occupied
// two ways and only one of them is a key match: `files` holds the paths sent
// under a folded name, and `dirs` holds, under each folded directory name, the
// sent paths that need that directory to exist. `Archive` is a key in the first
// and `archive/notes.md` puts `archive` in the second, and neither would ever
// find the other by comparing whole paths.
type collisionIndex struct {
	files map[string][]string
	dirs  map[string][]string
}

// newCollisionIndex indexes everything the path ledger says has been sent.
//
// The ledger is walked in sorted order so a path with two rivals names the same
// one every time. Map iteration order would otherwise reach into the refusal
// message itself, and a halt that phrases itself differently on each run is a
// halt nobody can match against the last one.
func newCollisionIndex(ledger map[string]string) *collisionIndex {
	sent := make([]string, 0, len(ledger))
	for path := range ledger {
		sent = append(sent, path)
	}
	sort.Strings(sent)

	index := &collisionIndex{
		files: make(map[string][]string, len(sent)),
		dirs:  make(map[string][]string, len(sent)),
	}
	for _, path := range sent {
		index.add(path)
	}
	return index
}

// add records one sent path: the name it occupies, and every directory it needs.
func (c *collisionIndex) add(path string) {
	key := vaultsync.CollisionKey(path)
	c.files[key] = append(c.files[key], path)
	for _, dir := range ancestors(path) {
		dirKey := vaultsync.CollisionKey(dir)
		c.dirs[dirKey] = append(c.dirs[dirKey], path)
	}
}

// rivals reports every indexed path that cannot coexist with this one, in the
// order they were indexed.
//
// Every match is reported rather than the first, because the caller has to ask
// the history whether each one is still live: a rival the commit no longer holds
// is a tombstone the peer applies first, and stopping at it would hide a live
// rival behind a dead one.
func (c *collisionIndex) rivals(path string) []rival {
	// The path itself is never its own rival. A path sent again is an update, and
	// the ledger holds exactly that string for it.
	seen := map[string]bool{path: true}
	var found []rival

	key := vaultsync.CollisionKey(path)
	found = appendRivals(found, seen, c.files[key], rivalName)
	// This path as a file against everything already sent from inside it.
	found = appendRivals(found, seen, c.dirs[key], rivalInside)
	// Every directory this path needs, against everything already sent as a file
	// under that name.
	for _, dir := range ancestors(path) {
		found = appendRivals(found, seen, c.files[vaultsync.CollisionKey(dir)], rivalAncestor)
	}
	return found
}

// appendRivals adds each not-yet-reported candidate under one kind.
func appendRivals(found []rival, seen map[string]bool, candidates []string, kind rivalKind) []rival {
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		found = append(found, rival{path: candidate, kind: kind})
	}
	return found
}

// ancestors lists every directory a portable path needs, outermost first.
//
// Portable paths are slash-separated by definition — vaultsync's wire form, not
// this machine's — so this counts separators rather than calling filepath.
func ancestors(path string) []string {
	var dirs []string
	for i, r := range path {
		if r == '/' {
			dirs = append(dirs, path[:i])
		}
	}
	return dirs
}

// collisionRefusal names both paths and asks for a rename.
//
// Both, because the remedy is a decision about the vault rather than about the
// export record: the clash is in a commit that already happened, so no amount of
// editing at the tip clears it, and the person clearing it has to know which two
// files they are choosing between. (Reset does drop the path ledger, but it
// re-baselines from the current tree — so if both names are still live there the
// baseline refuses in turn, and if they are not, the peer is left holding the
// name this device sent it earlier.) It reuses
// vaultsync.ErrPathCollision because the condition and the remedy are the same
// ones that pair gets inside a single manifest; a second sentinel would make one
// of the two look like a different problem.
func collisionRefusal(commit, path string, r rival) error {
	if r.kind == rivalName {
		return fmt.Errorf("%w: %s sends %q, which this device already sends as %q; rename one of them",
			vaultsync.ErrPathCollision, commit, path, r.path)
	}
	return fmt.Errorf("%w: %s sends %q, and this device already sends %q, where one of those names has to be a directory and the other a file; rename one of them",
		vaultsync.ErrPathCollision, commit, path, r.path)
}
