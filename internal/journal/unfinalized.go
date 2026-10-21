package journal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// An entry directory exists from the first Capture and carries a manifest only
// once Finalize has written one. Everything in this file is about that window.
//
// Ordinary callers never see it: a mutation that reaches Finalize closes it, and
// one that does not is a mutation whose own error path compensated the change
// away. It is visible only to a caller recovering a *process* that died inside
// it, which is the one situation where the compensation never ran — and such a
// caller has to be able to ask what the half-built entry holds and then remove
// it, neither of which the recorder can offer, because the recorder is the thing
// that died.

// Finalized reports whether entry id carries a manifest, which is the one
// durable fact that separates a recoverable entry from a recorded one.
//
// It is a question about the file rather than about its contents. LoadEntry
// answers "can this entry be used", and conflates an absent manifest with an
// unreadable one; a caller deciding whether an interrupted operation ever
// reached its journal needs those kept apart, because the first means the
// operation can be undone by hand and the second means the disk is damaged.
func Finalized(journalRoot, id string) (bool, error) {
	dir, err := entryDir(journalRoot, id)
	if err != nil {
		return false, fmt.Errorf("journal: %w", err)
	}
	manifest := filepath.Join(dir, ManifestFileName)
	switch _, err := os.Stat(manifest); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("journal: check %q: %w", manifest, err)
	}
}

// Started reports whether entry id has a directory at all, which is the durable
// fact that separates an operation that touched nothing from one that did.
//
// The directory is created by the first Capture, and vault.FileTxn.Commit
// captures every path *before* it applies any of them — so an entry that does
// not exist proves no destination was written, whatever the paths happen to hold
// now. That is a stronger statement than inspecting the paths, and it is the one
// a recovering caller needs: inspecting the paths can only compare them against
// what some record claimed they held, and a record whose claim was wrong is
// exactly the reason the operation was refused before it started.
//
// Together with Finalized this makes three states out of one identity: no
// directory (nothing happened), a directory without a manifest (something
// happened and can be undone), and a manifest (it is recovery history).
func Started(journalRoot, id string) (bool, error) {
	dir, err := entryDir(journalRoot, id)
	if err != nil {
		return false, fmt.Errorf("journal: %w", err)
	}
	switch _, err := os.Stat(dir); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("journal: check %q: %w", dir, err)
	}
}

// ReadSnapshot returns the bytes Capture stored for relSlash under entry id —
// what the path held before the operation touched it.
//
// This is how a caller undoes an entry the manifest never covered. journal.
// Restore is the supported way to reverse an operation and it reads the manifest
// to learn which paths to put back; an entry that never finalized has no such
// list, so the caller supplies the path itself, out of whatever record it made
// durable before the mutation started.
//
// A path whose pre-change state was not a regular file has no snapshot and
// reports fs.ErrNotExist. That is a real answer rather than a failure: the way
// to restore a path that was missing is to remove it, and the caller knows which
// case it is from the same record it took relSlash from.
func ReadSnapshot(journalRoot, id, relSlash string) ([]byte, error) {
	path, err := snapshotPath(journalRoot, id, relSlash)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("journal: read snapshot for %q in entry %s: %w", relSlash, id, err)
	}
	return data, nil
}

// snapshotPath locates one captured snapshot inside an entry directory.
//
// Containment does not rest on the path being checked, though it is checked: the
// name on disk is the digest of relSlash, so no relative path can address
// anything but a file directly inside the snapshots directory. The rejection of
// an escaping name is there so a caller that passed the wrong thing is told,
// rather than being handed a confident answer about a snapshot that cannot exist.
func snapshotPath(journalRoot, id, relSlash string) (string, error) {
	dir, err := entryDir(journalRoot, id)
	if err != nil {
		return "", fmt.Errorf("journal: %w", err)
	}
	relSlash = filepath.ToSlash(strings.TrimSpace(relSlash))
	if relSlash == "" {
		return "", fmt.Errorf("journal: empty snapshot path in entry %s", id)
	}
	if isEscapingRel(relSlash) {
		return "", fmt.Errorf("journal: snapshot path %q escapes vault root", relSlash)
	}
	return filepath.Join(dir, SnapshotsDirName, snapshotFileName(relSlash)), nil
}

// DiscardUnfinalized removes the entry directory of an operation that never
// wrote a manifest, and refuses to touch one that did.
//
// The refusal is the whole point of the function existing rather than the caller
// calling os.RemoveAll. A finalized entry is recovery history — it is what
// authorizes committing a deletion and what `bn undo` reads — and the only thing
// allowed to remove one is Prune, under a retention policy. An unfinalized one is
// the opposite: nothing can read it, nothing can undo it, and nothing will ever
// prune it, because listing skips a directory with no manifest. Leaving it is not
// neutral either, since its snapshots sit inside the content history's scope and
// show up as pending additions forever.
//
// An entry that is not there is not an error. The caller is converging on a state
// where it is gone, and it being gone already is that state.
func DiscardUnfinalized(journalRoot, id string) error {
	dir, err := entryDir(journalRoot, id)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	finalized, err := Finalized(journalRoot, id)
	if err != nil {
		return err
	}
	if finalized {
		return fmt.Errorf("journal: entry %s is finalized and is recovery history, not scratch", id)
	}
	// The same guard the write that produced these snapshots went through. A
	// removal is a mutation, and this one deletes the only copy of pre-change
	// content.
	if err := vault.GuardMutation("", dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("journal: discard unfinalized entry %s: %w", id, err)
	}
	return nil
}
