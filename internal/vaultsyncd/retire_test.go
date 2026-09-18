package vaultsyncd

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestRetirementLeavesTheExportersReservationAlone covers the one package a
// verified receipt is not enough to remove.
//
// internal/vaultexport reserves a sequence and an identity before it publishes
// and resolves the reservation on its next pass by asking whether the directory
// is there. A pass that published a whole package and died before recording the
// outcome leaves that reservation over a package the peer can apply and
// acknowledge, because the package is complete — so retiring it would make the
// absent-directory reading a lie, and the exporter would re-export the same
// commit under a fresh identity reusing the same sequence.
//
// The sweep is driven directly rather than through a pass because a pass would
// answer the question itself: the export runs before the retirement and resolves
// the reservation, so the window this covers is the pass whose export was
// deferred by a backoff or failed on something else.
func TestRetirementLeavesTheExportersReservationAlone(t *testing.T) {
	w := enroll(t)
	published := exchange(t, w)
	reserve(t, w.root(), published)

	outcome, err := runRetire(w.root())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if contains(outcome.Retired, published) {
		t.Fatalf("retired %v, which a reservation still names", outcome.Retired)
	}
	if outcome.Awaiting != 1 {
		t.Fatalf("the reserved package was not counted as awaiting: %+v", outcome)
	}
	if got := w.channel(vaultsync.OutgoingDir(w.root())); !contains(got, published) {
		t.Fatalf("the outgoing channel holds %v, want %s still among them", got, published)
	}

	// The receipt is genuinely good, so the same sweep retires the same package
	// the moment the reservation is resolved. Without this the test would pass on
	// a receipt that never verified at all.
	reserve(t, w.root(), "")
	resolved, err := runRetire(w.root())
	if err != nil {
		t.Fatalf("retire after the reservation was resolved: %v", err)
	}
	if !contains(resolved.Retired, published) {
		t.Fatalf("retired %v, want %s among them", resolved.Retired, published)
	}
}

// TestAnIncomingChannelThatIsNotThereReleasesNoReceipt. A receipt is released on
// the evidence that the package it acknowledges has left the incoming channel,
// and an empty channel is that evidence. A channel directory that does not exist
// is not: the transport may not have created it yet, or the volume it lives on
// may be gone, and releasing every receipt at once on that reading would tell a
// peer its changes were never applied.
func TestAnIncomingChannelThatIsNotThereReleasesNoReceipt(t *testing.T) {
	w := enroll(t)
	published := exchange(t, w)
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); !contains(got, published) {
		t.Fatalf("the peer published receipts %v, want one for %s", got, published)
	}

	if err := os.RemoveAll(vaultsync.IncomingDir(w.peerRoot())); err != nil {
		t.Fatalf("remove the incoming channel: %v", err)
	}

	outcome, err := runRetire(w.peerRoot())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if len(outcome.Released) != 0 {
		t.Fatalf("released %v against a channel that is not there", outcome.Released)
	}
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); !contains(got, published) {
		t.Fatalf("the peer's receipts are %v, want %s still among them", got, published)
	}
}

// reserve writes the export record back with a reservation naming packageID, or
// with none when it is empty.
//
// It is a valid export record naming that package as reserved, and deliberately
// not a byte-faithful reproduction of a crashed publication: a real one carries
// the *previous* cursor and a sequence one behind the reservation's, and for a
// baseline it carries no cursor at all. Retirement reads one field of it —
// reservedPackage takes Pending.PackageID and nothing else — so the shape that
// matters is exact and the rest is a record the loader will accept. A future
// reservedPackage that cross-checked the sequence against the cursor would want
// this helper to publish through a deferred export instead.
func reserve(t *testing.T, root, packageID string) {
	t.Helper()
	state, err := vaultexport.LoadState(root)
	if err != nil {
		t.Fatalf("load the export record: %v", err)
	}
	state.Pending = nil
	if packageID != "" {
		if state.Commit == "" {
			t.Fatalf("the export record has no cursor to reserve against: %+v", state)
		}
		state.Pending = &vaultexport.Reservation{
			Sequence:  state.Sequence,
			PackageID: packageID,
			Commit:    state.Commit,
		}
	}
	writeExportState(t, root, state)
}

// writeExportState puts an export record back, bypassing the producer's own
// writer so a test can pose a state a completed pass never leaves.
func writeExportState(t *testing.T, root string, state *vaultexport.State) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("encode the export record: %v", err)
	}
	if err := os.WriteFile(vaultexport.StatePath(root), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write the export record: %v", err)
	}
}
