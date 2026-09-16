package vaultsync

import (
	"time"
)

// Format is the wire format token every package carries. It is compared
// exactly, never parsed for a version number to compare as an ordering: a
// receiver that understood "v1 or later" would be claiming to understand a
// format that does not exist yet. A newer format is a package this build
// refuses and leaves in the channel for a build that knows it.
const Format = "brainnotes-sync-v1"

// Action is what one change does to one path.
type Action string

const (
	// ActionWrite installs the resulting bytes at the path, creating it or
	// replacing it. One action for both, because "create" and "modify" are the
	// same operation once the expected prior state is carried explicitly.
	ActionWrite Action = "write"
	// ActionDelete removes the path. It is a tombstone rather than an absence:
	// it carries the same identity, predecessors, and expected prior state a
	// write does, so a deletion is causally ordered against concurrent edits and
	// can lose a conflict instead of silently winning one. A package with no
	// tombstone for a path says nothing about that path; only a tombstone says
	// it was removed.
	ActionDelete Action = "delete"
)

// Manifest describes one published package.
//
// A package is immutable once published, and every field here is part of that
// immutability: the identity a receipt answers, the origin the ordering is
// relative to, the epoch the package is only meaningful within, and the exact
// bytes the payload directory must hold.
type Manifest struct {
	// Format is the wire format token, always Format for a package this build
	// produced.
	Format string `json:"format"`
	// PackageID identifies this package. It is what a receipt answers and what
	// a dependency would name, so it is stable across retries of a delivery and
	// never reused.
	PackageID string `json:"package_id"`
	// VaultID is the vault both replicas share. A package naming another vault
	// is refused rather than merged.
	VaultID string `json:"vault_id"`
	// OriginDevice is the enrolled device that produced the package. It is a
	// routing and enrollment fact, not an authentication one.
	OriginDevice string `json:"origin_device"`
	// Epoch is the enrollment epoch the package was produced under.
	Epoch uint64 `json:"epoch"`
	// Sequence is this package's position in OriginDevice's own totally ordered
	// stream, starting at 1. It is per-origin rather than global: the two
	// devices publish concurrently and never coordinate a counter.
	Sequence uint64 `json:"sequence"`
	// CreatedAt is when the package was built. It is descriptive; nothing
	// orders on it. Two devices' clocks disagree and ordering that trusted them
	// would be a lottery, which is what Sequence and Predecessors replace.
	CreatedAt time.Time `json:"created_at"`
	// Requires are the packages this one depends on having been applied first.
	// A receiver holding an unmet dependency holds the package rather than
	// applying it out of order.
	Requires []Dependency `json:"requires"`
	// Operations are the multi-file operation boundaries, preserved. A rename
	// that moved a note and rewrote every wikilink pointing at it is one
	// operation here, and a receiver applies it whole or not at all.
	Operations []Operation `json:"operations"`
	// Payloads is the inventory of committed bytes the package carries. It is
	// what makes a partially transferred package detectable: a payload the
	// inventory names and the directory does not hold is an incomplete package,
	// not a package with a missing file.
	Payloads []Payload `json:"payloads"`
}

// Dependency names another package by its origin and sequence rather than by
// its package identity, so a receiver can tell an unmet dependency from an
// unknown one: it knows how far it has consumed each origin's stream, and a
// sequence beyond that is simply not here yet.
type Dependency struct {
	// OriginDevice is the device whose stream the dependency belongs to.
	OriginDevice string `json:"origin_device"`
	// Sequence is the position in that stream.
	Sequence uint64 `json:"sequence"`
}

// Operation is one multi-file operation, preserved as a boundary.
type Operation struct {
	// OperationID is the immutable identity minted where the operation
	// happened. It never changes as the operation is packaged, transferred, or
	// applied, because it is what every later change's Predecessors point at.
	OperationID string `json:"operation_id"`
	// RecordedAt is when the operation happened on its origin device.
	RecordedAt time.Time `json:"recorded_at"`
	// Changes are the per-path effects, at least one.
	Changes []Change `json:"changes"`
}

// Change is one path's share of an operation.
type Change struct {
	// Path is the vault-relative slash-separated path, which must be portable
	// content. See Classify.
	Path string `json:"path"`
	// Action is what happens to the path.
	Action Action `json:"action"`
	// Predecessors are the operation identities this change is causally after
	// *for this path*: the operations the origin had already applied to Path
	// when it made this change. It is per-path rather than per-package because
	// that is the granularity a conflict has — two devices editing different
	// notes concurrently is not a conflict, and a package-level "parent" would
	// make it one.
	//
	// An empty list means the origin believed the path had no history it had
	// seen, which is what a creation looks like.
	//
	// An identity naming an operation *this same manifest* carries must point
	// backwards. A manifest's operations are ordered and an applier walks them
	// in that order, so an edge onto the current operation or a later one is one
	// nothing could ever satisfy; Validate refuses it. Naming an earlier
	// operation in the same package is legitimate and is how one logical change
	// split across several boundaries — a rename and the repair that follows it
	// — stays ordered inside a single package.
	Predecessors []string `json:"predecessors"`
	// ExpectExists is whether the origin believed the path existed before the
	// change.
	ExpectExists bool `json:"expect_exists"`
	// ExpectSHA256 is the content the origin believed the path held. Set when
	// and only when ExpectExists. A receiver whose path holds something else
	// has a conflict rather than a failed write.
	ExpectSHA256 string `json:"expect_sha256,omitempty"`
	// ResultSHA256 is the content the path holds after the change. Empty for a
	// tombstone.
	ResultSHA256 string `json:"result_sha256,omitempty"`
	// ResultSize is the length of those bytes, zero for a tombstone.
	ResultSize int64 `json:"result_size"`
	// Payload is the inventory name holding the resulting bytes, empty for a
	// tombstone.
	Payload string `json:"payload,omitempty"`
}

// Tombstone reports whether the change removes its path.
func (c Change) Tombstone() bool { return c.Action == ActionDelete }

// Payload is one entry in a package's inventory of committed bytes.
type Payload struct {
	// Name is the file name inside the package's payload directory. It is a
	// single path component with no directory structure, so a payload name can
	// never be a path at all, let alone one that escapes.
	Name string `json:"name"`
	// SHA256 is the hex digest of the bytes.
	SHA256 string `json:"sha256"`
	// Size is the byte length. Carried beside the digest so a truncated
	// transfer is detectable without hashing, and cross-checked against it so
	// the two cannot disagree.
	Size int64 `json:"size"`
}

// Paths returns every path the package changes, in manifest order.
func (m *Manifest) Paths() []string {
	var paths []string
	for _, op := range m.Operations {
		for _, change := range op.Changes {
			paths = append(paths, change.Path)
		}
	}
	return paths
}

// Changes returns every change in the package, in manifest order, flattened
// across its operation boundaries. The boundaries still matter to a caller that
// applies the package; this is for the callers that only need the effects.
func (m *Manifest) Changes() []Change {
	var changes []Change
	for _, op := range m.Operations {
		changes = append(changes, op.Changes...)
	}
	return changes
}

// payloadIndex maps inventory names to entries.
func (m *Manifest) payloadIndex() map[string]Payload {
	index := make(map[string]Payload, len(m.Payloads))
	for _, payload := range m.Payloads {
		index[payload.Name] = payload
	}
	return index
}
