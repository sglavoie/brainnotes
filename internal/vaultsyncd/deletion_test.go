package vaultsyncd

import (
	"context"
	"os"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

func TestExternalDeletionPropagatesAfterOfflineRetry(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	w.transport(w.root(), w.peerRoot())
	w.once(w.peerRoot())
	w.transport(w.peerRoot(), w.root())
	w.once(w.root())

	if err := os.Remove(w.pair.Local.Path(localNote)); err != nil {
		t.Fatal(err)
	}
	mgr, err := vaultgit.Open(context.Background(), w.root(), vaultgit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := mgr.CommitPending(context.Background())
	if err != nil || !result.Committed {
		t.Fatalf("snapshot deletion: %+v, %v", result, err)
	}
	// Several passes with no transport must leave a deliverable deletion.
	for i := 0; i < 3; i++ {
		w.once(w.root())
	}
	if got := w.pair.Peer.Read(t, localNote); got != localBody {
		t.Fatal("offline peer unexpectedly changed")
	}
	for i := 0; i < 2; i++ {
		w.transport(w.root(), w.peerRoot())
		w.once(w.peerRoot())
		if _, err := os.Stat(w.pair.Peer.Path(localNote)); !os.IsNotExist(err) {
			t.Fatalf("peer did not apply deletion: %v", err)
		}
	}
	w.transport(w.peerRoot(), w.root())
	w.once(w.root())
	if _, err := os.Stat(w.pair.Local.Path(localNote)); !os.IsNotExist(err) {
		t.Fatalf("receipt resurrected deleted note: %v", err)
	}
}

func TestExternalDeletionConflictsWithConcurrentPeerEdit(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	w.transport(w.root(), w.peerRoot())
	w.once(w.peerRoot())
	w.write(w.peerRoot(), localNote, "concurrent peer edit\n")
	if err := os.Remove(w.pair.Local.Path(localNote)); err != nil {
		t.Fatal(err)
	}
	mgr, err := vaultgit.Open(context.Background(), w.root(), vaultgit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CommitPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.once(w.root())
	w.transport(w.root(), w.peerRoot())
	pass := w.once(w.peerRoot())
	if !pass.Status.Is(StateConflict) {
		t.Fatalf("expected edit/delete conflict, got %v", pass.Status.States)
	}
	if got := w.pair.Peer.Read(t, localNote); got != "concurrent peer edit\n" {
		t.Fatalf("deletion discarded concurrent edit: %q", got)
	}
}
