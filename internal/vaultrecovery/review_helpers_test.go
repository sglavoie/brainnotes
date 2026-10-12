package vaultrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/backup"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

func restoreOlderCheckpoint(t *testing.T, r *reviewPair) {
	t.Helper()
	archivedReview, err := vaultsync.PrepareRestoreReviewContext(context.Background(), r.pair.Local.Root, strings.Repeat("c", 64), r.clock.Tick)
	if err != nil {
		t.Fatalf("prepare marker captured by checkpoint: %v", err)
	}
	created, err := backup.Create(r.pair.Local.Root, backup.CreateOptions{Now: r.clock.Tick})
	if err != nil {
		t.Fatalf("create older checkpoint: %v", err)
	}
	if err := vaultsync.ClearRestoreReviewContext(context.Background(), r.pair.Local.Root); err != nil {
		t.Fatalf("clear pre-checkpoint marker: %v", err)
	}
	r.pair.Peer.Write(t, "Alpha.md", "survivor newer body\n")
	if err := os.Remove(filepath.Join(r.pair.Peer.Root, "Beta.md")); err != nil {
		t.Fatal(err)
	}
	r.pair.Peer.Commit(t, "survivor edit and deletion")
	r.pair.Local.Write(t, "Alpha.md", "local work after checkpoint\n")
	r.pair.Local.Commit(t, "later local work")
	if _, err := noteops.RestoreBackup(r.pair.Local.Root, created.ArchivePath, backup.RestoreOptions{Wipe: true}); err != nil {
		t.Fatalf("restore older checkpoint: %v", err)
	}
	restoredReview, err := vaultsync.LoadRestoreReview(r.pair.Local.Root)
	if err != nil || restoredReview == nil || restoredReview.ProposedDeviceID == archivedReview.ProposedDeviceID {
		t.Fatalf("restored checkpoint reused archived proposal: archived=%+v restored=%+v err=%v", archivedReview, restoredReview, err)
	}
}

func reservePendingSequence(t *testing.T, root string) uint64 {
	t.Helper()
	state, err := vaultexport.LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	packageID, err := vaultsync.NewPackageID()
	if err != nil {
		t.Fatal(err)
	}
	state.Pending = &vaultexport.Reservation{Sequence: state.Sequence + 1, PackageID: packageID, Commit: state.Commit}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultexport.StatePath(root), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return state.Pending.Sequence
}

func reconcileFromSurvivor(t *testing.T, r *reviewPair) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(r.pair.Peer.Root, "Alpha.md"))
	if err != nil {
		t.Fatal(err)
	}
	r.pair.Local.Write(t, "Alpha.md", string(body))
	if err := os.Remove(filepath.Join(r.pair.Local.Root, "Beta.md")); err != nil {
		t.Fatal(err)
	}
	r.pair.Local.Commit(t, "reviewed reconciliation")
}

func reconnectBaseline(t *testing.T, r *reviewPair) {
	t.Helper()
	result, err := vaultexport.Export(context.Background(), vaultexport.Options{Root: r.pair.Local.Root, Now: r.clock.Tick})
	if err != nil {
		t.Fatalf("export reviewed baseline: %v", err)
	}
	if !result.Baseline || len(result.Packages) == 0 {
		t.Fatalf("reviewed reconnect export = %+v", result)
	}
	for _, published := range result.Packages {
		deliverReviewedPackage(t, r, published)
	}
	assertSurvivorContent(t, r)
}

func deliverReviewedPackage(t *testing.T, r *reviewPair, published vaultexport.Published) {
	t.Helper()
	destination := filepath.Join(vaultsync.IncomingDir(r.pair.Peer.Root), filepath.Base(published.Dir))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(destination, os.DirFS(published.Dir)); err != nil {
		t.Fatalf("deliver reviewed baseline: %v", err)
	}
	if _, err := vaultimport.Apply(r.pair.Peer.Root, destination, vaultimport.Options{Now: r.clock.Tick}); err != nil {
		t.Fatalf("apply reviewed baseline: %v", err)
	}
}

func assertSurvivorContent(t *testing.T, r *reviewPair) {
	t.Helper()
	if got, err := os.ReadFile(filepath.Join(r.pair.Peer.Root, "Alpha.md")); err != nil || string(got) != "survivor newer body\n" {
		t.Fatalf("reconnect changed survivor edit: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(r.pair.Peer.Root, "Beta.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reconnect resurrected deletion: %v", err)
	}
}
