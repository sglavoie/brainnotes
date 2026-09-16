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

// TestVerifyAgainstRefusesAReceiptThatIsNotEvidence is the producer's side. Each
// case is a receipt the producer must not act on, because acting means dropping
// the only remaining copy of a change.
func TestVerifyAgainstRefusesAReceiptThatIsNotEvidence(t *testing.T) {
	r := enroll(t)

	cases := []struct {
		name   string
		want   error
		mutate func(*Receipt)
	}{
		{
			name: "a receipt from a device this vault did not enroll",
			want: ErrUnknownPeer,
			mutate: func(receipt *Receipt) {
				receipt.Device = mustIDFor(NewDeviceID)
			},
		},
		{
			name: "a receipt answering a package from some other device",
			want: ErrReceiptMismatch,
			mutate: func(receipt *Receipt) {
				receipt.OriginDevice = mustIDFor(NewDeviceID)
			},
		},
		{
			name: "a receipt answering a different package",
			want: ErrReceiptMismatch,
			mutate: func(receipt *Receipt) {
				receipt.PackageID = mustIDFor(NewPackageID)
			},
		},
		{
			name: "a receipt claiming a different position in this device's stream",
			want: ErrReceiptMismatch,
			mutate: func(receipt *Receipt) {
				receipt.OriginSequence = receipt.OriginSequence + 1
			},
		},
		{
			name: "a receipt for another vault",
			want: ErrWrongVault,
			mutate: func(receipt *Receipt) {
				receipt.VaultID = mustIDFor(NewVaultID)
			},
		},
		{
			name: "a receipt from an epoch this device has not reached",
			want: ErrEpochAhead,
			mutate: func(receipt *Receipt) {
				receipt.Epoch++
			},
		},
		{
			name: "a receipt with no recovery entry",
			want: ErrNoRecoveryEntry,
			mutate: func(receipt *Receipt) {
				receipt.RecoveryEntry = ""
			},
		},
		{
			name: "a receipt in a format this build does not know",
			want: ErrUnsupportedFormat,
			mutate: func(receipt *Receipt) {
				receipt.Format = "brainnotes-sync-v9"
			},
		},
		{
			name: "a receipt a device wrote for its own package",
			want: ErrReceiptMismatch,
			mutate: func(receipt *Receipt) {
				receipt.Device = receipt.OriginDevice
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, _ := r.draft(t, func(b *Builder) {
				b.Write("Alpha.md", []byte("alpha body\n"), Absent())
			})
			receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
			if err != nil {
				t.Fatalf("Acknowledge: %v", err)
			}
			if err := receipt.VerifyAgainst(manifest, r.peer); err != nil {
				t.Fatalf("the unmutated receipt is not evidence: %v", err)
			}
			tc.mutate(receipt)
			requireIs(t, receipt.VerifyAgainst(manifest, r.peer), tc.want, tc.name)
		})
	}
}

// TestVerifyAgainstRefusesAReceiptFromASupersededEpoch is the restore-from-
// backup case on the producer's side: this device re-enrolled after publishing,
// so the receipt acknowledges an application to a history that no longer exists.
func TestVerifyAgainstRefusesAReceiptFromASupersededEpoch(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})
	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	reenrolled := r.peer.clone()
	reenrolled.Epoch = r.peer.Epoch + 1
	requireIs(t, receipt.VerifyAgainst(manifest, reenrolled), ErrStaleEpoch, "a receipt from before this device re-enrolled")
}

func TestReadReceiptSeparatesAbsentFromUnusable(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})

	_, err := ReadReceipt(channel, manifest.PackageID)
	requireIs(t, err, ErrIncompletePackage, "a receipt the peer has not published yet")

	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	path, err := PublishReceipt(channel, receipt)
	if err != nil {
		t.Fatalf("PublishReceipt: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("overwrite receipt: %v", err)
	}
	_, err = ReadReceipt(channel, manifest.PackageID)
	requireIs(t, err, ErrReceiptMismatch, "bytes that are not a receipt")
}

// TestReadReceiptTrustsTheFieldOverTheFileName covers the one thing ReadReceipt
// does that a plain file read would not: it compares the package identity the
// receipt claims against the one the caller asked about.
//
// The name is how the receipt was found, not what it says. A transport that
// delivered a file under the wrong name — or a peer that published one — would
// otherwise hand a producer an acknowledgment of some other package as though it
// answered the one it is about to drop its copy of.
func TestReadReceiptTrustsTheFieldOverTheFileName(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})
	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if _, err := PublishReceipt(channel, receipt); err != nil {
		t.Fatalf("PublishReceipt: %v", err)
	}

	// The same bytes, valid in every other respect, sitting under another
	// package's name.
	data, err := os.ReadFile(receiptPath(channel, receipt.PackageID))
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	other := mustIDFor(NewPackageID)
	if err := os.WriteFile(receiptPath(channel, other), data, 0o644); err != nil {
		t.Fatalf("write misnamed receipt: %v", err)
	}

	_, err = ReadReceipt(channel, other)
	requireIs(t, err, ErrReceiptMismatch, "a receipt filed under a package it does not acknowledge")

	// And the correctly named copy still reads, so the refusal above is about the
	// mismatch and not about the bytes.
	if _, err := ReadReceipt(channel, receipt.PackageID); err != nil {
		t.Errorf("the receipt under its own name no longer reads: %v", err)
	}
}

// TestReadReceiptSeparatesDamageFromANewerPeer is the receipt side of the format
// peek. Bytes that are not a receipt are damage to discard; a receipt in a later
// format is a peer this build cannot answer for, and the two get different
// errors because they have different remedies.
func TestReadReceiptSeparatesDamageFromANewerPeer(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})
	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	path, err := PublishReceipt(channel, receipt)
	if err != nil {
		t.Fatalf("PublishReceipt: %v", err)
	}

	// A later format is free to change what a field means: applied is a string
	// here and epoch is an object. Decoding first would report a mangled receipt
	// when what is on disk is a newer peer's perfectly good one.
	later := `{"format":"brainnotes-sync-v2","applied":"all of it","epoch":{"generation":4}}`
	if err := os.WriteFile(path, []byte(later), 0o644); err != nil {
		t.Fatalf("overwrite receipt: %v", err)
	}
	_, err = ReadReceipt(channel, receipt.PackageID)
	requireIs(t, err, ErrUnsupportedFormat, "a receipt from a build this one does not know")
}

// TestVerifyAgainstRefusesAReceiptWithNoPackage pins the nil manifest as a
// refusal rather than a check that quietly passes. VerifyAgainst is asked
// whether the producer may drop its own copy of a change, and the manifest is
// the only argument that says which change that is: a caller whose lookup
// returned nothing must be told no, not told yes for every package at once.
func TestVerifyAgainstRefusesAReceiptWithNoPackage(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})
	receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if err := receipt.VerifyAgainst(manifest, r.peer); err != nil {
		t.Fatalf("the unmutated receipt is not evidence: %v", err)
	}
	requireIs(t, receipt.VerifyAgainst(nil, r.peer), ErrReceiptMismatch, "a receipt with no package to answer")

	// The mirrored omission: no local enrollment to judge it against.
	requireIs(t, receipt.VerifyAgainst(manifest, nil), ErrNotEnrolled, "a receipt judged by a vault that is not enrolled")
}

// TestVerifyAgainstRefusesAReceiptThatDoesNotCoverThePackage is the producer's
// half of the coverage check, and it exists because the acknowledging half
// cannot stand in for it.
//
// Acknowledge runs coversPackage against an Application held in memory on the
// device that did the applying, which makes the receipts *this build* mints
// honest and says nothing about the receipt a producer is holding. That one
// arrived as a file, across a transport, possibly from another build or from a
// crash between two of the three paths. Every mutation below leaves a receipt
// whose format, identities, epoch, package and sequence all still match — so
// without the coverage check in VerifyAgainst each one of them verifies, and the
// producer drops its only copy of a change that landed nowhere.
func TestVerifyAgainstRefusesAReceiptThatDoesNotCoverThePackage(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
		b.Write("Notes/Gamma.md", []byte("gamma body\n"), Absent())
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})

	cases := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{
			name:   "a receipt that acknowledges nothing at all",
			mutate: func(r *Receipt) { r.Applied = nil },
		},
		{
			name:   "a receipt that stops partway through the package",
			mutate: func(r *Receipt) { r.Applied = r.Applied[:1] },
		},
		{
			name: "a receipt whose path holds bytes the package did not carry",
			mutate: func(r *Receipt) {
				r.Applied[0].SHA256 = HashBytes([]byte("something else\n"))
			},
		},
		{
			name: "a receipt that reports the deletion as a write",
			mutate: func(r *Receipt) {
				for i := range r.Applied {
					if r.Applied[i].Action == ActionDelete {
						r.Applied[i].Action = ActionWrite
					}
				}
			},
		},
		{
			name: "a receipt naming a path the package never changed",
			mutate: func(r *Receipt) {
				r.Applied[0].Path = "Somewhere/Else.md"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receipt, err := Acknowledge(r.local, manifest, applicationOf(r, manifest))
			if err != nil {
				t.Fatalf("Acknowledge: %v", err)
			}
			if err := receipt.VerifyAgainst(manifest, r.peer); err != nil {
				t.Fatalf("the unmutated receipt is not evidence: %v", err)
			}
			tc.mutate(receipt)

			// Stated rather than assumed: the mutation must leave a receipt that
			// is still internally consistent, or it would be refused by Validate
			// and this case would prove nothing about coverage.
			if err := receipt.Validate(); err != nil {
				t.Fatalf("the mutation broke the receipt itself, so the coverage check is never reached: %v", err)
			}
			requireIs(t, receipt.VerifyAgainst(manifest, r.peer), ErrIncompleteApplication, tc.name)
		})
	}
}

// TestReadReceiptRefusesAnIdentityThatIsAPath is receiptPath's stated
// precondition made true at the one exported door that did not enforce it.
//
// PublishReceipt validates the whole receipt before joining its package identity
// onto a directory; ReadReceipt takes the identity as an argument. Every
// in-repository caller passes one it got from a mint or a directory entry, so
// nothing is exploitable today — which is exactly why this is worth pinning
// rather than shrugging at. The comment on receiptPath says the name cannot be a
// path, and the next caller will read that as a guarantee it may lean on.
func TestReadReceiptRefusesAnIdentityThatIsAPath(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)

	for _, packageID := range []string{
		"../../../etc/passwd",
		"pkg-../escape",
		"/absolute",
		"",
		"not-a-package-identity",
	} {
		_, err := ReadReceipt(channel, packageID)
		requireIs(t, err, ErrReceiptMismatch, "ReadReceipt("+packageID+")")
	}
}

func mustIDFor(mint func() (string, error)) string {
	id, err := mint()
	if err != nil {
		panic(err)
	}
	return id
}
