package vaultimport

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

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

// disposition is one package's changes sorted into the three answers
// inspecting a change can give: the path holds what the package expected (it
// is staged), already holds what the package installs (converged), or holds
// neither (a conflict).
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
