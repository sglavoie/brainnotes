package vaultsync

import (
	"strings"
	"testing"
	"time"
)

// TestManifestValidateAcceptsAWellFormedPackage is the control for every
// rejection below: each case mutates exactly one thing about this package, so a
// refusal is evidence about that field rather than about the fixture.
func TestManifestValidateAcceptsAWellFormedPackage(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha\n"), Existing(HashBytes([]byte("old\n"))))
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed package: %v", err)
	}
	if len(manifest.Payloads) != 1 {
		t.Errorf("a package with one write carries one payload, got %d", len(manifest.Payloads))
	}
	if got := manifest.Paths(); len(got) != 2 {
		t.Errorf("Paths() = %v, want both changed paths", got)
	}
	if !manifest.Changes()[1].Tombstone() {
		t.Error("the delete is not reported as a tombstone")
	}
}

// The identities below are well-formed and belong to nothing in the control
// package, so a refusal that names one is a refusal about the field it was put
// in rather than about the identity being unreadable.
var (
	otherDeviceID     = devicePrefix + strings.Repeat("c", identityHexLen)
	absentOperationID = operationPrefix + strings.Repeat("d", identityHexLen)
)

// appendOperation adds a second operation boundary to a control package and
// returns its identity, for the cases that are only visible across two of them.
func appendOperation(m *Manifest, path string) string {
	id := operationPrefix + strings.Repeat("e", identityHexLen)
	m.Operations = append(m.Operations, Operation{
		OperationID: id,
		RecordedAt:  m.Operations[0].RecordedAt,
		Changes: []Change{{
			Path:         path,
			Action:       ActionDelete,
			Predecessors: []string{},
			ExpectExists: true,
			ExpectSHA256: HashBytes([]byte("gamma\n")),
		}},
	})
	return id
}

// rejection is one mutation of the control package and the sentinel Validate
// must answer with. The tables below are grouped by that sentinel because the
// sentinel is what a caller branches on: a malformed package is a producer bug,
// a private path is a producer applying a different inclusion policy, and a
// collision is a package that is well-formed everywhere except on the vault
// about to apply it.
type rejection struct {
	name   string
	want   error
	mutate func(*Manifest)
}

// runRejections gives every case its own copy of the control package, so one
// case's mutation cannot be what makes the next one fail.
func runRejections(t *testing.T, cases []rejection) {
	t.Helper()
	r := enroll(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, _ := r.draft(t, func(b *Builder) {
				b.Write("Alpha.md", []byte("alpha\n"), Absent())
				b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
			})
			tc.mutate(manifest)
			requireIs(t, manifest.Validate(), tc.want, tc.name)
		})
	}
}

// TestManifestValidateRejectsAMalformedHeader covers the fields that address a
// package. The format token is judged first and alone: a manifest written by a
// build this one does not know may mean anything by the fields it shares.
func TestManifestValidateRejectsAMalformedHeader(t *testing.T) {
	runRejections(t, []rejection{
		{
			name: "a format token this build does not know",
			want: ErrUnsupportedFormat,
			mutate: func(m *Manifest) {
				m.Format = "brainnotes-sync-v2"
			},
		},
		{
			name: "no format token at all",
			want: ErrUnsupportedFormat,
			mutate: func(m *Manifest) {
				m.Format = ""
			},
		},
		{
			name: "a device identity in the vault field",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.VaultID = m.OriginDevice
			},
		},
		{
			name: "epoch zero",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Epoch = 0
			},
		},
		{
			name: "sequence zero",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Sequence = 0
			},
		},
		{
			name: "a dependency on its own origin at or after itself",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Requires = []Dependency{{OriginDevice: m.OriginDevice, Sequence: m.Sequence}}
			},
		},
		{
			name: "a package that changes nothing",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations = nil
			},
		},
		{
			// Nothing orders on created_at — that is Sequence and Predecessors —
			// but an unset one is a manifest nobody built, and it is the field a
			// person reads first when working out what a stuck package is.
			name: "no build time at all",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.CreatedAt = time.Time{}
			},
		},
		{
			name: "a dependency on a device identity that is not one",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Requires = []Dependency{{OriginDevice: "pi", Sequence: 1}}
			},
		},
		{
			// Sequences start at 1, so a zero is an unset field rather than a
			// position. Accepting it would make "depends on nothing" and "depends
			// on the beginning of that device's stream" the same manifest.
			name: "a dependency at sequence zero",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Requires = []Dependency{{OriginDevice: otherDeviceID, Sequence: 0}}
			},
		},
	})
}

// TestBuilderRecordsDependencies is the other half of the header checks: the
// dependencies a producer declares survive into the manifest unchanged, in the
// order it declared them. A package held for an unmet dependency is held on
// exactly these two fields.
func TestBuilderRecordsDependencies(t *testing.T) {
	r := enroll(t)
	builder, err := NewBuilder(r.peer, 4, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	manifest, _, err := builder.
		Require(r.local.DeviceID, 2).
		Require(r.local.DeviceID, 3).
		BeginOperation("", r.clock.Tick()).
		Write("Alpha.md", []byte("alpha\n"), Absent()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []Dependency{
		{OriginDevice: r.local.DeviceID, Sequence: 2},
		{OriginDevice: r.local.DeviceID, Sequence: 3},
	}
	if len(manifest.Requires) != len(want) {
		t.Fatalf("Requires = %v, want %v", manifest.Requires, want)
	}
	for i, dep := range want {
		if manifest.Requires[i] != dep {
			t.Errorf("Requires[%d] = %v, want %v", i, manifest.Requires[i], dep)
		}
	}
}

// TestValidateRefusesANilManifest covers the caller that has an error and a nil
// pointer and checks only one of them. Validate is the first thing every reader
// runs, so it answers rather than panicking — and it answers with the sentinel
// that says "this is not a package", because a nil manifest is not a package.
func TestValidateRefusesANilManifest(t *testing.T) {
	t.Parallel()

	var m *Manifest
	requireIs(t, m.Validate(), ErrMalformedPackage, "a nil manifest")
}

// TestManifestValidateRejectsAMalformedOperation covers operation identity and
// the causal metadata hanging off each change — the fields an applier orders by,
// and therefore the ones it must never have to guess about.
func TestManifestValidateRejectsAMalformedOperation(t *testing.T) {
	runRejections(t, []rejection{
		{
			name: "an operation that changes nothing",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes = nil
			},
		},
		{
			name: "one operation identity used twice",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations = append(m.Operations, m.Operations[0])
			},
		},
		{
			name: "an operation that is its own causal predecessor",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Predecessors = []string{m.Operations[0].OperationID}
			},
		},
		{
			name: "a predecessor that is not an operation identity",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Predecessors = []string{m.PackageID}
			},
		},
		{
			name: "an expectation that a file existed with no hash for it",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].ExpectExists = true
				m.Operations[0].Changes[0].ExpectSHA256 = ""
			},
		},
		{
			name: "an expectation that a file was absent carrying a hash anyway",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].ExpectExists = false
				m.Operations[0].Changes[0].ExpectSHA256 = HashBytes([]byte("whatever"))
			},
		},
		{
			name: "an unknown action",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Action = "rename"
			},
		},
		{
			// RecordedAt is when the operation happened on its origin. An unset
			// one is an operation nobody performed, and it is what a person
			// reconciling two replicas reads to find out which edit came first.
			name: "an operation that never happened",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].RecordedAt = time.Time{}
			},
		},
		{
			// Duplicated rather than merged, because a producer that emitted the
			// same edge twice does not know what its own causal history is, and
			// silently deduplicating would hide that from the one build that can
			// still tell somebody.
			name: "one causal edge listed twice",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Predecessors = []string{absentOperationID, absentOperationID}
			},
		},
		{
			// The edge nothing can satisfy. An applier walks a manifest's
			// operations in order, so an edge from the first onto the second is a
			// wait that never ends — or, in an applier that does not wait, an
			// ordering constraint dropped with nobody told.
			name: "a causal edge onto an operation this package orders later",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				later := appendOperation(m, "Gamma.md")
				m.Operations[0].Changes[0].Predecessors = []string{later}
			},
		},
	})
}

// TestValidateAcceptsABackwardEdgeInsideOnePackage is the case the check above
// must not catch, and the reason it is written as an index comparison rather
// than as "no predecessor may name an operation in this package".
//
// One logical change split across two boundaries — a rename, then the repair of
// every wikilink that pointed at the old name — travels as two operations in one
// package, and the repair naming the rename is exactly how their order survives
// the trip. TestBuilderPreservesOperationBoundaries builds that package; this
// states that Validate agrees it is one.
func TestValidateAcceptsABackwardEdgeInsideOnePackage(t *testing.T) {
	r := enroll(t)
	rename := mustID(t, NewOperationID)

	builder, err := NewBuilder(r.peer, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	manifest, _, err := builder.
		BeginOperation(rename, r.clock.Tick()).
		Write("Notes/Renamed.md", []byte("body\n"), Absent()).
		BeginOperation("", r.clock.Tick()).
		Write("Alpha.md", []byte("repaired\n"), Absent(), rename).
		Build()
	if err != nil {
		t.Fatalf("Build rejected a backward edge inside one package: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate rejected a backward edge inside one package: %v", err)
	}
}

// TestValidateAcceptsAPredecessorFromAnEarlierPackage is the other half of the
// backward-edge rule, and the half the rejection table cannot state.
//
// Causality between packages is the only kind that matters over time: the edge
// that says "this edit follows the rename you applied last week" names an
// operation that is not in this package and never will be. The index comparison
// is deliberately silent about such an edge — it constrains only operations it
// can see — and the one place absentOperationID appears in the rejection table
// is a row about a *duplicated* edge, which would pass just as well if every
// out-of-package predecessor were refused outright.
//
// So state it: tightening predecessorOrder to require that every predecessor
// resolve inside this manifest would break every cross-package causal edge in
// the protocol, and without this test the whole suite would stay green.
func TestValidateAcceptsAPredecessorFromAnEarlierPackage(t *testing.T) {
	r := enroll(t)

	builder, err := NewBuilder(r.peer, 2, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	manifest, _, err := builder.
		Require(r.peer.DeviceID, 1).
		BeginOperation("", r.clock.Tick()).
		Write("Alpha.md", []byte("follows the rename\n"), Absent(), absentOperationID).
		Build()
	if err != nil {
		t.Fatalf("Build rejected an edge onto an operation in an earlier package: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate rejected an edge onto an operation in an earlier package: %v", err)
	}
	if got := manifest.Changes()[0].Predecessors; len(got) != 1 || got[0] != absentOperationID {
		t.Errorf("the causal edge did not survive Build: %v", got)
	}
}

// TestValidateAcceptsCausalEdgesOnATombstone says a delete is ordered the same
// way a write is.
//
// Every other predecessor assertion in this file is on a write, and the edge
// checks are action-independent by construction — manifestScan.change runs the
// predecessor scan before it looks at the action at all — so nothing can
// currently hide behind ActionDelete. That is a property of today's control
// flow, not a stated guarantee, and the guarantee is the load-bearing one: a
// delete that could not carry causality would be a delete no applier could order
// against a concurrent edit to the same path, which is the entire reason this
// protocol carries tombstones instead of inferring absence.
func TestValidateAcceptsCausalEdgesOnATombstone(t *testing.T) {
	r := enroll(t)
	rename := mustID(t, NewOperationID)

	builder, err := NewBuilder(r.peer, 2, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	manifest, _, err := builder.
		Require(r.peer.DeviceID, 1).
		BeginOperation(rename, r.clock.Tick()).
		Write("Notes/Renamed.md", []byte("body\n"), Absent()).
		BeginOperation("", r.clock.Tick()).
		// Both directions at once: the rename this package carries, and an
		// operation from a package applied before it.
		Delete("Notes/Original.md", Existing(HashBytes([]byte("body\n"))), rename, absentOperationID).
		Build()
	if err != nil {
		t.Fatalf("Build rejected a tombstone carrying causal edges: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate rejected a tombstone carrying causal edges: %v", err)
	}
	deletion := manifest.Changes()[1]
	if !deletion.Tombstone() {
		t.Fatalf("the second change is not a tombstone: %+v", deletion)
	}
	if len(deletion.Predecessors) != 2 {
		t.Errorf("the tombstone carries %d causal edges, want 2", len(deletion.Predecessors))
	}
}
