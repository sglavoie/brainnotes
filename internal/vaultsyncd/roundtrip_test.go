package vaultsyncd

import (
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

const localNote = "Sync/Local.md"
const localBody = "written on the mac\n"

// TestDeliveryIsNotApplicationUntilAReceiptSaysSo walks one change from the
// device that made it to the one that applied it, asking both vaults what they
// believe at every step.
//
// It is the test the whole status model exists for. Each step moves a file and
// nothing else, and the assertion after it is that the sender's view did *not*
// advance: a package that has been copied has been copied, and only an
// acknowledgment says a peer has it.
func TestDeliveryIsNotApplicationUntilAReceiptSaysSo(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)

	pass := w.once(w.root())
	published := w.channel(vaultsync.OutgoingDir(w.root()))
	if len(published) != 1 {
		t.Fatalf("the local vault published %v, want exactly one package", published)
	}
	requireStates(t, pass.Status, StateTransportPending)
	if pass.Status.Transport.AwaitingReceipt != 1 || pass.Status.Transport.Acknowledged != 0 {
		t.Fatalf("after publishing: %+v", pass.Status.Transport)
	}

	// The transport copies the package. That is its whole job and it proves
	// nothing whatever about the peer's vault.
	w.transport(w.root(), w.peerRoot())
	requireStates(t, w.status(w.root()), StateTransportPending)

	peer := w.status(w.peerRoot())
	if peer.Peer.Delivered != 1 || peer.Peer.AwaitingApplication != 1 || peer.Peer.Applied != 0 {
		t.Fatalf("on the peer after delivery: %+v", peer.Peer)
	}
	if !peer.Is(StatePeerApplication) {
		t.Fatalf("the peer holds an unapplied package and reports %v", peer.States)
	}

	// The peer applies it. Only now is the change in a vault rather than beside
	// one, and only now does an acknowledgment exist.
	w.once(w.peerRoot())
	if body := w.pair.Peer.Read(t, localNote); body != localBody {
		t.Fatalf("the peer's copy of %s is %q", localNote, body)
	}
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); !contains(got, published[0]) {
		t.Fatalf("the peer published receipts %v, want one for %s", got, published[0])
	}
	// And until that receipt is carried back, the sender still knows nothing.
	requireStates(t, w.status(w.root()), StateTransportPending)
}

// TestCoveredAcknowledgedPackageRetiresAndItsRemovalReleasesTheReceipt is the
// second half of the handshake: what each device removes, and on whose evidence.
func TestCoveredAcknowledgedPackageRetiresAndItsRemovalReleasesTheReceipt(t *testing.T) {
	w := enroll(t)
	published := exchange(t, w)
	manifest := readRetirementManifest(t, filepath.Join(vaultsync.OutgoingDir(w.root()), published))

	// The acknowledgment is here and exchange recorded verified backup coverage,
	// so the sender's copy has no reader left and is not the only recovery copy.
	pass := w.once(w.root())
	if retired := pass.retired(t).Retired; !contains(retired, published) {
		t.Fatalf("retired %v, want %s among them", retired, published)
	}
	if got := w.channel(vaultsync.OutgoingDir(w.root())); contains(got, published) {
		t.Fatalf("the acknowledged package is still published: %v", got)
	}
	retirement, err := vaultsync.ReadRetirement(vaultsync.OutgoingDir(w.root()))
	if err != nil || retirement == nil || retirement.Through < manifest.Sequence {
		t.Fatalf("sender retirement = %+v, %v; want sequence %d covered", retirement, err, manifest.Sequence)
	}
	requireStates(t, w.status(w.root()), StateIdle)

	// The peer's receipt is still in place, because the package it acknowledges
	// is still in the peer's incoming channel as far as the peer can see.
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); !contains(got, published) {
		t.Fatalf("the peer's receipts are %v, want %s still among them", got, published)
	}

	// The retirement reaches the peer, and the receipt has nothing left to
	// acknowledge.
	w.transport(w.root(), w.peerRoot())
	peerPass := w.once(w.peerRoot())
	if released := peerPass.retired(t).Released; !contains(released, published) {
		t.Fatalf("released %v, want %s among them", released, published)
	}
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); contains(got, published) {
		t.Fatalf("the peer is still holding a receipt for a package nobody has: %v", got)
	}
	if peerPass.Status.Peer.Applied != 0 {
		t.Fatalf("the peer still retains %d retired applied-package records", peerPass.Status.Peer.Applied)
	}
}

// TestAnEditOnEachSideMeetsInTheMiddle is the ordinary case: two people typing
// on two machines, and no conflict, because they touched different notes.
func TestAnEditOnEachSideMeetsInTheMiddle(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.write(w.peerRoot(), "Sync/Peer.md", "written on the pi\n")

	w.once(w.root())
	w.once(w.peerRoot())
	w.transport(w.root(), w.peerRoot())
	w.transport(w.peerRoot(), w.root())
	w.once(w.root())
	w.once(w.peerRoot())

	if body := w.pair.Peer.Read(t, localNote); body != localBody {
		t.Fatalf("the peer's copy of %s is %q", localNote, body)
	}
	if body := w.pair.Local.Read(t, "Sync/Peer.md"); body != "written on the pi\n" {
		t.Fatalf("the local copy of Sync/Peer.md is %q", body)
	}

	// Neither vault republishes what it just applied: the import ledger is
	// durable before the applying commit, so the exporter skips it.
	for _, root := range []string{w.root(), w.peerRoot()} {
		status := w.status(root)
		if status.Export.PendingCommits != 0 {
			t.Fatalf("%s has %d unpublished commits after applying the peer's change", root, status.Export.PendingCommits)
		}
	}
}

// TestAConvergedPackageIsAcknowledgedAndRetired drives the nothing-changed case
// through the same complete handshake as a package that wrote content. The
// receipt claims convergence rather than a recovery entry, and still gives the
// sender the verified evidence it needs to retire the package.
func TestAConvergedPackageIsAcknowledgedAndRetired(t *testing.T) {
	w := enroll(t)
	// No local edit. The two replicas were seeded identically, so the baseline
	// describes a vault the peer already has.
	w.once(w.root())
	published := w.channel(vaultsync.OutgoingDir(w.root()))
	if len(published) != 1 {
		t.Fatalf("the local vault published %v, want exactly one package", published)
	}

	w.transport(w.root(), w.peerRoot())
	applied := w.once(w.peerRoot()).imported(t)
	if applied.Answered != 1 || applied.Changed != 0 {
		t.Fatalf("the peer's drain: %+v", applied)
	}
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); !contains(got, published[0]) {
		t.Fatalf("the peer published receipts %v, want one for converged package %s", got, published[0])
	}

	// The convergence acknowledgment returns through the ordinary channel and is
	// enough to retire the package once backup coverage is present.
	w.transport(w.peerRoot(), w.root())
	coverOutgoing(t, w.root())
	pass := w.once(w.root())
	if retired := pass.retired(t).Retired; !contains(retired, published[0]) {
		t.Fatalf("retired %v, want converged package %s", retired, published[0])
	}
	if pass.Status.Transport.AwaitingReceipt != 0 {
		t.Fatalf("after the peer converged: %+v", pass.Status.Transport)
	}
}

// exchange drives one full round trip of a single local edit and returns the
// package the local vault published. It asserts only what it needs to be true to
// have driven anything at all; the tests that are *about* a step assert there.
func exchange(t *testing.T, w *world) string {
	t.Helper()
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	published := w.channel(vaultsync.OutgoingDir(w.root()))
	if len(published) != 1 {
		t.Fatalf("the local vault published %v, want exactly one package", published)
	}
	w.transport(w.root(), w.peerRoot())
	w.once(w.peerRoot())
	w.transport(w.peerRoot(), w.root())
	coverOutgoing(t, w.root())
	return published[0]
}
