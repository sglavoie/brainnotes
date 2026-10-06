package vaultexport

import "errors"

// The sentinels below are fine-grained where the *remedy* differs, following
// internal/vaultsync's vocabulary rather than inventing a second one. Three of
// them mean "stop and involve a person", and they are kept apart because the
// person has to do a different thing in each case.
var (
	// ErrCorruptState reports export state that cannot be decoded, or decodes
	// into a record that contradicts itself. Distinct from
	// ErrUnsupportedVersion: this is damage, that is a newer writer.
	//
	// It is never self-healed. internal/index rebuilds itself silently because
	// it holds no user intent; this file holds how far a peer has been told
	// about, and guessing at that would either resend history or skip it.
	ErrCorruptState = errors.New("vaultexport: corrupt export state")

	// ErrUnsupportedVersion reports readable export state written under a
	// version this build does not know.
	ErrUnsupportedVersion = errors.New("vaultexport: unsupported export state version")

	// ErrIdentityChanged reports export state naming a vault or device other
	// than the one the enrollment record names. It means a state file was
	// copied between vaults, which is the one way a device can publish under an
	// identity that is not its own.
	ErrIdentityChanged = errors.New("vaultexport: export state belongs to another vault or device")

	// ErrEpochChanged reports a cursor recorded under an earlier enrollment
	// epoch. The epoch advances when a vault is restored from a backup, which is
	// precisely when the history the cursor describes may no longer be the
	// history this vault has. Reset re-baselines under the new epoch.
	ErrEpochChanged = errors.New("vaultexport: export cursor predates the current enrollment epoch")

	// ErrBranchChanged reports a cursor recorded against a different branch. The
	// commits on another branch are a different line of history and advancing
	// across them would publish a sequence of operations that never happened in
	// that order.
	ErrBranchChanged = errors.New("vaultexport: export cursor names another branch")

	// ErrHistoryRewritten reports a cursor commit the branch no longer contains:
	// a rebase, an amend, a filter, or a hard reset. The operations already
	// published describe commits that are no longer this branch's, and nothing
	// here can tell which of them survived, so it refuses rather than guessing.
	ErrHistoryRewritten = errors.New("vaultexport: the branch no longer contains the export cursor")

	// ErrUnsupportedHistory reports a commit range this producer cannot replay
	// linearly — a merge, or a commit whose parent is not the commit before it.
	// A merge is not an operation: it is two lines of history joining, and there
	// is no single set of paths that changed together for a peer to apply.
	ErrUnsupportedHistory = errors.New("vaultexport: history cannot be replayed linearly")

	// ErrUnsupportedEntry reports a tracked path that is not a regular file — a
	// symlink, a submodule, a mode this reader does not understand. Skipping it
	// would silently leave content out of a replica that believes it is a
	// replica, so it halts and names the path.
	ErrUnsupportedEntry = errors.New("vaultexport: tracked path is not a regular file")

	// ErrUnauthorizedDeletion is retained for source compatibility.
	// Deprecated: regular-file deletions no longer require journal authority.
	ErrUnauthorizedDeletion = errors.New("vaultexport: committed deletion carries no journal authority")

	// ErrChannelConflict reports a reserved package identity whose directory in
	// the channel is neither this producer's completed package nor an
	// interrupted publication of it. Something else writes into this channel,
	// and continuing would interleave two producers' streams.
	ErrChannelConflict = errors.New("vaultexport: outgoing channel holds an unexpected package")
)

// One halt has no sentinel here on purpose. A package that would send a name the
// peer cannot tell apart from one this device already sent is reported as
// vaultsync.ErrPathCollision, the same error that pair gets inside a single
// manifest. The producer sees a case it has to look in the path ledger for and
// vaultsync cannot; the condition, the remedy, and the thing a person has to
// understand are identical, and a second name for them would only make one of the
// two look like a different problem. See exporter.checkPathCollisions and
// collisionIndex, which restate both halves of vaultsync's rule — a file clash
// and a file-versus-directory clash — over the ledger.
