package mutationmatrix

import (
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultimport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// The conflict resolution routes, spelled out for the reason settingsPreviewRoute
// gives.
const (
	syncPreviewRoute = "/api/sync/conflicts/preview"
	syncResolveRoute = "/api/sync/conflicts/resolve"
)

// The two versions of Alpha.md the replicas disagree about. Each differs from
// the seed, so the snapshot can only hold the peer's if the resolution wrote it.
const (
	syncLocalAlpha  = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, rewritten here.\n"
	syncRemoteAlpha = "---\nstatus: review\ntitle: Alpha\n---\n\nAlpha body, rewritten on the peer.\n"
)

// webSyncMutation is the conflict resolution writer: a held package decided in
// the browser and redeemed through the receipt its preview issued.
//
// It takes the peer's version, because that is the one choice a resolution
// carries out in a single commit: the others apply the package and then revise
// it, which is two commits by design and is asserted as such by the route's own
// tests. Taking the peer's version is still the whole resolution path — the
// receipt, the engine's staleness check, the application under the plan's
// pinned branches, and the release of the hold.
//
// Like previewedJSONRow it has no no-op: a conflict that is already decided
// issues no receipt, and the route's own tests cover that refusal.
func webSyncMutation() mutation {
	return mutation{
		name:    "web/sync-resolve",
		prepare: prepareSyncResolve,
		apply: func(t *testing.T, h *harness, state any) error {
			return applyConfigWrite(t, state.(configWritePreview), syncResolveRoute)
		},
		touches:   []string{"Alpha.md"},
		journaled: true,
		overHTTP:  true,
	}
}

// prepareSyncResolve enrolls the harness vault with a peer, has both rewrite
// Alpha.md, and lets the import hold the peer's package for a decision.
//
// Nothing about the conflict is written by hand. The peer is a second fixture
// vault enrolled the way a person enrolls one, the package is built by
// vaultsync.Builder, and the hold is recorded by vaultimport.Drain refusing to
// apply it — so the row resolves exactly what the engine records, and a
// hand-written state file could not make it pass.
func prepareSyncResolve(t *testing.T, h *harness) any {
	t.Helper()
	peer := enrollSyncPeer(t, h)

	h.writeFile("Alpha.md", syncLocalAlpha)
	// The local edit is committed by exact path, as the engine would have, so
	// .bnotes stays out of the history the way it does in a vault in use.
	h.rawGit("add", "--", "Alpha.md")
	h.rawGit("commit", "-q", "-m", "the local edit that conflicts")

	packageID := deliverSyncPackage(t, h, peer)
	if _, err := vaultimport.Drain(h.root, vaultimport.Options{Command: "bn sync --matrix"}); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	requireHeld(t, h, packageID)

	return previewConfigWrite(t, h, syncPreviewRoute, map[string]any{
		"package_id": packageID,
		"paths": []map[string]any{
			{"path": "Alpha.md", "choice": string(vaultimport.AcceptIncoming)},
		},
	})
}

// enrollSyncPeer builds the other replica from the harness's own seed and
// enrolls the pair in both directions, returning the peer's enrollment.
func enrollSyncPeer(t *testing.T, h *harness) *vaultsync.Enrollment {
	t.Helper()
	peerVault := vaultfixture.New(t, vaultfixture.Options{
		Name: "peer", Config: fixtureConfig, Files: baseFiles,
	})

	local, err := vaultsync.Establish(h.root, vaultsync.Options{Label: "mac"})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	peer, err := vaultsync.Join(peerVault.Root, local.Invite(), vaultsync.Options{Label: "pi"})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if _, err := vaultsync.RecordPeer(h.root, vaultsync.Peer{DeviceID: peer.DeviceID, Label: peer.Label}); err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	return peer
}

// deliverSyncPackage publishes the peer's rewrite of Alpha.md into the harness
// vault's incoming channel, where the transport would have left it. The package
// states the seed as the peer's belief, which the local edit contradicts.
func deliverSyncPackage(t *testing.T, h *harness, peer *vaultsync.Enrollment) string {
	t.Helper()
	builder, err := vaultsync.NewBuilder(peer, 1, time.Now())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	operationID, err := vaultsync.NewOperationID()
	if err != nil {
		t.Fatalf("NewOperationID: %v", err)
	}
	builder.BeginOperation(operationID, time.Now())
	believed := vaultsync.Existing(vaultsync.HashBytes([]byte(baseFiles["Alpha.md"])))
	builder.Write("Alpha.md", []byte(syncRemoteAlpha), believed)
	manifest, _, err := builder.PublishTo(vaultsync.IncomingDir(h.root))
	if err != nil {
		t.Fatalf("PublishTo: %v", err)
	}
	return manifest.PackageID
}

// requireHeld fails unless the import is holding the package for a decision,
// so a row that resolves nothing cannot pass by accident.
func requireHeld(t *testing.T, h *harness, packageID string) {
	t.Helper()
	pending, err := vaultimport.Inspect(h.root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for _, entry := range pending {
		if entry.PackageID == packageID && entry.Reason == vaultimport.HoldConflict {
			return
		}
	}
	t.Fatalf("%s is not held for a decision: %+v", packageID, pending)
}
