package vaultsyncd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

func TestRetirementWaitsForCheckpoint(t *testing.T) {
	w := enroll(t)
	held, err := checkpoint.Hold(w.root(), checkpoint.Options{Reason: "test retirement", Drain: time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, retireErr := runRetire(w.root())
		done <- retireErr
	}()
	select {
	case err := <-done:
		t.Fatalf("retirement completed during checkpoint: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("retirement after checkpoint: %v", err)
	}
}

func TestWorkerTransitionWaitsForCheckpointBeforePublishingSchedule(t *testing.T) {
	w := enroll(t)
	root := w.root()
	record := &Record{Workers: map[WorkerName]WorkerRecord{WorkerImport: {Failures: 2}}}
	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "worker transition", Drain: time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- record.save(root, "") }()
	select {
	case err := <-done:
		t.Fatalf("worker record completed during checkpoint: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	if _, err := os.Stat(RecordPath(root)); !os.IsNotExist(err) {
		t.Fatalf("worker record appeared during checkpoint: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("worker transition after checkpoint: %v", err)
	}
	loaded, err := LoadRecord(root)
	if err != nil || loaded.worker(WorkerImport).Failures != 2 {
		t.Fatalf("loaded transition = %+v, err=%v", loaded, err)
	}
}

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

func TestRetirementKeepsAcknowledgedPackageUntilAVerifiedBackupCoversIt(t *testing.T) {
	w := enroll(t)
	published := exchange(t, w)
	if err := os.Remove(vaultsync.BackupCoveragePath(w.root())); err != nil {
		t.Fatalf("remove backup coverage: %v", err)
	}

	outcome, err := runRetire(w.root())
	if err != nil {
		t.Fatalf("retire without backup coverage: %v", err)
	}
	if contains(outcome.Retired, published) || outcome.Uncovered != 1 {
		t.Fatalf("retirement without coverage = %+v, want one retained as uncovered", outcome)
	}
	if got := w.channel(vaultsync.OutgoingDir(w.root())); !contains(got, published) {
		t.Fatalf("outgoing channel %v lost the only locally recoverable package", got)
	}

	coverOutgoing(t, w.root())
	covered, err := runRetire(w.root())
	if err != nil {
		t.Fatalf("retire with backup coverage: %v", err)
	}
	if !contains(covered.Retired, published) {
		t.Fatalf("retired %v, want covered package %s", covered.Retired, published)
	}
}

func TestRetirementRefusesMalformedBackupCoverage(t *testing.T) {
	w := enroll(t)
	exchange(t, w)
	if err := os.WriteFile(vaultsync.BackupCoveragePath(w.root()), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRetire(w.root()); err == nil {
		t.Fatal("retirement accepted malformed backup coverage")
	}
}

func TestRetirementKeepsCoveredPackagesWhenTheDependencyGraphIsUnreadable(t *testing.T) {
	w := enroll(t)
	published := exchange(t, w)
	incomplete := filepath.Join(vaultsync.OutgoingDir(w.root()), "pkg-"+strings.Repeat("f", 32))
	if err := os.Mkdir(incomplete, 0o755); err != nil {
		t.Fatal(err)
	}

	outcome, err := runRetire(w.root())
	if err != nil {
		t.Fatal(err)
	}
	if contains(outcome.Retired, published) || outcome.Required != 1 || outcome.Incomplete != 1 {
		t.Fatalf("retirement against unreadable dependency graph = %+v", outcome)
	}
}

func TestRetirementKeepsABackupCoveredDependencyUntilItsDependentLeaves(t *testing.T) {
	w := enroll(t)
	first := exchange(t, w)
	if err := os.Remove(vaultsync.BackupCoveragePath(w.root())); err != nil {
		t.Fatal(err)
	}

	second := publishDependent(t, w, first)
	w.transport(w.root(), w.peerRoot())
	w.once(w.peerRoot())
	w.transport(w.peerRoot(), w.root())
	coverOutgoing(t, w.root())

	firstSweep, err := runRetire(w.root())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(firstSweep.Retired, second) {
		t.Fatalf("first sweep did not retire dependent %s: %+v", second, firstSweep)
	}
	if !contains(w.channel(vaultsync.OutgoingDir(w.root())), first) || firstSweep.Required != 1 {
		t.Fatalf("dependency %s was not retained by the initial graph: %+v", first, firstSweep)
	}

	secondSweep, err := runRetire(w.root())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(secondSweep.Retired, first) {
		t.Fatalf("dependency was not retired after its dependent left: %+v", secondSweep)
	}
}

// publishDependent adds a second exported package and states an explicit
// dependency on first. Exporters ordinarily derive Requires from imported
// origins; retirement's decision is solely about the manifests on disk, so the
// fixture writes that valid edge directly rather than constructing a third
// replica just to make the producer derive it.
func publishDependent(t *testing.T, w *world, first string) string {
	t.Helper()
	w.write(w.root(), localNote, "written again on the mac\n")
	w.once(w.root())
	packages := w.channel(vaultsync.OutgoingDir(w.root()))
	if len(packages) != 2 {
		t.Fatalf("published packages = %v, want dependency and dependent", packages)
	}
	second := packages[0]
	if second == first {
		second = packages[1]
	}
	outgoing := vaultsync.OutgoingDir(w.root())
	firstManifest := readRetirementManifest(t, filepath.Join(outgoing, first))
	secondDir := filepath.Join(outgoing, second)
	secondManifest := readRetirementManifest(t, secondDir)
	secondManifest.Requires = append(secondManifest.Requires, vaultsync.Dependency{
		OriginDevice: firstManifest.OriginDevice,
		Sequence:     firstManifest.Sequence,
	})
	data, err := json.MarshalIndent(secondManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondDir, vaultsync.ManifestName), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return second
}

func readRetirementManifest(t *testing.T, dir string) *vaultsync.Manifest {
	t.Helper()
	manifest, err := vaultsync.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
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
