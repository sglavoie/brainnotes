package vaultsync

import (
	"strings"
	"testing"
)

// TestManifestValidateRejectsAMalformedInventory covers the coupling between a
// change and the bytes it names. A tombstone is checked from both sides —
// carrying a payload and carrying resulting content — because a delete that
// smuggles content is a delete the receiver would apply as a write.
func TestManifestValidateRejectsAMalformedInventory(t *testing.T) {
	runRejections(t, []rejection{
		{
			name: "a write naming a payload the inventory does not list",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Payload = "absent.bin"
			},
		},
		{
			name: "a write whose resulting hash is not its payload's",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].ResultSHA256 = HashBytes([]byte("different"))
			},
		},
		{
			name: "a write whose resulting size is not its payload's",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].ResultSize++
			},
		},
		{
			name: "a tombstone carrying a payload",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[1].Payload = m.Payloads[0].Name
			},
		},
		{
			name: "a tombstone carrying resulting content",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[1].ResultSHA256 = HashBytes([]byte("resurrected"))
			},
		},
		{
			// A delete has to name what it is deleting. Without the expectation
			// there is nothing for the receiver to compare the live file against,
			// so the tombstone removes whatever happens to be at the path — which
			// on the other replica may be a note written since the package was
			// built. The receipt would then acknowledge destroying it.
			name: "a tombstone that expects no file to delete",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[1].ExpectExists = false
				m.Operations[0].Changes[1].ExpectSHA256 = ""
			},
		},
		{
			name: "an inventory entry no change names",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Payloads = append(m.Payloads, Payload{
					Name:   "smuggled.bin",
					SHA256: HashBytes([]byte("smuggled")),
					Size:   int64(len("smuggled")),
				})
			},
		},
		{
			name: "a payload name that is a path",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Payloads[0].Name = "../../escape.bin"
				m.Operations[0].Changes[0].Payload = "../../escape.bin"
			},
		},
		{
			name: "a payload with no digest",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Payloads[0].SHA256 = "not-a-digest"
			},
		},
		{
			// Two entries under one name are two answers to "what are these
			// bytes", and the package directory can only hold one file. Whichever
			// entry a reader indexed last would decide, which is not a decision a
			// format should leave to map iteration.
			name: "one payload name listed twice",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Payloads = append(m.Payloads, m.Payloads[0])
			},
		},
		{
			// Size is cross-checked against the digest so a truncated transfer is
			// detectable without hashing. A negative one is not a length at all,
			// and it reaches a reader as the size a file on disk is compared to.
			name: "a payload of negative size",
			want: ErrMalformedPackage,
			mutate: func(m *Manifest) {
				m.Payloads[0].Size = -1
			},
		},
	})
}

// TestManifestValidateRejectsAPrivatePath is the inclusion policy stated as a
// refusal. Three of these are paths the content history tracks on purpose,
// which is exactly why the portable scope has to be its own policy.
func TestManifestValidateRejectsAPrivatePath(t *testing.T) {
	runRejections(t, []rejection{
		{
			name: "the machine-local configuration overlay",
			want: ErrPrivatePath,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "brainnotes.local.toml"
			},
		},
		{
			name: "the brief state the content history does track",
			want: ErrPrivatePath,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = ".bnotes/brief.json"
			},
		},
		{
			name: "an undo journal entry",
			want: ErrPrivatePath,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = ".bnotes/journal/2026-01-01T00-00-00Z-abc/manifest.json"
			},
		},
		{
			name: "a package from the synchronization channel itself",
			want: ErrPrivatePath,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = ".bnotes/sync/outgoing/pkg-x/manifest.json"
			},
		},
		{
			name: "the repository",
			want: ErrPrivatePath,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = ".git/config"
			},
		},
	})
}

// TestManifestValidateRejectsAPathItCannotInterpret covers paths that are not
// vault-relative at all. They are refused rather than cleaned: a producer that
// meant something else should hear about it here instead of having its path
// resolve to somewhere.
func TestManifestValidateRejectsAPathItCannotInterpret(t *testing.T) {
	runRejections(t, []rejection{
		{
			name: "a parent traversal",
			want: ErrPathTraversal,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "../outside.md"
			},
		},
		{
			name: "an interior traversal",
			want: ErrPathTraversal,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "Notes/../../outside.md"
			},
		},
		{
			name: "an absolute path",
			want: ErrPathTraversal,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "/etc/passwd"
			},
		},
		{
			name: "an empty path",
			want: ErrPathTraversal,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = ""
			},
		},
	})
}

// TestManifestValidateRejectsAPathCollision is the two-filesystem case: each of
// these packages is well-formed on the Pi's ext4 and is two writes to one file
// on the Mac's APFS, where the second would silently win.
func TestManifestValidateRejectsAPathCollision(t *testing.T) {
	runRejections(t, []rejection{
		{
			name: "two paths differing only in case",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[1].Path = "alpha.md"
			},
		},
		{
			name: "two paths differing only in Unicode normalization form",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				// Composed and decomposed spellings of the same name, written
				// as escapes rather than literals: the difference is invisible
				// in a diff, and an editor or a tool that normalized this file
				// would otherwise quietly turn the test into a tautology.
				m.Operations[0].Changes[0].Path = "Caf\u00e9.md"
				m.Operations[0].Changes[1].Path = "Cafe\u0301.md"
			},
		},
		{
			name: "two paths differing only in case inside a directory",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "Notes/Gamma.md"
				m.Operations[0].Changes[1].Path = "notes/Gamma.md"
			},
		},
		{
			name: "one path changed twice in one package",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[1].Path = m.Operations[0].Changes[0].Path
			},
		},
		{
			// A name is a file or a directory and not both, so these two changes
			// are one impossible request rather than two paths. A map keyed on
			// whole paths alone sees two distinct keys and lets it through, and
			// the failure then arrives halfway into applying the package.
			name: "a file and a directory under one name, file first",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "Archive"
				m.Operations[0].Changes[1].Path = "Archive/2024.md"
			},
		},
		{
			// Stated in both orders because the two are found by different halves
			// of the check — one by a path meeting a claimed directory, the other
			// by a directory claim meeting a recorded path.
			name: "a file and a directory under one name, directory first",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "Archive/2024.md"
				m.Operations[0].Changes[1].Path = "Archive"
			},
		},
		{
			name: "a file and a directory whose names differ only in case",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "archive"
				m.Operations[0].Changes[1].Path = "Archive/2024.md"
			},
		},
		{
			name: "a file and a grandparent directory under one name",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				m.Operations[0].Changes[0].Path = "Archive"
				m.Operations[0].Changes[1].Path = "Archive/2024/June.md"
			},
		},
		{
			name: "a collision across two operation boundaries",
			want: ErrPathCollision,
			mutate: func(m *Manifest) {
				// A collision is a fact about the package, not about one
				// operation: two operations that each look fine on their own
				// still name one file on the receiving vault.
				m.Operations = append(m.Operations, Operation{
					OperationID: operationPrefix + strings.Repeat("a", identityHexLen),
					RecordedAt:  m.Operations[0].RecordedAt,
					Changes: []Change{{
						Path:         "ALPHA.md",
						Action:       ActionDelete,
						Predecessors: []string{},
						ExpectExists: true,
						ExpectSHA256: HashBytes([]byte("alpha\n")),
					}},
				})
			},
		},
	})
}

// TestBuilderRefusesAChangeOutsideAnOperation pins the one way a producer can
// lose a multi-file operation boundary: adding a change before opening one.
// Operation boundaries are preserved end to end, so a change with no boundary
// is a bug at the producer rather than something to invent a boundary for.
func TestBuilderRefusesAChangeOutsideAnOperation(t *testing.T) {
	r := enroll(t)
	builder, err := NewBuilder(r.peer, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	_, _, err = builder.Write("Alpha.md", []byte("alpha\n"), Absent()).Build()
	requireIs(t, err, ErrMalformedPackage, "a change with no operation boundary")
}

// TestBuilderPreservesOperationBoundaries is the other half: two operations
// stay two, and the identity minted where an operation happened survives into
// the package unchanged.
func TestBuilderPreservesOperationBoundaries(t *testing.T) {
	r := enroll(t)
	rename := mustID(t, NewOperationID)
	repair := mustID(t, NewOperationID)

	builder, err := NewBuilder(r.peer, 7, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	manifest, _, err := builder.
		BeginOperation(rename, r.clock.Tick()).
		Write("Notes/Renamed.md", []byte("body\n"), Absent()).
		Delete("Notes/Original.md", Existing(HashBytes([]byte("body\n")))).
		BeginOperation(repair, r.clock.Tick()).
		Write("Alpha.md", []byte("repaired\n"), Existing(HashBytes([]byte("alpha\n"))), rename).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(manifest.Operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(manifest.Operations))
	}
	if manifest.Operations[0].OperationID != rename || manifest.Operations[1].OperationID != repair {
		t.Errorf("operation identities were not preserved: %q, %q", manifest.Operations[0].OperationID, manifest.Operations[1].OperationID)
	}
	if len(manifest.Operations[0].Changes) != 2 {
		t.Errorf("the rename lost a file: %d changes", len(manifest.Operations[0].Changes))
	}
	if got := manifest.Operations[1].Changes[0].Predecessors; len(got) != 1 || got[0] != rename {
		t.Errorf("per-path predecessors = %v, want [%s]", got, rename)
	}
	if manifest.Sequence != 7 {
		t.Errorf("Sequence = %d, want 7", manifest.Sequence)
	}
}

// TestIdenticalBytesShareOnePayload is why payloads are content-addressed: a
// rename that writes the same body at a new path carries the body once.
func TestIdenticalBytesShareOnePayload(t *testing.T) {
	r := enroll(t)
	body := []byte("shared body\n")
	manifest, payloads := r.draft(t, func(b *Builder) {
		b.Write("Copy A.md", body, Absent())
		b.Write("Copy B.md", body, Absent())
	})
	if len(manifest.Payloads) != 1 || len(payloads) != 1 {
		t.Fatalf("identical bytes were carried twice: %d inventory entries, %d payloads", len(manifest.Payloads), len(payloads))
	}
	changes := manifest.Changes()
	if changes[0].Payload != changes[1].Payload {
		t.Errorf("the two changes name different payloads: %q, %q", changes[0].Payload, changes[1].Payload)
	}
}
