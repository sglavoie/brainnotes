package vaultsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
)

// TestAcceptTakesACompletePackageFromAnEnrolledPeer is the control: everything
// below removes exactly one of the properties this asserts.
func TestAcceptTakesACompletePackageFromAnEnrolledPeer(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	body := []byte("alpha body\n")

	manifest, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", body, Absent())
	})

	accepted, err := Accept(r.local, dir)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.PackageID != manifest.PackageID {
		t.Errorf("Accept returned %q, want %q", accepted.PackageID, manifest.PackageID)
	}
	data, err := ReadPayload(dir, accepted, accepted.Changes()[0].Payload)
	if err != nil {
		t.Fatalf("ReadPayload: %v", err)
	}
	if string(data) != string(body) {
		t.Errorf("payload holds %q, want %q", data, body)
	}
}

// TestAcceptRefusesTheWrongVault covers the transport being pointed at a channel
// belonging to somebody else's vault entirely.
func TestAcceptRefusesTheWrongVault(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	_, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha\n"), Absent())
	})

	other := vaultfixture.New(t, vaultfixture.Options{Name: "unrelated"})
	if _, err := Establish(other.Root, Options{Label: "unrelated", Now: r.clock.Tick}); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	// The producing device is enrolled here too, so the refusal is about the
	// vault and not about the device: a device list that happens to overlap
	// must not admit a package from another vault's history.
	stranger, err := RecordPeer(other.Root, Peer{DeviceID: r.peer.DeviceID, Label: "pi"})
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}

	_, err = Accept(stranger, dir)
	requireIs(t, err, ErrWrongVault, "a package from another vault")
}

func TestAcceptRefusesAnUnenrolledOrigin(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	_, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha\n"), Absent())
	})

	forgotten := r.local.clone()
	forgotten.Peers = nil
	_, err := Accept(forgotten, dir)
	requireIs(t, err, ErrUnknownPeer, "a package from a device this vault did not enroll")

	// The transport delivering a channel back to the device that filled it is
	// the same refusal, and is worth its own case: a device that accepted its
	// own packages would replay its whole history against itself.
	_, err = Accept(r.peer, dir)
	requireIs(t, err, ErrUnknownPeer, "a package delivered back to its own origin")
}

func TestAcceptRefusesAnotherEpoch(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	_, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha\n"), Absent())
	})

	moved := r.local.clone()
	moved.Epoch = r.local.Epoch + 1
	_, err := Accept(moved, dir)
	requireIs(t, err, ErrStaleEpoch, "a package produced before this vault was re-enrolled")

	ahead := r.peer.clone()
	ahead.Epoch = r.peer.Epoch + 1
	builder, err := NewBuilder(ahead, 2, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	_, next, err := builder.
		BeginOperation("", r.clock.Now()).
		Write("Beta.md", []byte("beta\n"), Absent()).
		PublishTo(channel)
	if err != nil {
		t.Fatalf("PublishTo: %v", err)
	}
	_, err = Accept(r.local, next)
	requireIs(t, err, ErrEpochAhead, "a package from an epoch this vault has not learned about")
}

// TestAcceptChecksEnrollmentBeforeBytes pins the order the three checks run in.
// A package addressed to another vault is not this vault's to hash, and a
// receiver that verified payloads first would spend the transfer cost of every
// misrouted package before noticing it was misrouted.
func TestAcceptChecksEnrollmentBeforeBytes(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha\n"), Absent())
	})
	removePayload(t, dir, manifest.Payloads[0].Name)

	stranger := r.local.clone()
	stranger.VaultID = mustID(t, NewVaultID)
	_, err := Accept(stranger, dir)
	requireIs(t, err, ErrWrongVault, "a misrouted package that is also incomplete")
}

func TestAcceptRefusesAnIncompletePackage(t *testing.T) {
	r := enroll(t)

	cases := []struct {
		name   string
		damage func(t *testing.T, dir string, m *Manifest)
	}{
		{
			name: "the manifest has not landed yet",
			damage: func(t *testing.T, dir string, _ *Manifest) {
				if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
					t.Fatalf("remove manifest: %v", err)
				}
			},
		},
		{
			name: "a payload has not landed yet",
			damage: func(t *testing.T, dir string, m *Manifest) {
				removePayload(t, dir, m.Payloads[0].Name)
			},
		},
		{
			name: "a payload is still being written",
			damage: func(t *testing.T, dir string, m *Manifest) {
				writePayload(t, dir, m.Payloads[0].Name, []byte("half"))
			},
		},
		{
			name: "a payload arrived with the wrong bytes",
			damage: func(t *testing.T, dir string, m *Manifest) {
				writePayload(t, dir, m.Payloads[0].Name, []byte("tampered!!\n"))
			},
		},
		{
			name: "the transport left its own temporary file behind",
			damage: func(t *testing.T, dir string, _ *Manifest) {
				writePayload(t, dir, ".syncthing.tmp", []byte("in flight"))
			},
		},
		{
			name: "a payload is a symlink to somewhere else",
			damage: func(t *testing.T, dir string, m *Manifest) {
				path := filepath.Join(dir, PayloadsName, m.Payloads[0].Name)
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove payload: %v", err)
				}
				if err := os.Symlink("/etc/hosts", path); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := r.channel(t)
			manifest, dir := r.publish(t, channel, func(b *Builder) {
				b.Write("Alpha.md", []byte("alpha body\n"), Absent())
			})
			tc.damage(t, dir, manifest)
			_, err := Accept(r.local, dir)
			requireIs(t, err, ErrIncompletePackage, tc.name)
		})
	}
}

// TestReadManifestRefusesBytesThatAreNotAPackage separates "not here yet" from
// "not a package": an unparseable or unsupported manifest is a decision, not
// something to keep waiting on.
func TestReadManifestRefusesBytesThatAreNotAPackage(t *testing.T) {
	r := enroll(t)

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"bytes that are not json", []byte("{not json"), ErrMalformedPackage},
		{"a format this build does not know", []byte(`{"format":"brainnotes-sync-v9"}`), ErrUnsupportedFormat},
		{"a package that changes nothing", []byte(`{"format":"` + Format + `"}`), ErrMalformedPackage},
		{
			// The case the format token is peeked at for. A later format is free
			// to change what a field *means*: here epoch is a string and
			// operations is an object. Decoding the whole record first would fail
			// on the type mismatch and report damage — sending a person looking
			// for a corrupt file when what they have is a newer peer. The remedy
			// differs too: damage is discarded, a newer format is left in the
			// channel for the build that knows it.
			"a later format whose fields hold different types",
			[]byte(`{"format":"brainnotes-sync-v2","epoch":"2026-Q1","operations":{"rename":["Alpha.md"]},"payloads":7}`),
			ErrUnsupportedFormat,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := r.channel(t)
			_, dir := r.publish(t, channel, func(b *Builder) {
				b.Write("Alpha.md", []byte("alpha\n"), Absent())
			})
			if err := os.WriteFile(filepath.Join(dir, ManifestName), tc.data, 0o644); err != nil {
				t.Fatalf("overwrite manifest: %v", err)
			}
			_, err := Accept(r.local, dir)
			requireIs(t, err, tc.want, tc.name)
		})
	}
}

// TestReadPayloadRehashes is why applying a package re-reads rather than trusting
// the Accept that ran a moment ago: the transport is still running.
func TestReadPayloadRehashes(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})
	if _, err := Accept(r.local, dir); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	name := manifest.Payloads[0].Name
	writePayload(t, dir, name, []byte("rewritten!\n"))
	_, err := ReadPayload(dir, manifest, name)
	requireIs(t, err, ErrIncompletePackage, "payload bytes that changed after acceptance")

	_, err = ReadPayload(dir, manifest, "nothing.bin")
	requireIs(t, err, ErrMalformedPackage, "a payload the inventory does not list")
}

func removePayload(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, PayloadsName, name)); err != nil {
		t.Fatalf("remove payload %q: %v", name, err)
	}
}

func writePayload(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, PayloadsName, name), data, 0o644); err != nil {
		t.Fatalf("write payload %q: %v", name, err)
	}
}
