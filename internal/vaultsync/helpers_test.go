package vaultsync

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vaultfixture"
)

// replicas is a pair of disposable vaults enrolled as two replicas of one
// vault, which is the shape every acceptance test needs: a package is only
// meaningful relative to an enrollment, and half the refusals under test are
// refusals to accept from the wrong one.
type replicas struct {
	pair  *vaultfixture.Pair
	clock *vaultfixture.Clock
	local *Enrollment
	peer  *Enrollment
}

// enroll builds the pair and walks the real baseline-enrollment flow: establish
// on one device, hand its invitation to the other, and record the joiner back
// on the first. Nothing here writes an enrollment record by hand, so a test
// that passes is evidence about the flow a person would actually run.
func enroll(t *testing.T) *replicas {
	t.Helper()
	pair := vaultfixture.NewPair(t, nil)
	clock := vaultfixture.NewClock(t)
	opts := Options{Label: "mac", Now: clock.Now}

	local, err := Establish(pair.Local.Root, opts)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	clock.Tick()
	peer, err := Join(pair.Peer.Root, local.Invite(), Options{Label: "pi", Now: clock.Now})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	clock.Tick()
	local, err = RecordPeer(pair.Local.Root, Peer{DeviceID: peer.DeviceID, Label: peer.Label})
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	return &replicas{pair: pair, clock: clock, local: local, peer: peer}
}

// channel is the disposable directory a package is published into. It lives
// under the pair's own parent rather than inside either vault, because a test
// that published into a vault would be asserting about a transport that had
// already written where it must not.
func (r *replicas) channel(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// publish builds and publishes one package from the peer device, which is the
// direction every acceptance test reads: the peer produces, the local vault
// judges.
func (r *replicas) publish(t *testing.T, channelDir string, build func(*Builder)) (*Manifest, string) {
	t.Helper()
	builder, err := NewBuilder(r.peer, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	builder.BeginOperation("", r.clock.Now())
	build(builder)
	manifest, dir, err := builder.PublishTo(channelDir)
	if err != nil {
		t.Fatalf("PublishTo: %v", err)
	}
	return manifest, dir
}

// draft returns a valid manifest and its payloads without publishing them, for
// the tests that mutate one field and assert the refusal.
func (r *replicas) draft(t *testing.T, build func(*Builder)) (*Manifest, map[string][]byte) {
	t.Helper()
	builder, err := NewBuilder(r.peer, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	builder.BeginOperation("", r.clock.Now())
	build(builder)
	manifest, payloads, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return manifest, payloads
}

// requireIs fails unless err is the expected sentinel, and says what it got
// instead. Every rejection in this package is identified by its sentinel rather
// than by its message, so the messages stay free to improve.
func requireIs(t *testing.T, err, want error, context string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected %v, got no error", context, want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s: expected %v, got %v", context, want, err)
	}
}

// mustTime is a fixed instant for the tests that need one without a clock.
func mustTime() time.Time { return vaultfixture.Epoch }
