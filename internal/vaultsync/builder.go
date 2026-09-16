package vaultsync

import (
	"fmt"
	"time"
)

// payloadSuffix is the extension every payload file carries. Payloads are
// content-addressed — the name is the digest — so the suffix is decoration for
// a person listing the directory and is never parsed.
const payloadSuffix = ".bin"

// Expectation is what a producer believed a path held before it changed it.
//
// It is carried rather than recomputed because it is a statement about the
// producer's causal position, not about either disk. A receiver whose path
// holds something else has a conflict — two devices edited the same note from
// the same starting point — and that is a fact the package has to make
// checkable rather than something the receiver can infer afterwards.
type Expectation struct {
	// Exists is whether the producer believed the path was there.
	Exists bool
	// SHA256 is what it believed the path held, set when and only when Exists.
	SHA256 string
}

// Absent is the expectation for a path the producer believed did not exist.
func Absent() Expectation { return Expectation{} }

// Existing is the expectation for a path the producer believed held these
// exact bytes.
func Existing(sha256 string) Expectation { return Expectation{Exists: true, SHA256: sha256} }

// Builder assembles one package and its payloads together, so the inventory
// and the changes that name it cannot disagree.
//
// Hand-assembling a Manifest is possible and Validate will catch most of the
// ways it goes wrong, but the coupling between a change's resulting hash, its
// payload name, and the inventory entry's digest is the kind that is easy to
// get subtly right on the producer and wrong on a retry. Here it is derived
// once, from the bytes.
//
// A Builder is not safe for concurrent use. One package is assembled by one
// producer.
type Builder struct {
	manifest Manifest
	payloads map[string][]byte
	current  int
	err      error
}

// NewBuilder starts a package from local's identity at the given position in
// this device's own stream.
//
// sequence is the caller's to choose and to persist: it is the producer's
// export cursor, and a package published twice under one sequence would make
// the peer's dependency arithmetic ambiguous. Nothing here remembers it.
func NewBuilder(local *Enrollment, sequence uint64, at time.Time) (*Builder, error) {
	if local == nil {
		return nil, fmt.Errorf("%w: no local enrollment", ErrNotEnrolled)
	}
	if err := local.Validate(); err != nil {
		return nil, err
	}
	packageID, err := NewPackageID()
	if err != nil {
		return nil, err
	}
	return &Builder{
		manifest: Manifest{
			Format:       Format,
			PackageID:    packageID,
			VaultID:      local.VaultID,
			OriginDevice: local.DeviceID,
			Epoch:        local.Epoch,
			Sequence:     sequence,
			CreatedAt:    at.UTC(),
			Requires:     []Dependency{},
			Operations:   []Operation{},
			Payloads:     []Payload{},
		},
		payloads: map[string][]byte{},
		current:  -1,
	}, nil
}

// PackageID is the identity this package will be published under. It exists
// before the package does, so a caller can record the attempt.
func (b *Builder) PackageID() string { return b.manifest.PackageID }

// Require records a package this one must be applied after.
func (b *Builder) Require(originDevice string, sequence uint64) *Builder {
	b.manifest.Requires = append(b.manifest.Requires, Dependency{
		OriginDevice: originDevice,
		Sequence:     sequence,
	})
	return b
}

// BeginOperation opens a new multi-file operation boundary. Every change added
// afterwards belongs to it until the next BeginOperation.
//
// operationID is the identity minted where the operation actually happened — a
// rename, a bulk repair — and is passed in rather than generated, because it is
// what later packages' causal predecessors point at. An empty identity mints a
// fresh one, which is right only for an operation that has no prior identity of
// its own.
func (b *Builder) BeginOperation(operationID string, recordedAt time.Time) *Builder {
	if b.err != nil {
		return b
	}
	if operationID == "" {
		minted, err := NewOperationID()
		if err != nil {
			b.err = err
			return b
		}
		operationID = minted
	}
	b.manifest.Operations = append(b.manifest.Operations, Operation{
		OperationID: operationID,
		RecordedAt:  recordedAt.UTC(),
		Changes:     []Change{},
	})
	b.current = len(b.manifest.Operations) - 1
	return b
}

// Write records a path whose committed bytes are content.
//
// The bytes are the *committed* ones: what a peer applies is what this vault
// durably holds, never an editor buffer or a file being written. Preserving
// that is the caller's job; this only records what it is handed.
func (b *Builder) Write(path string, content []byte, prior Expectation, predecessors ...string) *Builder {
	if b.err != nil {
		return b
	}
	digest := HashBytes(content)
	name := digest + payloadSuffix
	if _, carried := b.payloads[name]; !carried {
		b.payloads[name] = content
		b.manifest.Payloads = append(b.manifest.Payloads, Payload{
			Name:   name,
			SHA256: digest,
			Size:   int64(len(content)),
		})
	}
	return b.change(Change{
		Path:         path,
		Action:       ActionWrite,
		Predecessors: normalizePredecessors(predecessors),
		ExpectExists: prior.Exists,
		ExpectSHA256: prior.SHA256,
		ResultSHA256: digest,
		ResultSize:   int64(len(content)),
		Payload:      name,
	})
}

// Delete records a tombstone for a path.
func (b *Builder) Delete(path string, prior Expectation, predecessors ...string) *Builder {
	if b.err != nil {
		return b
	}
	return b.change(Change{
		Path:         path,
		Action:       ActionDelete,
		Predecessors: normalizePredecessors(predecessors),
		ExpectExists: prior.Exists,
		ExpectSHA256: prior.SHA256,
	})
}

func (b *Builder) change(change Change) *Builder {
	if b.current < 0 {
		b.err = fmt.Errorf("%w: %q was added outside any operation", ErrMalformedPackage, change.Path)
		return b
	}
	op := &b.manifest.Operations[b.current]
	op.Changes = append(op.Changes, change)
	return b
}

// Build returns the finished manifest and the payload bytes Publish needs. It
// validates, so a package that could not be published is refused here rather
// than after a directory exists.
func (b *Builder) Build() (*Manifest, map[string][]byte, error) {
	if b.err != nil {
		return nil, nil, b.err
	}
	manifest := b.manifest.clone()
	if err := manifest.Validate(); err != nil {
		return nil, nil, err
	}
	payloads := make(map[string][]byte, len(b.payloads))
	for name, data := range b.payloads {
		payloads[name] = data
	}
	return manifest, payloads, nil
}

// clone returns a manifest sharing no mutable state with the receiver.
//
// A struct copy is not enough, and the difference is the whole reason this
// exists. Operations, each operation's Changes and Predecessors, and Payloads
// are slices, so a copied Manifest still points at the builder's backing arrays.
// A caller that went on building after Build — one more Write into the open
// operation — would be editing a manifest that has already been validated and
// may already be on its way into a channel, and whether the edit is visible
// through the returned pointer would come down to whether append happened to
// have spare capacity. Revalidating in Publish does not catch it: by then the
// mutated manifest is the manifest, and it is still self-consistent.
//
// nil slices stay nil and empty ones stay empty, because the difference is
// visible in the encoded record — `null` against `[]` — and this format is one
// people read.
func (m *Manifest) clone() *Manifest {
	out := *m
	out.Requires = cloneSlice(m.Requires)
	out.Payloads = cloneSlice(m.Payloads)
	out.Operations = cloneSlice(m.Operations)
	for i := range out.Operations {
		out.Operations[i].Changes = cloneSlice(m.Operations[i].Changes)
		for j := range out.Operations[i].Changes {
			out.Operations[i].Changes[j].Predecessors = cloneSlice(m.Operations[i].Changes[j].Predecessors)
		}
	}
	return &out
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

// PublishTo builds the package and publishes it into channelDir in one step.
func (b *Builder) PublishTo(channelDir string) (*Manifest, string, error) {
	manifest, payloads, err := b.Build()
	if err != nil {
		return nil, "", err
	}
	dir, err := Publish(channelDir, manifest, payloads)
	if err != nil {
		return nil, "", err
	}
	return manifest, dir, nil
}

// normalizePredecessors returns a non-nil slice, so an encoded manifest carries
// `[]` rather than `null` for a path with no seen history. The two decode
// identically in Go; they do not read identically to a person diffing two
// manifests, and this format is one people will read.
func normalizePredecessors(predecessors []string) []string {
	if len(predecessors) == 0 {
		return []string{}
	}
	return append([]string(nil), predecessors...)
}
