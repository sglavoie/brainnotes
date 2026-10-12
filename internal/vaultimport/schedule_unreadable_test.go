package vaultimport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestDrainCallsAPackageStillBeingCopiedInUnreadableRatherThanFailed is about
// the one refusal that reaches the scheduler from two different places.
//
// The scan reads the manifest and nothing else, so a package whose payloads are
// still being written looks whole to it — the incompleteness is found later, by
// Accept, on the far side of the scan's own unreadable check. Both are the same
// situation and need the same answer: wait. Calling the late one a failure would
// put "applying it failed" in front of somebody whose transport is simply still
// running, and the remedy for a failure is to go looking at the vault.
func TestDrainCallsAPackageStillBeingCopiedInUnreadableRatherThanFailed(t *testing.T) {
	r := enroll(t)
	arriving, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	independent, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	})
	emptyPayloads(t, dir)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{independent.PackageID},
		"the packages the drain applied past the one still arriving")
	requireHeldFor(t, drained, arriving.PackageID, HoldUnreadable)
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaOnceBody {
		t.Fatalf("the independent edit holds %q", got)
	}
}

// TestDrainSaysAPackageForAnotherVaultIsRejectedRatherThanFailed draws the line
// between a wait and a remedy.
//
// Every other hold resolves itself: the transport delivers the dependency, or a
// person edits the note. A package this vault will not accept resolves never, and
// reporting it in the same bucket as a failed application invites the one useless
// response — running the command again.
func TestDrainSaysAPackageForAnotherVaultIsRejectedRatherThanFailed(t *testing.T) {
	r := enroll(t)
	foreign, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	misaddress(t, dir)

	drained := mustDrain(t, r)

	if len(drained.Applied) != 0 {
		t.Fatalf("the drain applied %v, want nothing", appliedIDs(drained))
	}
	requireHeldFor(t, drained, foreign.PackageID, HoldRejected)
	if _, err := os.Stat(r.pair.Local.Path("Notes/Delta.md")); !os.IsNotExist(err) {
		t.Fatalf("the refused creation landed anyway: %v", err)
	}
}

// TestDrainSaysAPackageInAnUnknownFormatIsRejectedRatherThanStillArriving is
// the scan's half of the same distinction, and it is the one that costs the most
// to get wrong.
//
// A peer that upgrades starts publishing a format this build cannot read, and
// the scan — which reads the manifest and nothing else — is where that is found.
// Reporting it as a package still being copied in tells a person to wait for
// something no wait will fix, and the peer's every later change queues behind it.
func TestDrainSaysAPackageInAnUnknownFormatIsRejectedRatherThanStillArriving(t *testing.T) {
	r := enroll(t)
	newer, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	rewriteManifest(t, dir, func(manifest map[string]any) { manifest["format"] = "brainnotes-sync-v2" })

	drained := mustDrain(t, r)

	if len(drained.Applied) != 0 {
		t.Fatalf("the drain applied %v, want nothing", appliedIDs(drained))
	}
	if len(drained.Held) != 1 {
		t.Fatalf("the drain held %v, want the one package", heldIDs(drained))
	}
	// The manifest is what carries the identity, and this build refused to read it,
	// so the report can only name the directory — which is enough to act on.
	held := drained.Held[0]
	if held.Reason != HoldRejected {
		t.Fatalf("the package is held for %q, want %q: %s", held.Reason, HoldRejected, held.Detail)
	}
	if held.PackageID != "" || held.Dir != dir {
		t.Fatalf("the report identifies it as %+v, want the directory of %s", held, newer.PackageID)
	}
}

// TestDrainReportsAPackageThatLandedEvenWhenItsOwnChannelIsUnreadable is about
// the one fact that outranks the error: a Result means the content is durable.
//
// A receipt written by a newer build sitting in this vault's *outgoing* channel
// makes the acknowledgment fail with the same sentinel a package this vault
// refuses to accept carries. Classifying on the error first would report a
// package that is committed, journaled and snapshotted as one this vault will
// never accept — and drop both the Result and the failure out of the report, so
// nothing would say the peer has not been told.
func TestDrainReportsAPackageThatLandedEvenWhenItsOwnChannelIsUnreadable(t *testing.T) {
	r := enroll(t)
	landing, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	plantUnreadableReceipt(t, r, landing.PackageID)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{landing.PackageID}, "the packages the drain applied")
	if len(drained.Held) != 0 {
		t.Fatalf("the drain held %v, want nothing", heldIDs(drained))
	}
	if got := r.pair.Local.Read(t, "Notes/Delta.md"); got != deltaBody {
		t.Fatalf("the created note holds %q, want the package's bytes", got)
	}
	if drained.Unfinished == nil {
		t.Fatalf("the drain reported nothing unfinished, want the acknowledgment failure")
	}
	if !strings.Contains(drained.Unfinished.Error(), landing.PackageID+" was applied and not acknowledged") {
		t.Fatalf("the drain reported %q, want it to name %s as unacknowledged",
			drained.Unfinished, landing.PackageID)
	}
	if result := drained.Applied[0]; result.Receipt == nil || result.ReceiptPath != "" {
		t.Fatalf("the result reports the unpublished receipt as %+v at %q", result.Receipt, result.ReceiptPath)
	}
}

// plantUnreadableReceipt puts a receipt this build cannot read where this vault
// publishes its own, under the name the package's acknowledgment will take. That
// is what a vault whose peer half was upgraded and rolled back holds, and it is
// the cheapest way to make acknowledging fail after the content is durable.
func plantUnreadableReceipt(t *testing.T, r *replicas, packageID string) {
	t.Helper()
	channel := vaultsync.OutgoingDir(r.root())
	path, err := vaultsync.ReceiptPath(channel, packageID)
	if err != nil {
		t.Fatalf("ReceiptPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the outgoing channel: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"format":"brainnotes-sync-v2"}`), 0o644); err != nil {
		t.Fatalf("write the receipt: %v", err)
	}
}

// TestHoldsNamesThePackagesAPassNeverReached covers the partial report, which is
// the only place a verdict can be missing.
//
// A blank reason renders as a hold whose explanation went missing, and what it
// actually means is the opposite of a decision: the drain stopped before this
// package's turn. It is driven through the struct rather than through a refusal
// because the refusals that end a pass — corrupt state, a changed identity — are
// not reachable from a fixture without corrupting the vault first, and the
// substitution is the whole of what is being asserted.
func TestHoldsNamesThePackagesAPassNeverReached(t *testing.T) {
	decided := &candidate{
		dir:      "decided",
		manifest: &vaultsync.Manifest{PackageID: "pkg-decided", OriginDevice: "device-peer", Sequence: 2},
		reason:   HoldOrder,
		detail:   `changes "Beta.md", which pkg-earlier changes first`,
	}
	unreached := &candidate{
		dir:      "unreached",
		manifest: &vaultsync.Manifest{PackageID: "pkg-unreached", OriginDevice: "device-peer", Sequence: 3},
	}
	landed := &candidate{
		dir:      "landed",
		manifest: &vaultsync.Manifest{PackageID: "pkg-landed", OriginDevice: "device-peer", Sequence: 1},
		done:     true,
	}
	d := &drain{pending: []*candidate{landed, decided, unreached}}

	held := d.holds()

	if len(held) != 2 {
		t.Fatalf("holds reported %+v, want only the two that are still in the channel", held)
	}
	if held[0].Reason != HoldOrder || held[0].Detail != decided.detail {
		t.Fatalf("the decided package reports %q/%q, want its own verdict", held[0].Reason, held[0].Detail)
	}
	if held[1].Reason != HoldNotAttempted {
		t.Fatalf("the unreached package reports %q, want %q", held[1].Reason, HoldNotAttempted)
	}
	if held[1].Detail == "" {
		t.Fatalf("the unreached package reports no detail, want one naming the stopped pass")
	}
	if held[1].PackageID != "pkg-unreached" || held[1].Sequence != 3 {
		t.Fatalf("the unreached package is reported as %+v, want it identified", held[1])
	}
}

// TestUnfinishedWorkSaysWhichStepDidNotHappen is the discriminator behind
// Drained.Unfinished, and the receipt is what it reads rather than the error.
//
// Both shapes come back from Apply as a Result *with* an error, and they send a
// person to opposite places: one to a peer that was never told, the other to
// whatever came after telling it — the import record, or the channel the
// acknowledgment could not be written back into. Guessing from the error would
// describe a receipt this vault holds as one it never minted.
func TestUnfinishedWorkSaysWhichStepDidNotHappen(t *testing.T) {
	const acknowledged = "was applied and acknowledged and the work after that did not finish"
	cases := []struct {
		name   string
		result *Result
		cause  error
		want   string
	}{
		{
			name:   "the receipt was never minted",
			result: &Result{PackageID: "pkg-unacknowledged"},
			cause:  errors.New("mint receipt: read back the recovery entry"),
			want:   "pkg-unacknowledged was applied and not acknowledged",
		},
		{
			name:   "the receipt is published and the record is not saved",
			result: &Result{PackageID: "pkg-unrecorded", Receipt: &vaultsync.Receipt{PackageID: "pkg-unrecorded"}, ReceiptPath: "/outgoing/pkg-unrecorded.json"},
			cause:  errors.New("write import state: no space left on device"),
			want:   "pkg-unrecorded " + acknowledged,
		},
		{
			// A redelivery whose retained receipt could not be put back in the
			// channel. The statement exists locally, but the peer still cannot read it.
			name:   "the receipt exists and could not be republished",
			result: &Result{PackageID: "pkg-unpublished", Duplicate: true, Receipt: &vaultsync.Receipt{PackageID: "pkg-unpublished"}},
			cause:  errors.New("publish receipt: read-only file system"),
			want:   "pkg-unpublished was applied and not acknowledged",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := unfinishedWork(tc.result, tc.cause)
			if err == nil {
				t.Fatalf("unfinishedWork answered nothing for a failure")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unfinishedWork said %q, want it to name %q", err, tc.want)
			}
			if !errors.Is(err, tc.cause) {
				t.Fatalf("unfinishedWork dropped the cause: %v", err)
			}
		})
	}
	if err := unfinishedWork(&Result{PackageID: "pkg-clean"}, nil); err != nil {
		t.Fatalf("unfinishedWork answered %v for an application that finished", err)
	}
}

// emptyPayloads leaves a package's manifest in place and takes away the bytes it
// names, which is what the channel holds while the transport is mid-copy.
func emptyPayloads(t *testing.T, dir string) {
	t.Helper()
	payloads := filepath.Join(dir, vaultsync.PayloadsName)
	entries, err := os.ReadDir(payloads)
	if err != nil {
		t.Fatalf("read the payload directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("the package carries no payload to take away")
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(payloads, entry.Name())); err != nil {
			t.Fatalf("remove payload %s: %v", entry.Name(), err)
		}
	}
}

// misaddress readdresses a delivered package to a vault that is not this one,
// which is what a misrouted transport share puts in the channel. The manifest is
// rewritten in place because nothing signs it: enrollment here is a routing fact
// rather than an authentication one, which is exactly why a package that fails
// the check is refused rather than merged.
func misaddress(t *testing.T, dir string) {
	t.Helper()
	other, err := vaultsync.NewVaultID()
	if err != nil {
		t.Fatalf("NewVaultID: %v", err)
	}
	rewriteManifest(t, dir, func(manifest map[string]any) { manifest["vault_id"] = other })
}

// rewriteManifest edits a delivered package's manifest in place, which is how
// these tests produce a package no honest producer of this build would write.
func rewriteManifest(t *testing.T, dir string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, vaultsync.ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	mutate(manifest)
	rewritten, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	if err := os.WriteFile(path, rewritten, 0o644); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
}

func mustDrain(t *testing.T, r *replicas) *Drained {
	t.Helper()
	drained, err := Drain(r.root(), Options{Command: "bn sync drain --test", Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	return drained
}

// appliedIDs and heldIDs report a drain's two outcomes in the order it decided
// them, which is the order the assertions above are about.
func appliedIDs(drained *Drained) []string {
	ids := make([]string, 0, len(drained.Applied))
	for _, result := range drained.Applied {
		ids = append(ids, result.PackageID)
	}
	return ids
}

func heldIDs(drained *Drained) []string {
	ids := make([]string, 0, len(drained.Held))
	for _, held := range drained.Held {
		ids = append(ids, held.PackageID)
	}
	return ids
}

func requireHeldFor(t *testing.T, drained *Drained, packageID string, reason HoldReason) {
	t.Helper()
	for _, held := range drained.Held {
		if held.PackageID != packageID {
			continue
		}
		if held.Reason != reason {
			t.Fatalf("%s is held for %q, want %q: %s", packageID, held.Reason, reason, held.Detail)
		}
		return
	}
	t.Fatalf("%s is not held at all", packageID)
}
