package vaultsync

import (
	"os"
	"testing"
)

const testRecoveryEntry = "2026-03-04T05-06-07Z-a1b2c3"

// applicationOf is the honest application of m: every path it changes, with the
// content the package said that path would hold. Each refusal test below takes
// this and removes one truthful thing from it.
func applicationOf(r *replicas, m *Manifest) Application {
	changes := m.Changes()
	applied := make([]AppliedPath, 0, len(changes))
	for _, change := range changes {
		applied = append(applied, AppliedPath{
			Path:   change.Path,
			Action: change.Action,
			SHA256: change.ResultSHA256,
		})
	}
	return Application{RecoveryEntry: testRecoveryEntry, Applied: applied, Now: r.clock.Tick}
}

// TestAcknowledgeNamesWhatWasApplied covers what a receipt says: which device
// applied which of the other device's packages, and the local recovery entry
// that can undo it.
func TestAcknowledgeNamesWhatWasApplied(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.publish(t, r.channel(t), func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})
	if _, err := Accept(r.local, dir); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if receipt.Device != r.local.DeviceID || receipt.OriginDevice != r.peer.DeviceID {
		t.Errorf("receipt names the wrong devices: applied by %q, answering %q", receipt.Device, receipt.OriginDevice)
	}
	if receipt.OriginSequence != manifest.Sequence {
		t.Errorf("OriginSequence = %d, want %d", receipt.OriginSequence, manifest.Sequence)
	}
	if receipt.RecoveryEntry != testRecoveryEntry {
		t.Errorf("RecoveryEntry = %q, want the entry that can undo the application", receipt.RecoveryEntry)
	}
}

// TestAcknowledgeNamesConvergenceInsteadOfARecoveryEntry covers the other
// honest receipt shape: every packaged result was already on disk, so there is
// no application to undo and no recovery entry to name. The complete Applied
// list is still required and is what lets the producer verify the claim.
func TestAcknowledgeNamesConvergenceInsteadOfARecoveryEntry(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})
	application := applicationOf(r, manifest)
	application.RecoveryEntry = ""
	application.Converged = true

	receipt, err := Acknowledge(r.local, manifest, application)
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if !receipt.Converged || receipt.RecoveryEntry != "" {
		t.Fatalf("converged receipt is %+v, want no recovery entry", receipt)
	}
	if err := receipt.VerifyAgainst(manifest, r.peer); err != nil {
		t.Fatalf("the producer did not recognize the converged receipt: %v", err)
	}
}

// TestAcknowledgeRoundTrip is the receipt's whole purpose in one flow: the
// receiver publishes what it owes, and the producer reads that answer back and
// recognizes it as its own.
func TestAcknowledgeRoundTrip(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})
	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	// The acknowledging device answers through its own outgoing channel, which
	// is the only thing the two devices share.
	back := r.channel(t)
	path, err := PublishReceipt(back, receipt)
	if err != nil {
		t.Fatalf("PublishReceipt: %v", err)
	}
	reread, err := ReadReceipt(back, manifest.PackageID)
	if err != nil {
		t.Fatalf("ReadReceipt: %v", err)
	}
	if reread.PackageID != receipt.PackageID || reread.RecoveryEntry != receipt.RecoveryEntry {
		t.Errorf("the receipt on disk is not the one published:\n%+v\n%+v", reread, receipt)
	}
	if err := reread.VerifyAgainst(manifest, r.peer); err != nil {
		t.Errorf("the producer did not recognize its own receipt: %v", err)
	}

	// And it is immutable for the same reason a package is: the producer may
	// already have dropped its copy of the change on the strength of it.
	_, err = PublishReceipt(back, receipt)
	requireIs(t, err, ErrPackageExists, "republishing one receipt")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the refused republication disturbed the receipt: %v", err)
	}
}

// TestAcknowledgeRefusesAClaimItCannotStandBehind covers the three ways a
// receipt would be a lie the peer acts on by discarding its only copy of a
// change.
func TestAcknowledgeRefusesAClaimItCannotStandBehind(t *testing.T) {
	r := enroll(t)

	cases := []struct {
		name   string
		want   error
		mutate func(*Application)
	}{
		{
			name: "an application with no recovery entry behind it",
			want: ErrNoRecoveryEntry,
			mutate: func(a *Application) {
				a.RecoveryEntry = ""
			},
		},
		{
			name: "a recovery entry that is only whitespace",
			want: ErrNoRecoveryEntry,
			mutate: func(a *Application) {
				a.RecoveryEntry = "   "
			},
		},
		{
			name: "a converged application that names a recovery entry",
			want: ErrReceiptMismatch,
			mutate: func(a *Application) {
				a.Converged = true
			},
		},
		{
			name: "an application that skipped a path",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied = a.Applied[:1]
			},
		},
		{
			name: "an application that acknowledged nothing",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied = nil
			},
		},
		{
			name: "an application that touched a path the package does not change",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied = append(a.Applied, AppliedPath{Path: "Gamma.md", Action: ActionDelete})
			},
		},
		{
			name: "one path acknowledged twice",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied = append(a.Applied, a.Applied[0])
			},
		},
		{
			name: "a path that holds something other than what the package said",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied[0].SHA256 = HashBytes([]byte("something else\n"))
			},
		},
		{
			name: "a write acknowledged as a deletion",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied[0].Action = ActionDelete
			},
		},
		{
			name: "a deletion acknowledged as a write",
			want: ErrIncompleteApplication,
			mutate: func(a *Application) {
				a.Applied[1].Action = ActionWrite
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, _ := r.draft(t, func(b *Builder) {
				b.Write("Alpha.md", []byte("alpha body\n"), Absent())
				b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
			})
			application := applicationOf(r, manifest)
			tc.mutate(&application)
			_, err := Acknowledge(r.local, manifest, application)
			requireIs(t, err, tc.want, tc.name)
		})
	}
}

// TestAcknowledgeRefusesAPackageThisVaultCouldNotHaveApplied closes the loop
// between the two checks: a receipt for a package Accept would have refused is
// itself a refusal, so no path leads to acknowledging what was never applied.
func TestAcknowledgeRefusesAPackageThisVaultCouldNotHaveApplied(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})

	forgotten := r.local.clone()
	forgotten.Peers = nil
	_, err := Acknowledge(forgotten, manifest, applicationOf(r, manifest))
	requireIs(t, err, ErrUnknownPeer, "acknowledging a package from a device this vault did not enroll")

	stranger := r.local.clone()
	stranger.VaultID = mustID(t, NewVaultID)
	_, err = Acknowledge(stranger, manifest, applicationOf(r, manifest))
	requireIs(t, err, ErrWrongVault, "acknowledging another vault's package")
}

func mustIDFor(mint func() (string, error)) string {
	id, err := mint()
	if err != nil {
		panic(err)
	}
	return id
}
