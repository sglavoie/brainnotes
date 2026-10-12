package vaultimport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// resolutionsName is the staged-revision store inside the synchronization state
// directory, beside the retained branches and out of the content history's
// scope for the same reason: content that has not been decided on yet must
// never be committed as though it had.
const resolutionsName = "resolutions"

// resolutionsRoot is the store itself, derived from vaultsync.StateDir for the
// reason StatePath is: a literal `.bnotes/sync/` beside a derived one would let
// the two drift apart.
func resolutionsRoot(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), resolutionsName)))
}

// resolutionDir is where one resolution's staged content lives. The identity is
// checked before it is joined onto a directory, the rule internal/vaultsync
// states for every path derived from one.
func resolutionDir(root, packageID string) (string, error) {
	if !vaultsync.PackageName(packageID) {
		return "", fmt.Errorf("%w: %q is not a package identity", ErrCorruptState, packageID)
	}
	return filepath.Join(resolutionsRoot(root), packageID), nil
}

// stageRevision writes every byte the revision will install, before the
// revision is promised.
//
// It is what makes the second commit recoverable at all. The content a
// resolution installs is not in the package and is not necessarily on the disk
// — a merge exists only in the caller's memory, and this vault's own version is
// about to be overwritten by the application the revision sits on top of. So it
// is saved first, content-addressed the way the retained branches are, and the
// re-drive reads it back rather than re-deriving a choice nobody is there to
// make again.
func stageRevision(root string, held *ConflictRecord, record *Plan) error {
	dir, err := resolutionDir(root, record.PackageID)
	if err != nil {
		return err
	}
	for _, step := range record.steps() {
		if step.Action != vaultsync.ActionWrite {
			continue
		}
		data, err := record.content(root, held, step.SHA256)
		if err != nil {
			return err
		}
		if err := vault.WriteStateFile(filepath.Join(dir, step.SHA256), data, statePerm); err != nil {
			return fmt.Errorf("vaultimport: stage revision content %s for %s: %w",
				step.SHA256, record.PackageID, err)
		}
	}
	return nil
}

// stagedContent reads back one step's content.
//
// The digest is verified rather than trusted, because this is the last reading
// before the bytes become a commit. The store is written by this package alone
// and lives where nothing else looks, so a mismatch means the file was damaged
// rather than replaced — which is exactly the case where installing it would
// put content in the history that nobody chose.
func stagedContent(root, packageID, digest string) ([]byte, error) {
	dir, err := resolutionDir(root, packageID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, digest))
	// Only content that is *gone* says the decision can never be carried out, and
	// only that reading may reach unkeepable — which abandons the promise and
	// then deletes this very store. Everything else a read can fail with is a
	// condition of the moment rather than of the decision: a descriptor limit, a
	// network mount, a backup tool holding the file open. Abandoning on one of
	// those would destroy a merge somebody typed because the machine was busy
	// for a second, so it is returned as itself and read again on the next pass.
	//
	// "Gone" is three answers, not one. ENOENT is the ordinary one. ENOTDIR says
	// a directory on the way to it is a file now, and EISDIR says the file itself
	// is a directory now — neither maps to fs.ErrNotExist, and both mean the
	// bytes are as absent as ENOENT says they are while being permanent in the
	// same way. Leaving those two out would answer a destroyed store by refusing
	// every import in the vault forever instead of dropping the one decision it
	// cost.
	if err != nil {
		if gone(err) {
			return nil, fmt.Errorf("%w: the revision content %s of %s is gone: %v",
				ErrResolutionUnavailable, digest, packageID, err)
		}
		return nil, fmt.Errorf("vaultimport: read the revision content %s of %s: %w", digest, packageID, err)
	}
	if vaultsync.HashBytes(data) != digest {
		return nil, fmt.Errorf("%w: the staged revision content %s of %s is not what it is named",
			ErrCorruptState, digest, packageID)
	}
	return data, nil
}

// gone reports a read failure that says the bytes are not there, as against one
// that says this machine could not get at them just now. See stagedContent for
// why the difference decides whether somebody's decision survives.
//
// Only ENOENT reaches fs.ErrNotExist — syscall.Errno.Is maps that sentinel and
// no other — so the three named errnos are load-bearing rather than belt and
// braces. Each is a permanent statement about the path this vault chose and
// wrote itself: a component of it is a file (ENOTDIR), the digest-named file is
// a directory (EISDIR), or something on the way is a symlink cycle (ELOOP).
// None of them clears on a retry, and a permanent failure that is not on this
// list stops every import, drain, resolution and recovery in the vault forever,
// because finishResolutions runs ahead of all four.
//
// EACCES is deliberately not here. A permission that denies this process may be
// a permission somebody can give back, and the bytes are still on the disk in
// the meantime; treating that as gone would throw a decision away over a
// recoverable mistake. ENAMETOOLONG is not here either — a path this long is a
// property of the vault's own root rather than of one decision, so it would
// fail identically for every package and is not a fact about this one.
func gone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, syscall.ENOTDIR) ||
		errors.Is(err, syscall.EISDIR) ||
		errors.Is(err, syscall.ELOOP)
}

// pruneStaged removes staged content this resolution's record has stopped
// naming, and runs after that record is saved for the reason retention.prune
// does: removing first would invite a re-drive to read content that is already
// gone.
//
// It is needed because one package can be resolved more than once. A resolution
// that is abandoned — its application refused, or rolled back after a crash —
// leaves its content behind, and the next attempt at the same package stages
// different bytes under a different name. Without this, a package somebody
// resolves three different ways keeps all three.
func pruneStaged(root string, record *Plan) error {
	dir, err := resolutionDir(root, record.PackageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(root, dir); err != nil {
		return err
	}
	named := make(map[string]struct{}, len(record.Paths))
	for _, step := range record.steps() {
		if step.Action == vaultsync.ActionWrite {
			named[step.SHA256] = struct{}{}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list staged revision content for %s: %w", record.PackageID, err)
	}
	return removeUnnamed(dir, record.PackageID, entries, named)
}

// removeUnnamed is the removal itself, separated so the marker below covers one
// loop rather than a function that also reads state.
func removeUnnamed(dir, packageID string, entries []os.DirEntry, named map[string]struct{}) error {
	var failures []error
	for _, entry := range entries {
		if _, still := named[entry.Name()]; still {
			continue
		}
		// Not vault content: one staged copy of a resolution's content inside this
		// package's own scratch directory under `.bnotes/sync`, which the content
		// history and the portable scope both exclude wholesale.
		//bn:direct-write removes one staged revision the state file written just above has stopped naming
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			failures = append(failures, fmt.Errorf("vaultimport: discard staged revision %s of %s: %w",
				entry.Name(), packageID, err))
		}
	}
	return errors.Join(failures...)
}

// discardStaged removes one resolution's staged content. The caller must have
// already written the state that stopped naming it.
func discardStaged(root, packageID string) error {
	dir, err := resolutionDir(root, packageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(root, dir); err != nil {
		return err
	}
	// Nothing in internal/vault removes a tree, and this is not vault content: it
	// is this package's own scratch directory under `.bnotes/sync`, which the
	// content history and the portable scope both exclude wholesale.
	//bn:direct-write removes only this resolution's own staged-content directory, after the state file that named it was rewritten without it
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("vaultimport: discard staged revision content for %s: %w", packageID, err)
	}
	return nil
}
