//go:build syncrecovery

package vaultsyncd

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

const qualificationOperations = 18

// TestRecoveryQualificationRandomizedOfflineDelivery runs the same fault model
// from fixed seeds so a failure is reproducible. Both vaults continue committing
// while transport is absent for thirty days. Their immutable packages are then
// delivered one at a time in a different random order, with import passes mixed
// between deliveries. The final assertions are from the vaults and signed
// receipts rather than from the transport fixture: every branch is present on
// both sides, every acknowledgment verifies, and repeated passes publish no
// echo.
func TestRecoveryQualificationRandomizedOfflineDelivery(t *testing.T) {
	for _, seed := range []int64{7, 19, 41, 73, 109} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runRandomizedQualification(t, seed)
		})
	}
}

type qualificationEdit struct {
	path string
	body string
}

type qualificationDelivery struct {
	direction    int
	sequence     uint64
	from         string
	to           string
	name         string
	operations   []string
	predecessors []string
}

func runRandomizedQualification(t *testing.T, seed int64) {
	t.Helper()
	ownerRNG := rand.New(rand.NewSource(seed))
	publishRNG := rand.New(rand.NewSource(seed + 1_000))
	deliveryRNG := rand.New(rand.NewSource(seed + 2_000))
	w := enroll(t)

	// Publish both baselines before the outage. They may arrive after later
	// packages; the consumer order and dependency rules must wait rather than
	// guess from directory order.
	w.once(w.root())
	w.once(w.peerRoot())

	expected := makeQualificationEdits(t, w, ownerRNG, publishRNG, seed)
	expectedPackages := map[string]map[string]*vaultsync.Manifest{
		w.root():     qualificationManifests(t, w, w.root()),
		w.peerRoot(): qualificationManifests(t, w, w.peerRoot()),
	}
	publishedBeforeDelivery := qualificationChannels(w)
	if held := deliverQualificationPackages(t, w, deliveryRNG); !held {
		t.Fatalf("seed %d: forced dependent and its conflict-held carrier were not both observed", seed)
	}
	settleQualification(t, w)
	requireQualificationContent(t, w, seed, expected)
	requireQualifiedReceipts(t, w, w.root(), w.peerRoot(), expectedPackages[w.root()])
	requireQualifiedReceipts(t, w, w.peerRoot(), w.root(), expectedPackages[w.peerRoot()])
	requireNoQualificationEcho(t, w, seed, publishedBeforeDelivery)
}

func makeQualificationEdits(
	t *testing.T,
	w *world,
	ownerRNG *rand.Rand,
	publishRNG *rand.Rand,
	seed int64,
) map[string]string {
	t.Helper()
	expected := map[string]string{}
	for operation := range qualificationOperations {
		root := w.root()
		owner := "mac"
		if ownerRNG.Intn(2) == 1 {
			root = w.peerRoot()
			owner = "pi"
		}
		edit := qualificationEdit{
			path: fmt.Sprintf("Qualification/%s-%d.md", owner, operation%3),
			body: fmt.Sprintf("seed=%d operation=%d owner=%s\n", seed, operation, owner),
		}
		w.write(root, edit.path, edit.body)
		expected[edit.path] = edit.body
		if publishRNG.Intn(3) != 0 {
			w.once(root)
		}
		if operation == qualificationOperations/2 {
			requireProlongedOffline(t, w, seed)
		}
	}
	w.once(w.root())
	w.once(w.peerRoot())
	return expected
}

func requireProlongedOffline(t *testing.T, w *world, seed int64) {
	t.Helper()
	w.clock.Advance(30 * 24 * time.Hour)
	for _, root := range []string{w.root(), w.peerRoot()} {
		qualificationAwaiting := qualificationAwaitingCount(t, w, root)
		if qualificationAwaiting == 0 {
			t.Fatalf("seed %d: %s has no qualification package outstanding during the outage", seed, root)
		}
		opts := w.options()
		opts.DisconnectedAfter = 7 * 24 * time.Hour
		status, err := Read(context.Background(), root, opts.readOptions())
		if err != nil {
			t.Fatalf("seed %d: read prolonged offline status for %s: %v", seed, root, err)
		}
		if !status.Is(StateDisconnected) {
			t.Fatalf("seed %d: %s reports %v after thirty days without a receipt", seed, root, status.States)
		}
		if status.Transport.AwaitingReceipt < qualificationAwaiting {
			t.Fatalf("seed %d: %s has %d unacknowledged qualification packages but status reports only %d awaiting receipts",
				seed, root, qualificationAwaiting, status.Transport.AwaitingReceipt)
		}
	}
}

func deliverQualificationPackages(t *testing.T, w *world, rng *rand.Rand) bool {
	t.Helper()
	deliveries := qualificationDeliveries(t, w)
	rng.Shuffle(len(deliveries), func(i, j int) { deliveries[i], deliveries[j] = deliveries[j], deliveries[i] })
	witness := forceOutOfOrderWitness(deliveries)
	held := false
	for i, delivery := range deliveries {
		copyTree(t,
			filepath.Join(vaultsync.OutgoingDir(delivery.from), delivery.name),
			filepath.Join(vaultsync.IncomingDir(delivery.to), delivery.name),
		)
		randomDrain := rng.Intn(3) != 0
		if (i > 0 && randomDrain) || i == 1 || i == len(deliveries)-1 {
			drained, err := vaultimport.Drain(delivery.to, vaultimport.Options{
				Command: "bn sync drain --recovery-qualification",
				Now:     w.clock.Now,
			})
			if err != nil {
				t.Fatalf("drain randomized delivery into %s: %v", delivery.to, err)
			}
			if qualificationHeldForPredecessor(drained, witness) {
				held = true
			}
			w.once(delivery.to)
		}
	}
	return held
}

// forceOutOfOrderWitness keeps the rest of the seeded schedule random while
// making its central fault certain: a package and the package carrying one of
// its named predecessors are offered first, while a predecessor of that carrier
// is still absent. The first drain therefore sees the exact causal carrier in
// the channel and cannot apply it yet.
type qualificationCausalWitness struct {
	dependent string
	carrier   string
}

func forceOutOfOrderWitness(deliveries []qualificationDelivery) qualificationCausalWitness {
	carriers := map[string]int{}
	for i, delivery := range deliveries {
		for _, operationID := range delivery.operations {
			carriers[operationID] = i
		}
	}
	dependent, carrier := qualificationWitness(deliveries, carriers)
	if dependent < 0 {
		return qualificationCausalWitness{}
	}
	delivery := deliveries[dependent]
	carrierDelivery := deliveries[carrier]
	ordered := []qualificationDelivery{delivery, carrierDelivery}
	for i, other := range deliveries {
		if i != dependent && i != carrier {
			ordered = append(ordered, other)
		}
	}
	copy(deliveries, ordered)
	return qualificationCausalWitness{dependent: delivery.name, carrier: carrierDelivery.name}
}

func qualificationWitness(deliveries []qualificationDelivery, carriers map[string]int) (int, int) {
	for dependent, delivery := range deliveries {
		for _, predecessor := range delivery.predecessors {
			carrier, present := carriers[predecessor]
			if !present || deliveries[carrier].direction != delivery.direction ||
				len(deliveries[carrier].predecessors) == 0 {
				continue
			}
			return dependent, carrier
		}
	}
	return -1, -1
}

func qualificationHeldForPredecessor(drained *vaultimport.Drained, witness qualificationCausalWitness) bool {
	if drained == nil {
		return false
	}
	dependentHeld := slices.ContainsFunc(drained.Held, func(held vaultimport.Held) bool {
		return held.PackageID == witness.dependent && held.Reason == vaultimport.HoldPredecessor &&
			strings.Contains(held.Detail, witness.carrier)
	})
	carrierHeld := slices.ContainsFunc(drained.Held, func(held vaultimport.Held) bool {
		return held.PackageID == witness.carrier && held.Reason == vaultimport.HoldConflict
	})
	return dependentHeld && carrierHeld
}

func settleQualification(t *testing.T, w *world) {
	t.Helper()
	// A complete channel lets one drain release every package that was waiting
	// on a missing predecessor. A second pass proves the recovered state is
	// stable and advances each export cursor over the imported commits.
	for range 2 {
		w.once(w.root())
		w.once(w.peerRoot())
	}
}

func requireQualificationContent(t *testing.T, w *world, seed int64, expected map[string]string) {
	t.Helper()
	paths := make([]string, 0, len(expected))
	for path := range expected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		body := expected[path]
		if got := w.pair.Local.Read(t, path); got != body {
			t.Fatalf("seed %d: local %s = %q, want %q", seed, path, got, body)
		}
		if got := w.pair.Peer.Read(t, path); got != body {
			t.Fatalf("seed %d: peer %s = %q, want %q", seed, path, got, body)
		}
	}
}

func qualificationDeliveries(t *testing.T, w *world) []qualificationDelivery {
	t.Helper()
	var deliveries []qualificationDelivery
	for direction, route := range []struct{ from, to string }{
		{w.root(), w.peerRoot()},
		{w.peerRoot(), w.root()},
	} {
		for _, name := range w.channel(vaultsync.OutgoingDir(route.from)) {
			manifest, err := vaultsync.ReadManifest(filepath.Join(vaultsync.OutgoingDir(route.from), name))
			if err != nil {
				t.Fatalf("read package %s before delivery: %v", name, err)
			}
			operations, predecessors := qualificationRelations(manifest)
			deliveries = append(deliveries, qualificationDelivery{
				direction:    direction,
				sequence:     manifest.Sequence,
				from:         route.from,
				to:           route.to,
				name:         name,
				operations:   operations,
				predecessors: predecessors,
			})
		}
	}
	sort.Slice(deliveries, func(i, j int) bool {
		if deliveries[i].direction != deliveries[j].direction {
			return deliveries[i].direction < deliveries[j].direction
		}
		return deliveries[i].sequence < deliveries[j].sequence
	})
	return deliveries
}

func qualificationRelations(manifest *vaultsync.Manifest) (operations, predecessors []string) {
	for _, operation := range manifest.Operations {
		operations = append(operations, operation.OperationID)
		for _, change := range operation.Changes {
			predecessors = append(predecessors, change.Predecessors...)
		}
	}
	return operations, predecessors
}

func qualificationManifests(t *testing.T, w *world, root string) map[string]*vaultsync.Manifest {
	t.Helper()
	manifests := map[string]*vaultsync.Manifest{}
	for _, name := range w.channel(vaultsync.OutgoingDir(root)) {
		manifest, err := vaultsync.ReadManifest(filepath.Join(vaultsync.OutgoingDir(root), name))
		if err != nil {
			t.Fatalf("read qualification package %s: %v", name, err)
		}
		if qualificationManifest(manifest) {
			manifests[name] = manifest
		}
	}
	return manifests
}

func qualificationAwaitingCount(t *testing.T, w *world, root string) int {
	t.Helper()
	awaiting := 0
	for _, manifest := range qualificationManifests(t, w, root) {
		acknowledged, err := verifyReceipt(root, manifest, w.vaultEnrollment(root))
		if err != nil {
			t.Fatalf("verify qualification receipt for %s: %v", manifest.PackageID, err)
		}
		if !acknowledged {
			awaiting++
		}
	}
	return awaiting
}

func requireQualifiedReceipts(
	t *testing.T,
	w *world,
	producer,
	consumer string,
	expected map[string]*vaultsync.Manifest,
) {
	t.Helper()
	if len(expected) == 0 {
		t.Fatalf("%s published no qualification packages for %s to acknowledge", producer, consumer)
	}
	receipts := qualificationReceipts(w, consumer, expected)
	if len(receipts) != len(expected) {
		t.Fatalf("%s acknowledged %d qualification packages from %s, want %d",
			consumer, len(receipts), producer, len(expected))
	}
	for _, packageID := range receipts {
		manifest := expected[packageID]
		receipt, err := vaultsync.ReadReceipt(vaultsync.OutgoingDir(consumer), packageID)
		if err != nil {
			t.Fatalf("read receipt for %s: %v", packageID, err)
		}
		if err := receipt.VerifyAgainst(manifest, w.vaultEnrollment(producer)); err != nil {
			t.Fatalf("receipt for %s is false: %v", packageID, err)
		}
	}
}

func qualificationReceipts(w *world, consumer string, expected map[string]*vaultsync.Manifest) []string {
	var receipts []string
	for _, packageID := range w.receipts(vaultsync.OutgoingDir(consumer)) {
		if _, qualifies := expected[packageID]; qualifies {
			receipts = append(receipts, packageID)
		}
	}
	return receipts
}

func qualificationManifest(manifest *vaultsync.Manifest) bool {
	for _, operation := range manifest.Operations {
		if slices.ContainsFunc(operation.Changes, func(change vaultsync.Change) bool {
			return filepath.ToSlash(filepath.Dir(change.Path)) == "Qualification"
		}) {
			return true
		}
	}
	return false
}

func (w *world) vaultEnrollment(root string) *vaultsync.Enrollment {
	if root == w.root() {
		return w.local
	}
	return w.peer
}

func qualificationChannels(w *world) map[string][]string {
	channels := map[string][]string{}
	for _, root := range []string{w.root(), w.peerRoot()} {
		channels[root] = slices.Clone(w.channel(vaultsync.OutgoingDir(root)))
	}
	return channels
}

func requireNoQualificationEcho(t *testing.T, w *world, seed int64, before map[string][]string) {
	t.Helper()
	for range 3 {
		w.once(w.root())
		w.once(w.peerRoot())
	}
	for _, root := range []string{w.root(), w.peerRoot()} {
		after := w.channel(vaultsync.OutgoingDir(root))
		known := make(map[string]struct{}, len(before[root]))
		for _, packageID := range before[root] {
			known[packageID] = struct{}{}
		}
		for _, packageID := range after {
			if _, existed := known[packageID]; !existed {
				t.Fatalf("seed %d: %s published echo %s: before %v, after %v",
					seed, root, packageID, before[root], after)
			}
		}
		if pending := w.status(root).Export.PendingCommits; pending != 0 {
			t.Fatalf("seed %d: %s still has %d unpublished commits", seed, root, pending)
		}
	}
}
