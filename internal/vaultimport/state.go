package vaultimport

import (
	"io/fs"
	"path"
	"path/filepath"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// stateVersion is the schema version of the import record. Compared exactly for
// the reason the export record gives: a newer build may carry a field this one
// would drop on the next write, and dropping an unresolved attempt silently is
// worse than refusing to run.
//
// Raised to 2 for the conflict scheduler: a record now carries the origin
// sequence a dependency is stated against and the convergence flag that explains
// an application with no recovery entry, and the file carries the held conflicts.
// A version 1 file names none of those, and a build that read one would take an
// applied package for an unapplied position in its origin's stream.
//
// Raised to 3 for the resolution engine, and this one is exactly the harm the
// exact comparison exists to prevent rather than a missing field. A resolution
// in progress is recorded here between the application it revises and the
// revision itself; a build that did not know the field would drop it on its next
// write, and the vault would be left agreeing with the peer about a note
// somebody had decided to keep, with nothing left that says so.
const stateVersion = 3

// stateFileName is the record's name inside the synchronization state
// directory, beside the exporter's.
const stateFileName = "import.json"

// statePerm matches the export record's: identities and path names, no secrets,
// in a directory that is already world-readable.
const statePerm fs.FileMode = 0o644

// StatePath returns the absolute path of the import record for the vault at
// root.
//
// Derived from vaultsync.StateDir rather than spelled out, so it cannot drift
// out of the directory the portable policy excludes wholesale. A record naming
// every path an import touched is exactly the thing that must never become
// portable content.
func StatePath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), stateFileName)))
}

// State is what this vault remembers about packages it has applied, and about
// the one it may have been applying when it stopped.
//
// It exists because none of the six steps an import goes through can be made
// atomic with the next one. The peer's bytes land, the journal manifest lands,
// the commit lands, the receipt lands — four separate durability events, and a
// process can die between any two. Without a record written *before* the first
// of them, a vault that comes back up cannot tell an import that never started
// from one that half finished, and cannot tell either from a package being
// delivered a second time.
//
// It is one file rather than an attempt file and a ledger file, for the reason
// the export record is one file: clearing the attempt and recording what it
// applied are the same decision, and a crash between two files would either
// strand the attempt or forget the application.
type State struct {
	// Version is stateVersion.
	Version int `json:"version"`
	// VaultID and DeviceID are the enrollment identities this record was
	// written under, checked on load. A record copied from the peer would
	// otherwise make this device believe it had applied the peer's imports.
	VaultID  string `json:"vault_id"`
	DeviceID string `json:"device_id"`
	// Pending is the application that was started and whose outcome is not yet
	// known, written before this vault is touched and cleared once it is
	// resolved. It is the only thing that makes an interrupted import
	// recoverable rather than a mystery: the paths, what they held, and what
	// they were going to hold are all in it, so a later process can decide
	// between rolling forward and rolling back without the package.
	Pending *Attempt `json:"pending,omitempty"`
	// Applied maps a package identity to what applying it produced here. It is
	// what makes a redelivery idempotent: the transport may deliver one package
	// any number of times, and every delivery after the first must answer from
	// this map instead of touching the vault again.
	//
	// Entries are retained until two independent facts hold: the package is gone
	// from the incoming channel, and the sender-authored retirement high-water
	// mark covers its origin sequence. The second fact is the safety boundary. A
	// locally missing package alone may be restored by the receive-only transport,
	// while the watermark is written only after the sender has retired its owned
	// package and can no longer redeliver it. Requiring absence as well handles
	// transport reordering where that watermark arrives before the deletion.
	// The watermark is one monotonically advancing record rather than a tombstone
	// per package, and it names only the sender's still-redeliverable exceptions,
	// so both it and this map remain bounded by outstanding work rather than by
	// vault lifetime.
	Applied map[string]Record `json:"applied"`
	// Conflicts maps a package identity to the hold placed on it: the contested
	// paths, and where every version of each was kept.
	//
	// It is deliberately not an Attempt. A held operation has touched nothing, so
	// recording it as pending would make every later import refuse — which is the
	// opposite of holding one operation while independent work proceeds. Each
	// entry is re-decided on every pass over the package and cleared the moment
	// the disk stops disagreeing, so a person who resolves a conflict by editing
	// the note needs no command to say that they did.
	Conflicts map[string]*ConflictRecord `json:"conflicts,omitempty"`
	// Resolutions maps a package identity to the revision a resolution has
	// promised to make on top of it, and is the one field here that describes
	// something this vault has decided to do rather than something it has done.
	//
	// It exists because a resolution is two commits and the first of them agrees
	// with the peer. An interruption in between leaves a vault holding the version
	// that was rejected, which is indistinguishable from a package that was simply
	// accepted — so the decision is written down before the first commit, and
	// every entry point finishes it before doing anything else. It is cleared when
	// the revision is made, or when the application it was waiting on turns out
	// never to have landed. See finishResolutions.
	Resolutions map[string]*Plan `json:"resolutions,omitempty"`
}

// Attempt is one application in progress, made durable before it begins.
//
// It is self-contained on purpose. A recovering process has nothing else to work
// from: the journal entry may have no manifest to read, the peer is not
// reachable, and the package directory is a caller's argument that this record
// has no claim on — Apply takes the path it is handed, and by the time recovery
// runs the channel may have been swept, re-delivered, or mounted somewhere else.
// So the accepted manifest is kept whole here rather than re-read, and every
// question recovery asks is answered out of these two fields.
type Attempt struct {
	// JournalEntry is the local undo entry the application will record under.
	// It is minted before the recorder exists and pinned, which is what lets a
	// recovering process find a half-built entry it never saw created.
	JournalEntry string `json:"journal_entry"`
	// Manifest is the package as vaultsync.Accept approved it. Both ends of
	// every change matter to recovery: the expected prior state is what a
	// rollback restores to, and the resulting state is what says the change
	// landed at all. It is also the only thing that can mint the receipt the
	// peer is owed, since an acknowledgment is checked against the package it
	// answers.
	Manifest *vaultsync.Manifest `json:"manifest"`
	// Converged are the paths the package names that this vault already held at
	// the result, and which the transaction therefore never staged.
	//
	// It has to be here because recovery cannot tell them apart from applied
	// ones. A rollback decides per path by comparing the disk against what the
	// package expected and what it installs, and a path that had converged reads
	// as installed — so a recovering process would restore it to the package's
	// baseline and destroy content this import never wrote. They are named rather
	// than re-derived for the reason Restored is: the disk moves on, and the
	// question is about what this application did.
	Converged []string `json:"converged,omitempty"`
	// Restored is the rollback's record of itself, written the moment the
	// content is back and before the two steps that follow it. Nil means no
	// rollback has finished.
	//
	// It exists because the rollback's own decision is not repeatable. A plan is
	// made by comparing each path against what the package expected to find and
	// what it installed, and once the content has been put back every path reads
	// as the first of those — indistinguishable from a path that was never
	// written. That is harmless while nothing else touches the vault, and wrong
	// the moment something does: a person who edits one of those notes before the
	// interrupted rollback is finished makes it read as neither, which is the
	// signature of an edit that has to be refused rather than overwritten. Without
	// this the refusal would be aimed at a rollback that had already happened, and
	// a finished rollback would be treated as an unresolvable one for the rest of
	// the vault's life.
	Restored *Rollback `json:"restored,omitempty"`
}

// Rollback is a completed restoration: what a rollback put back, kept so the
// steps after it never have to decide again.
type Rollback struct {
	// Paths are the vault-relative paths whose content was restored, in package
	// order. Empty is a real answer and the commonest one — the interruption
	// came before anything was written — and is why this is a pointer: absent
	// and "restored nothing" are different facts.
	Paths []string `json:"paths"`
}

// PackageID is the package this attempt was applying.
func (a *Attempt) PackageID() string {
	if a == nil || a.Manifest == nil {
		return ""
	}
	return a.Manifest.PackageID
}

// Record is one applied package's outcome.
type Record struct {
	// OperationID is the peer operation that was applied.
	OperationID string `json:"operation_id"`
	// OriginDevice is the peer that produced it.
	OriginDevice string `json:"origin_device"`
	// Sequence is the package's position in that peer's stream, which is what a
	// later package's stated dependency is expressed in. Without it this vault
	// remembers that it applied a package and cannot answer whether it has
	// consumed the position another package requires.
	Sequence uint64 `json:"sequence"`
	// JournalEntry is the local recovery entry that can undo it. Empty only when
	// Converged, which is the one application that wrote nothing.
	JournalEntry string `json:"journal_entry"`
	// Converged reports a package this vault had already made every change of
	// before it arrived — two replicas that reached the same content
	// independently, which is ordinary when both are edited from one source.
	//
	// It applied nothing, so there is no recovery entry. Its receipt explicitly
	// records convergence instead: every package result was observed on disk and
	// there is no package write to undo. It is recorded rather than left
	// unrecorded so a redelivery republishes the same receipt instead of being
	// inspected again.
	Converged bool `json:"converged,omitempty"`
	// Receipt is the acknowledgment minted for the peer, kept whole.
	//
	// Stored rather than re-derived, and that is a correctness decision. A
	// receipt states what each path held when the package was applied; minting a
	// replacement now would state what they hold today, and a note edited since
	// would make the two disagree — so a peer that lost the first receipt could
	// never be given an honest second one. Keeping the original means a lost
	// reply is answered by republishing the same bytes, whatever has happened to
	// the vault since.
	//
	// Nil means the package was applied but no acknowledgment could be minted.
	// A non-nil receipt may still be waiting for publication; retaining the minted
	// statement is what lets a redelivery publish it after later work moves the
	// live paths on.
	Receipt *vaultsync.Receipt `json:"receipt,omitempty"`
}
