// Package attachments is the private staging area for files uploaded alongside
// a note that has not been saved yet.
//
// An upload arrives before the note that will reference it exists, so its bytes
// have to live somewhere for a while without being part of the vault. That
// somewhere is `<vault>/.bnotes/uploads`, and every property this package has
// follows from where it is:
//
//   - it is outside `vaultgit.InScope`, so a staged file never reaches the
//     content history and an abandoned draft leaves no commit behind;
//   - it is outside `vault.Walk`, so a staged file is never loaded as a note;
//   - every component of it starts with a dot, and `internal/web`'s media
//     handler refuses any such component at any depth, so staged bytes cannot be
//     served — which matters because the bytes are whatever a browser uploaded
//     and have had no safety headers decided for them yet.
//
// A staged file becomes part of the vault only when the note referencing it is
// saved, and it gets there through that note's own `vault.FileTxn`: one
// journaled operation covering the note and every attachment it commits, so an
// undo takes back all of it or none of it. Nothing in this package writes to the
// vault's content — it stages, it names, and it forgets.
//
// # What an upload id is
//
// An id is 16 bytes from crypto/rand rendered as hex, and it is the only handle
// a client ever holds. It is unguessable, so possession of one is already
// evidence; the draft `Owner` recorded beside it is a second, cheaper check that
// also lets a browser cancel everything one editor tab staged. A lookup that
// fails either check reports [ErrNotFound] rather than distinguishing "not
// yours" from "not there", because the distinction is exactly the fact an id
// guess would be probing for.
//
// # What makes an upload complete
//
// Three artifacts, written in this order and never any other:
//
//  1. `<id>.bin`, the bytes, created O_EXCL, streamed, hashed as they arrive,
//     and fsynced;
//  2. `names/<key>.name`, an O_EXCL claim on the vault-relative destination the
//     file will be committed to, which is how two uploads racing for the same
//     filename get two different destinations instead of one lost file;
//  3. `<id>.json`, the record, written last.
//
// The record is therefore the completion marker: [Store.Lookup] reads it and
// nothing else, so an upload the client abandoned or a process died in the
// middle of is invisible rather than half-usable. What it leaves behind is a
// blob with no record, which is [Store.Sweep]'s job.
package attachments

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Sentinel failures a caller above this package has to tell apart, because each
// one is a different answer to a person: too big, unusable name, unusable draft,
// and nothing there.
//
// ErrNotFound deliberately covers "no such upload", "not complete yet", and
// "owned by a different draft". They are one answer on purpose — see the package
// doc.
var (
	ErrTooLarge        = errors.New("attachments: upload exceeds the per-file limit")
	ErrInvalidFilename = errors.New("attachments: unusable filename")
	ErrInvalidOwner    = errors.New("attachments: unusable draft owner")
	ErrInvalidID       = errors.New("attachments: unusable upload id")
	ErrNotFound        = errors.New("attachments: no such staged upload")
)

// uploadIDBytes is the entropy behind an upload id. Sixteen bytes is the same
// order as a v4 UUID and far past anything an attacker could enumerate against a
// local server, which is what lets possession of an id stand in for a capability.
const uploadIDBytes = 16

// newUploadID mints an opaque upload id.
//
// It is hex rather than base64 so the id is safe in a query string, a filename,
// and a JSON field without any escaping anywhere, and so validID can be a
// character test rather than a decode.
func newUploadID() (string, error) {
	var raw [uploadIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("attachments: generate upload id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// validID refuses anything that is not an id this package minted.
//
// It runs before an id is ever joined onto a path, which is what makes the join
// safe: the alphabet has no separator, no dot, and no case, so a validated id
// cannot traverse, cannot name a hidden file, and cannot be two names on a
// case-insensitive filesystem.
func validID(id string) error {
	if len(id) != uploadIDBytes*2 {
		return fmt.Errorf("%w %q", ErrInvalidID, id)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return fmt.Errorf("%w %q", ErrInvalidID, id)
	}
	return nil
}

// ownerMaxLen bounds a draft owner. The owner never becomes a path component —
// it is stored as a JSON string and compared as one — so this is about refusing
// an absurd request rather than about containment.
const ownerMaxLen = 128

// cleanOwner normalizes and refuses a draft owner.
//
// A control character is rejected rather than stripped: the owner is echoed back
// in refusals and reaches a log line, and a value that could carry a newline
// there is not worth accepting for the sake of a client that sent one.
func cleanOwner(owner string) (string, error) {
	owner = strings.TrimSpace(owner)
	switch {
	case owner == "":
		return "", fmt.Errorf("%w: empty", ErrInvalidOwner)
	case len(owner) > ownerMaxLen:
		return "", fmt.Errorf("%w: longer than %d bytes", ErrInvalidOwner, ownerMaxLen)
	case strings.ContainsFunc(owner, isControl):
		return "", fmt.Errorf("%w: control character", ErrInvalidOwner)
	}
	return owner, nil
}
