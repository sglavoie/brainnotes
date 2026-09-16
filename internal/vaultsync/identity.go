package vaultsync

import (
	"crypto/rand"
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
