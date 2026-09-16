package vaultsync

import "errors"

// The sentinels below are the vocabulary a caller branches on. They are
// deliberately fine-grained where the *remedy* differs: a stale package is
// discarded, a package from an unknown epoch means this device has not caught
// up with a re-enrollment, and a package from an unknown device means somebody
// is writing into a channel this vault does not accept from. Collapsing those
// into one "rejected" would make all three look like corruption.
var (
	// ErrNotEnrolled reports a vault that has no enrollment record. It is the
	// ordinary state of every vault that synchronizes with nothing, never a
	// failure on its own.
	ErrNotEnrolled = errors.New("vaultsync: vault is not enrolled")

	// ErrAlreadyEnrolled reports an enrollment that exists. Enrollment is
	// established once and then only advanced, so a second Establish is refused
	// rather than allowed to replace an identity a peer is already addressing.
	ErrAlreadyEnrolled = errors.New("vaultsync: vault is already enrolled")

	// ErrCorruptState reports bytes that cannot be decoded, or a decoded record
	// that contradicts itself. Distinct from ErrUnsupportedVersion: this is
	// damage, that is a newer writer.
	ErrCorruptState = errors.New("vaultsync: corrupt synchronization state")

	// ErrUnsupportedVersion reports a readable record written under a state
	// version this build does not know. Downgrading the record would discard
	// whatever the newer version recorded, so it is refused.
	ErrUnsupportedVersion = errors.New("vaultsync: unsupported state version")

	// ErrUnverifiedEnrollment reports an enrollment whose bytes did not read
	// back as what was written. The baseline is verified rather than assumed:
	// an identity that does not survive its own round trip is one a peer would
	// address into a void.
	ErrUnverifiedEnrollment = errors.New("vaultsync: enrollment did not verify after writing")

	// ErrUnsupportedFormat reports a package whose format token is not Format.
	// A package format is never guessed at and never partially understood.
	ErrUnsupportedFormat = errors.New("vaultsync: unsupported package format")

	// ErrMalformedPackage reports a package whose manifest contradicts itself:
	// a write with no payload, an expectation with no hash, a duplicate
	// operation identity. The bytes decoded; what they say is not a package.
	ErrMalformedPackage = errors.New("vaultsync: malformed package")

	// ErrIncompletePackage reports a package whose payloads on disk do not
	// match its inventory — missing, truncated, altered, or unlisted. This is
	// the ordinary state of a package a file transfer is still moving, which is
	// exactly why a transferred file is never treated as a delivered package.
	ErrIncompletePackage = errors.New("vaultsync: incomplete package")

	// ErrWrongVault reports a package addressed to a different vault identity.
	ErrWrongVault = errors.New("vaultsync: package belongs to another vault")

	// ErrUnknownPeer reports a package whose origin device this vault did not
	// enroll, including a package this vault appears to have sent itself.
	ErrUnknownPeer = errors.New("vaultsync: package origin is not an enrolled peer")

	// ErrStaleEpoch reports a package produced before the current enrollment
	// epoch. Re-enrollment exists to invalidate in-flight packages — after a
	// restore from backup, the peer's queue describes a history this vault no
	// longer has — so applying one is precisely the outcome the epoch prevents.
	ErrStaleEpoch = errors.New("vaultsync: package predates the current enrollment epoch")

	// ErrEpochAhead reports a package produced under an epoch later than this
	// vault's. The remedy is the opposite of ErrStaleEpoch's: this device is
	// behind and must learn the new epoch before it can judge the package.
	ErrEpochAhead = errors.New("vaultsync: package is from a later enrollment epoch")

	// ErrPrivatePath reports a path the portable-content policy excludes:
	// machine-local state, the local configuration overlay, or a directory that
	// belongs to one machine's tooling. See Classify.
	ErrPrivatePath = errors.New("vaultsync: path is not portable vault content")

	// ErrPathTraversal reports a path that is not a usable vault-relative path
	// at all: empty, absolute, or carrying a component that would resolve
	// outside the vault.
	ErrPathTraversal = errors.New("vaultsync: path escapes the vault")

	// ErrPathCollision reports two paths in one package that name the same file
	// on a case-insensitive or normalization-insensitive filesystem. macOS is
	// both by default, so a package that is unambiguous on the Pi can apply as
	// one path overwriting another on the Mac.
	ErrPathCollision = errors.New("vaultsync: paths collide under case folding or Unicode normalization")

	// ErrPackageExists reports a package identity already published in a
	// channel. A published package is immutable: a peer may have read it, and a
	// second version under one identity would make the two devices disagree
	// about what that identity means.
	ErrPackageExists = errors.New("vaultsync: package is already published")

	// ErrNoRecoveryEntry reports an acknowledgment attempted without the local
	// recovery-history entry that can undo the application. An acknowledgment
	// is a promise the peer acts on by dropping its own copy of the change; it
	// is only honest once the change is durable *and* undoable here.
	ErrNoRecoveryEntry = errors.New("vaultsync: acknowledgment requires a local recovery entry")

	// ErrIncompleteApplication reports an acknowledgment covering fewer paths
	// than the package changed, or covering them with different bytes.
	ErrIncompleteApplication = errors.New("vaultsync: acknowledgment does not cover the package")

	// ErrReceiptMismatch reports a receipt that does not answer the package it
	// was matched against.
	ErrReceiptMismatch = errors.New("vaultsync: receipt does not match the package")
)
