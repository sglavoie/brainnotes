package vaultsync

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// Identity prefixes. Every identifier this protocol carries says what kind of
// thing it names, so a device identifier pasted into a vault field is a decode
// failure rather than a package addressed to a vault that will never exist.
// They are not namespaces and carry no meaning beyond the kind.
const (
	vaultPrefix     = "vault-"
	devicePrefix    = "device-"
	packagePrefix   = "pkg-"
	operationPrefix = "op-"
)

// identityBytes is the random body of every identifier, before hex encoding.
// 128 bits, because two devices mint identifiers independently and never
// negotiate: uniqueness has to come from the width alone.
const identityBytes = 16

// identityHexLen is the encoded length of that body.
const identityHexLen = identityBytes * 2

// NewVaultID mints the identity two replicas of one vault share.
func NewVaultID() (string, error) { return newIdentity(vaultPrefix) }

// NewDeviceID mints the identity of one enrolled replica.
func NewDeviceID() (string, error) { return newIdentity(devicePrefix) }

// NewPackageID mints the identity of one published package. It is the name the
// package is published under and the name a receipt answers, so it is minted
// before the package is built rather than derived from its contents: a package
// identity has to exist for a package that failed to publish, or a retry would
// be indistinguishable from a second package.
func NewPackageID() (string, error) { return newIdentity(packagePrefix) }

// NewOperationID mints the immutable identity of one multi-file operation. It
// is minted where the operation happens and never re-derived, because it is
// what every later package's causal predecessors point at: re-deriving it from
// content would make an identical edit made twice look like one event.
func NewOperationID() (string, error) { return newIdentity(operationPrefix) }

// DeriveOperationID returns the operation identity of an event named by parts,
// so a producer that has to describe the same event twice describes it under the
// same identity both times.
//
// This is the deliberate exception to NewOperationID's rule, and the distinction
// is what the event is named *by*. Deriving an operation identity from the
// content it changed would be wrong for the reason NewOperationID gives: the
// same edit made twice is two events and must not collapse into one. Deriving it
// from something that already is a durable, unique name for the event itself —
// a commit object name, which differs for two identical edits because their
// parents and timestamps differ — collapses nothing that was ever distinct.
//
// What it buys is the failure case. A producer that published a package and
// crashed before recording that it had cannot tell, on restart, whether the peer
// saw it; it republishes. With a minted identity the peer receives one event
// twice under two names and has no way to know they are one. With a derived one
// the second copy carries the identity the first did, and an applier that
// remembers which operations it has applied is idempotent for free.
//
// parts are length-prefixed before hashing, so ("ab", "c") and ("a", "bc") are
// different events. Callers should lead with the vault identity, which namespaces
// the derivation: two *unrelated* vaults can genuinely produce the same commit
// object name — seeded from one template and committed under the same author and
// timestamp, they have the same tree, the same parent, and therefore the same
// name — and without the namespace they would mint one operation identity for two
// unrelated events.
//
// It deliberately does not separate a fork or a copy of one vault, because those
// carry that vault's identity with them and nothing derived here could tell them
// apart. A copied replica that starts publishing is a different problem with a
// different answer: the enrollment and export records name a device, and a record
// arriving under an identity that is not this vault's is refused outright.
func DeriveOperationID(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(part))
	}
	return operationPrefix + hex.EncodeToString(digest.Sum(nil)[:identityBytes])
}

func newIdentity(prefix string) (string, error) {
	var raw [identityBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("vaultsync: mint %sidentifier: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

// validIdentity reports whether s is an identifier of the given kind. The body
// is required to be lowercase hex of the exact length, so an identifier that
// round-tripped through something that upcased it is rejected here rather than
// silently becoming a second identity for the same thing.
func validIdentity(prefix, s string) bool {
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	body := s[len(prefix):]
	if len(body) != identityHexLen {
		return false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// checkIdentity returns a described error for an identifier of the wrong shape.
// field names the manifest or record field, so a refusal says which one.
func checkIdentity(sentinel error, field, prefix, value string) error {
	if validIdentity(prefix, value) {
		return nil
	}
	return fmt.Errorf("%w: %s %q is not a %sidentifier", sentinel, field, value, prefix)
}
