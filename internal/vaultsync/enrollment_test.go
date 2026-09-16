package vaultsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
)

// TestEstablishMintsABaselineIdentity covers the record itself: a vault that
// synchronizes with nothing yet, addressed by two distinct identities, at the
// first epoch.
func TestEstablishMintsABaselineIdentity(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "mac"})
	clock := vaultfixture.NewClock(t)

	record, err := Establish(v.Root, Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if !validIdentity(vaultPrefix, record.VaultID) || !validIdentity(devicePrefix, record.DeviceID) {
		t.Fatalf("Establish minted %q / %q", record.VaultID, record.DeviceID)
	}
	if record.VaultID == record.DeviceID {
		t.Error("the vault and device identities must not be the same value")
	}
	if record.Epoch != 1 {
		t.Errorf("Epoch = %d, want 1", record.Epoch)
	}
	if !record.EnrolledAt.Equal(vaultfixture.Epoch) || !record.EpochChangedAt.Equal(vaultfixture.Epoch) {
		t.Errorf("timestamps = %v / %v, want the fixture epoch", record.EnrolledAt, record.EpochChangedAt)
	}
	if len(record.Peers) != 0 {
		t.Errorf("a baseline enrollment has no peers, got %d", len(record.Peers))
	}
}

// TestEstablishWritesAVerifiedBaseline is the session's baseline-enrollment
// claim, stated as a test: what Establish returns is what a reader finds on
// disk, and internal/config's own probe agrees the vault is enrolled.
func TestEstablishWritesAVerifiedBaseline(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "mac"})
	clock := vaultfixture.NewClock(t)

	record, err := Establish(v.Root, Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	loaded, err := Load(v.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.equal(record) {
		t.Fatalf("the record on disk is not the record Establish returned:\n%+v\n%+v", loaded, record)
	}

	// The loader's own probe is the reason this path is where it is. A record
	// written somewhere internal/config does not look leaves every enrolled
	// vault reporting itself unenrolled, which silently turns off the field
	// ownership rules session 2 added.
	state, err := config.VaultEnrollment(v.Root)
	if err != nil {
		t.Fatalf("config.VaultEnrollment: %v", err)
	}
	if state != config.EnrollmentEnrolled {
		t.Errorf("config.VaultEnrollment = %q, want %q", state, config.EnrollmentEnrolled)
	}
}

func TestLoadDistinguishesAbsentCorruptAndUnsupported(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "mac"})
	path := EnrollmentPath(v.Root)

	_, err := Load(v.Root)
	requireIs(t, err, ErrNotEnrolled, "a vault that synchronizes with nothing")

	writeEnrollmentBytes(t, path, []byte("{not json"))
	_, err = Load(v.Root)
	requireIs(t, err, ErrCorruptState, "undecodable bytes")

	writeEnrollmentBytes(t, path, []byte(`{"version":99,"vault_id":"x"}`))
	_, err = Load(v.Root)
	requireIs(t, err, ErrUnsupportedVersion, "a record from a newer build")

	// Decodable, current version, and self-contradictory: a device listed as its
	// own peer would accept its own packages back from the transport.
	record := Enrollment{
		Version:  enrollmentVersion,
		VaultID:  mustID(t, NewVaultID),
		DeviceID: mustID(t, NewDeviceID),
		Epoch:    1,
	}
	record.EnrolledAt = mustTime()
	record.EpochChangedAt = mustTime()
	record.Peers = []Peer{{DeviceID: record.DeviceID, JoinedEpoch: 1}}
	writeEnrollmentBytes(t, path, mustJSON(t, record))
	_, err = Load(v.Root)
	requireIs(t, err, ErrCorruptState, "a device enrolled as its own peer")
}

func TestEstablishRefusesAnEnrolledVault(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "mac"})
	clock := vaultfixture.NewClock(t)

	first, err := Establish(v.Root, Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	_, err = Establish(v.Root, Options{Label: "mac", Now: clock.Now})
	requireIs(t, err, ErrAlreadyEnrolled, "a second Establish")

	// And the refusal left the first identity exactly as it was. Replacing it
	// would leave the peer publishing into a channel nobody reads.
	after, err := Load(v.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !after.equal(first) {
		t.Error("the refused Establish changed the vault identity")
	}
}

func TestJoinAdoptsTheInvitedVaultIdentity(t *testing.T) {
	r := enroll(t)

	if r.peer.VaultID != r.local.VaultID {
		t.Errorf("the two replicas disagree about the vault: %q vs %q", r.peer.VaultID, r.local.VaultID)
	}
	if r.peer.DeviceID == r.local.DeviceID {
		t.Error("the two replicas minted the same device identity")
	}
	if r.peer.Epoch != r.local.Epoch {
		t.Errorf("epochs differ after joining: %d vs %d", r.peer.Epoch, r.local.Epoch)
	}
	if _, enrolled := r.peer.Peer(r.local.DeviceID); !enrolled {
		t.Error("the joining device did not record its inviter as a peer")
	}
	if _, enrolled := r.local.Peer(r.peer.DeviceID); !enrolled {
		t.Error("the inviting device did not record the joiner as a peer")
	}
}

func TestJoinRefusesAMalformedInvitation(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "pi"})
	valid := Invitation{
		VaultID:  mustID(t, NewVaultID),
		DeviceID: mustID(t, NewDeviceID),
		Epoch:    1,
	}

	cases := map[string]Invitation{
		"no vault identity":                    {DeviceID: valid.DeviceID, Epoch: 1},
		"a device identity in the vault field": {VaultID: valid.DeviceID, DeviceID: valid.DeviceID, Epoch: 1},
		"no device identity":                   {VaultID: valid.VaultID, Epoch: 1},
		"epoch zero":                           {VaultID: valid.VaultID, DeviceID: valid.DeviceID},
	}
	for name, invite := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Join(v.Root, invite, Options{})
			requireIs(t, err, ErrCorruptState, name)
			// The vault must be left unenrolled specifically, not merely
			// unloadable: a refusal that wrote a half-record would fail this
			// with ErrCorruptState and a bare nil check would call that a pass.
			_, err = Load(v.Root)
			requireIs(t, err, ErrNotEnrolled, "the vault after a refused Join")
		})
	}
}

func TestRecordPeerIsIdempotentAndRefusesSelf(t *testing.T) {
	r := enroll(t)

	again, err := RecordPeer(r.pair.Local.Root, Peer{DeviceID: r.peer.DeviceID, Label: "pi"})
	if err != nil {
		t.Fatalf("RecordPeer twice: %v", err)
	}
	if len(again.Peers) != 1 {
		t.Errorf("recording a known peer added a second entry: %+v", again.Peers)
	}

	_, err = RecordPeer(r.pair.Local.Root, Peer{DeviceID: r.local.DeviceID})
	requireIs(t, err, ErrCorruptState, "enrolling this vault as its own peer")
}

// TestAdvanceEpochInvalidatesInFlightPackages is the restore-from-backup case
// stated end to end: a package the peer published under the old epoch is
// refused once this vault has moved on.
func TestAdvanceEpochInvalidatesInFlightPackages(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, _ := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", []byte("peer body\n"), Absent())
	})

	if err := manifest.CheckEnrollment(r.local); err != nil {
		t.Fatalf("the package should be acceptable before the epoch moves: %v", err)
	}

	advanced, err := AdvanceEpoch(r.pair.Local.Root, Options{Now: r.clock.Tick})
	if err != nil {
		t.Fatalf("AdvanceEpoch: %v", err)
	}
	if advanced.Epoch != r.local.Epoch+1 {
		t.Fatalf("Epoch = %d, want %d", advanced.Epoch, r.local.Epoch+1)
	}
	if advanced.EpochChangedAt.Equal(advanced.EnrolledAt) {
		t.Error("EpochChangedAt did not move with the epoch")
	}
	requireIs(t, manifest.CheckEnrollment(advanced), ErrStaleEpoch, "a package from the previous epoch")

	// The other direction: the peer, still on the old epoch, cannot judge a
	// package this vault publishes under the new one.
	builder, err := NewBuilder(advanced, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	ahead, _, err := builder.BeginOperation("", r.clock.Now()).
		Write("Beta.md", []byte("local body\n"), Absent()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireIs(t, ahead.CheckEnrollment(r.peer), ErrEpochAhead, "a package from a later epoch")
}

// TestEstablishRefusesAnEnrollmentThatDidNotReadBack exercises the read-back
// verification itself, which is otherwise the one step in the write path that
// nothing proves is load-bearing.
//
// The lever is a label that cannot survive the round trip: encoding/json does
// not reject invalid UTF-8, it substitutes U+FFFD. So these bytes validate, they
// write, and they come back as a different record — which is exactly the shape
// of the failure the read-back exists to catch, since every later exchange is
// addressed by whatever is actually on disk rather than by what the caller
// believed it wrote.
func TestEstablishRefusesAnEnrollmentThatDidNotReadBack(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "mac"})
	clock := vaultfixture.NewClock(t)

	record, err := Establish(v.Root, Options{Label: "mac\xff", Now: clock.Now})
	requireIs(t, err, ErrUnverifiedEnrollment, "a label that does not survive being written")
	if record != nil {
		t.Errorf("a refused Establish returned a record anyway: %+v", record)
	}

	// The refusal does not undo the write, and saying so here is the point: the
	// bytes on disk are a valid enrollment holding the substituted label, so a
	// caller that ignored the error would go on addressing this vault by an
	// identity it never verified. The remedy belongs to whoever enrolls, which is
	// why the error names the file.
	loaded, err := Load(v.Root)
	if err != nil {
		t.Fatalf("Load after a failed verification: %v", err)
	}
	if loaded.Label == "mac\xff" {
		t.Fatal("the label round-tripped after all; this test no longer exercises the read-back")
	}
}

func writeEnrollmentBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func mustID(t *testing.T, mint func() (string, error)) string {
	t.Helper()
	id, err := mint()
	if err != nil {
		t.Fatalf("mint identifier: %v", err)
	}
	return id
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return data
}
