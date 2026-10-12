package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// alphaBody is what the peer's package rewrites Alpha.md to. It differs from the
// seeded body in the frontmatter as well as the prose, so a comparison cannot
// pass on a partial write.
const alphaBody = "---\nstatus: review\ntitle: Alpha\n---\n\nAlpha body, rewritten on the peer, see [[Beta]].\n"

const deltaBody = "---\nstatus: draft\ntitle: Delta\n---\n\nDelta body, created on the peer.\n"

// TestApplyWritesPackagedContentAndRecordsRecovery is the session's headline
// claim: a valid package produces matching live content *and* a local recovery
// history, and the receipt that goes back to the peer names that history.
func TestApplyWritesPackagedContentAndRecordsRecovery(t *testing.T) {
	r := enroll(t)
	before := r.pair.Local.Head(t)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	result := r.mustApply(t, dir)

	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q, want the packaged body", got)
	}
	if got := r.pair.Local.Read(t, "Notes/Delta.md"); got != deltaBody {
		t.Fatalf("Notes/Delta.md holds %q, want the packaged body", got)
	}

	// The recovery history: a real entry on disk, covering exactly the paths the
	// package changed, reachable by the identity the receipt hands the peer.
	entryCovers(t, r, result.JournalEntry, "Alpha.md", "Notes/Delta.md")

	// The receipt: published in this device's own outgoing channel, answering
	// this package, naming that entry.
	requireReceiptIsOwed(t, r, manifest, result)

	// Independent local history: one commit of this vault's own, carrying the
	// package's paths and the manifests that authorize them, and nothing else.
	if got, want := commitCount(t, r.pair.Local), 2; got != want {
		t.Fatalf("the vault holds %d commits, want %d", got, want)
	}
	if head := r.pair.Local.Head(t); head == before {
		t.Fatal("applying a package left the content history unchanged")
	}
	changed := contentPaths(commitPaths(t, r.pair.Local, "HEAD"))
	requireEqual(t, changed, []string{"Alpha.md", "Notes/Delta.md"}, "the applying commit carries")
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after an import: %v", dirty)
	}

	// The anti-echo ledger, written before the commit that needs it.
	requireProvenance(t, r, result.JournalEntry, manifest.Operations[0].OperationID)
}

// entryCovers loads the recovery entry by the identity the import reported and
// returns it, having checked it covers exactly the given paths.
//
// Loading it by that identity rather than by listing the journal is the point:
// it is the identity the receipt hands the peer, and the peer may discard its
// only copy of the change on the strength of it.
func entryCovers(t *testing.T, r *replicas, entryID string, want ...string) journal.Entry {
	t.Helper()
	entry, err := journal.LoadEntry(noteops.JournalRoot(r.root()), entryID)
	if err != nil {
		t.Fatalf("LoadEntry(%s): %v", entryID, err)
	}
	paths := make([]string, 0, len(entry.Files))
	for _, file := range entry.Files {
		paths = append(paths, file.Path)
	}
	requireEqual(t, paths, want, "recovery entry covers")
	return *entry
}

// requireReceiptIsOwed checks the acknowledgment as the peer would: read back
// from the outgoing channel and verified against the package it answers.
func requireReceiptIsOwed(t *testing.T, r *replicas, manifest *vaultsync.Manifest, result *Result) {
	t.Helper()
	if result.Receipt.RecoveryEntry != result.JournalEntry {
		t.Fatalf("receipt names recovery entry %q, the import produced %q",
			result.Receipt.RecoveryEntry, result.JournalEntry)
	}
	published, err := vaultsync.ReadReceipt(vaultsync.OutgoingDir(r.root()), manifest.PackageID)
	if err != nil {
		t.Fatalf("ReadReceipt: %v", err)
	}
	if err := published.VerifyAgainst(manifest, r.peer); err != nil {
		t.Fatalf("the peer would refuse the receipt it is owed: %v", err)
	}
}

// requireProvenance checks the anti-echo ledger: the local entry this import
// produced is recorded against the peer operation that caused it, which is what
// stops the exporter publishing the peer's own change back to it.
func requireProvenance(t *testing.T, r *replicas, entryID, operationID string) {
	t.Helper()
	imported := r.imported(t)
	record, known := imported[entryID]
	if !known {
		t.Fatalf("the export ledger has no record for entry %q: %v", entryID, imported)
	}
	if record.OperationID != operationID || record.OriginDevice != r.peer.DeviceID {
		t.Fatalf("the export ledger records %+v, want operation %s from %s",
			record, operationID, r.peer.DeviceID)
	}
}

// TestApplyAcknowledgesTheBytesOnThisDisk pins that the receipt's hashes are
// computed from the vault rather than copied from the package. A receipt
// assembled from the manifest would be true by construction and say nothing.
func TestApplyAcknowledgesTheBytesOnThisDisk(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	result := r.mustApply(t, dir)

	if len(result.Receipt.Applied) != 1 {
		t.Fatalf("receipt acknowledges %d paths, want 1", len(result.Receipt.Applied))
	}
	applied := result.Receipt.Applied[0]
	onDisk := vaultsync.HashBytes([]byte(r.pair.Local.Read(t, "Alpha.md")))
	if applied.SHA256 != onDisk {
		t.Fatalf("receipt acknowledges %q, the file holds %q", applied.SHA256, onDisk)
	}
	if applied.Action != vaultsync.ActionWrite || applied.Path != "Alpha.md" {
		t.Fatalf("receipt acknowledges %+v, want a write of Alpha.md", applied)
	}
}

// TestApplyRefusesAPackageThisVaultDisagreesWith is the other half of the
// session's completion claim: an invalid precondition leaves the contested
// content unchanged, and leaves no trace of the attempt anywhere.
func TestApplyRefusesAPackageThisVaultDisagreesWith(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")

	// The local device edits the same note first and commits it, which is the
	// concurrent-edit case the expectation exists to catch.
	const localBody = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, edited here.\n"
	r.pair.Local.Write(t, "Alpha.md", localBody)
	head := r.pair.Local.Commit(t, "local edit")

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package built against superseded content")

	if got := r.pair.Local.Read(t, "Alpha.md"); got != localBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("a refused import moved the content history")
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("a refused import left %d recovery entries behind", len(entries))
	}
	if imported := r.imported(t); len(imported) != 0 {
		t.Fatalf("a refused import recorded provenance: %v", imported)
	}
	if _, err := os.Lstat(vaultsync.ReceiptsDir(vaultsync.OutgoingDir(r.root()))); !os.IsNotExist(err) {
		t.Fatalf("a refused import published something into the outgoing channel (%v)", err)
	}
}

// TestApplyLeavesEveryPathAloneWhenOneOfThemConflicts is the multi-file half of
// the same claim. A package is one operation, so a single stale expectation has
// to stop all of it — including the paths that would have applied cleanly.
func TestApplyLeavesEveryPathAloneWhenOneOfThemConflicts(t *testing.T) {
	r := enroll(t)
	gamma := r.believes(t, "Notes/Gamma.md")
	stale := r.believes(t, "Alpha.md")

	const localBody = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, edited here.\n"
	r.pair.Local.Write(t, "Alpha.md", localBody)
	r.pair.Local.Commit(t, "local edit")
	gammaBefore := r.pair.Local.Read(t, "Notes/Gamma.md")

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Gamma.md", []byte("---\ntitle: Gamma\n---\n\nRewritten.\n"), gamma)
		b.Write("Alpha.md", []byte(alphaBody), stale)
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package whose second path conflicts")

	if got := r.pair.Local.Read(t, "Notes/Gamma.md"); got != gammaBefore {
		t.Fatalf("an uncontested path was written anyway: %q", got)
	}
	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("a refused package still created Notes/Delta.md")
	}
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
		t.Fatalf("a refused import left the vault dirty: %v", dirty)
	}
}

// TestApplyPreservesUnrelatedWorkingTreeEdits is the sentinel assertion every
// mutation in this repository owes: an operation commits what it was asked to
// and never sweeps in somebody's unsaved work.
func TestApplyPreservesUnrelatedWorkingTreeEdits(t *testing.T) {
	r := enroll(t)
	const unrelatedEdit = "---\nstatus: draft\ntitle: Unrelated\n---\n\nA local edit nobody packaged.\n"
	r.pair.Local.Write(t, vaultfixture.UnrelatedNote, unrelatedEdit)

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	r.mustApply(t, dir)

	if got := r.pair.Local.Read(t, vaultfixture.UnrelatedNote); got != unrelatedEdit {
		t.Fatalf("%s holds %q, want the local edit untouched", vaultfixture.UnrelatedNote, got)
	}
	changed := contentPaths(commitPaths(t, r.pair.Local, "HEAD"))
	requireEqual(t, changed, []string{"Alpha.md"}, "the applying commit carries")
	dirty := dirtyInScope(t, r.pair.Local)
	if len(dirty) != 1 || !strings.HasSuffix(dirty[0], vaultfixture.UnrelatedNote) {
		t.Fatalf("expected exactly the unrelated edit to remain uncommitted, got %v", dirty)
	}
}

// TestApplyCommitsATombstoneThroughTheDeletionGuard covers the interaction the
// session calls out by name. internal/vaultgit commits a deletion only when an
// uncommitted journal record proves it was intended, and the record is written
// by Finalize before the snapshot runs — so a tombstone that lands on disk but
// is not committed would leave the vault permanently dirty.
func TestApplyCommitsATombstoneThroughTheDeletionGuard(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", r.believes(t, "Notes/Gamma.md"))
	})

	result := r.mustApply(t, dir)

	if exists(t, r.pair.Local, "Notes/Gamma.md") {
		t.Fatal("the tombstoned path is still on disk")
	}
	status := commitStatus(t, r.pair.Local, "HEAD")
	if !containsLine(status, "D\tNotes/Gamma.md") {
		t.Fatalf("the applying commit does not record the deletion: %v", status)
	}
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
		t.Fatalf("a committed deletion left the vault dirty: %v", dirty)
	}

	// The undo entry has to be able to put the note back, which means it has to
	// carry the content that was removed.
	entry := entryCovers(t, r, result.JournalEntry, "Notes/Gamma.md")
	if entry.Files[0].Before.Snapshot == "" {
		t.Fatal("the recovery entry holds no snapshot of the removed note")
	}

	applied := result.Receipt.Applied
	if len(applied) != 1 || applied[0].Action != vaultsync.ActionDelete || applied[0].SHA256 != "" {
		t.Fatalf("receipt acknowledges %+v, want an empty-hashed tombstone", applied)
	}
}

// TestApplyKeepsAMultiFileOperationTogether pins the boundary the producer took
// the trouble to preserve: one package is one commit and one recovery entry,
// whatever mix of writes, creations and deletions it carries.
func TestApplyKeepsAMultiFileOperationTogether(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
		b.Delete("Notes/Gamma.md", r.believes(t, "Notes/Gamma.md"))
	})

	result := r.mustApply(t, dir)

	if got, want := commitCount(t, r.pair.Local), 2; got != want {
		t.Fatalf("a three-path package produced %d commits in total, want %d", got, want)
	}
	changed := contentPaths(commitPaths(t, r.pair.Local, "HEAD"))
	requireEqual(t, changed, []string{"Alpha.md", "Notes/Delta.md", "Notes/Gamma.md"}, "the applying commit carries")

	entryCovers(t, r, result.JournalEntry, "Alpha.md", "Notes/Delta.md", "Notes/Gamma.md")
	if entries := r.entries(t); len(entries) != 1 {
		t.Fatalf("one package produced %d recovery entries, want 1", len(entries))
	}
	if len(result.Receipt.Applied) != 3 {
		t.Fatalf("receipt acknowledges %d paths, want 3", len(result.Receipt.Applied))
	}
}

// TestApplyRoundTripsBinaryContent covers the third thing the session asks to be
// reviewed. Payload bytes are written verbatim, so a note this package never
// parses — an attachment, or a file carrying a NUL and an invalid UTF-8
// sequence — arrives byte for byte.
func TestApplyRoundTripsBinaryContent(t *testing.T) {
	r := enroll(t)
	binary := []byte{0x00, 0xff, 0xfe, 'P', 'N', 'G', 0x00, 0x1a, 0x0a, 0x80, 0x81}

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Attachments/diagram.png", binary, vaultsync.Absent())
	})
	result := r.mustApply(t, dir)

	got, err := os.ReadFile(r.pair.Local.Path("Attachments/diagram.png"))
	if err != nil {
		t.Fatalf("read the applied attachment: %v", err)
	}
	if string(got) != string(binary) {
		t.Fatalf("the attachment holds %v, want %v", got, binary)
	}
	if result.Receipt.Applied[0].SHA256 != vaultsync.HashBytes(binary) {
		t.Fatal("the receipt does not acknowledge the attachment's own bytes")
	}
}

// TestApplyRefusesAPackageCarryingTwoOperations pins the narrowing doc.go
// documents. A receipt names one recovery entry and the export ledger maps one
// journal entry to one peer operation, so two operations in one package have no
// honest recording and the package is left in the channel untouched.
func TestApplyRefusesAPackageCarryingTwoOperations(t *testing.T) {
	r := enroll(t)
	head := r.pair.Local.Head(t)

	builder := r.build(t)
	builder.BeginOperation("", r.clock.Now())
	builder.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	builder.BeginOperation("", r.clock.Now())
	builder.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	_, dir := r.publish(t, builder)

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrMultipleOperations, "applying a two-operation package")

	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("a refused package moved the content history")
	}
	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("a refused package applied half of itself")
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("a refused package left %d recovery entries behind", len(entries))
	}
}

// TestApplyReturnsAcceptanceRefusalsUnwrapped pins that a package this vault
// could not have accepted is reported under internal/vaultsync's own sentinel.
// An applier that re-wrapped these would make one condition look like two
// different problems to a caller deciding whether to wait, quarantine, or halt.
func TestApplyReturnsAcceptanceRefusalsUnwrapped(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	// A vault restored from a backup advances its epoch, which makes every
	// package still in flight describe a history it no longer has.
	if _, err := vaultsync.AdvanceEpoch(r.root(), vaultsync.Options{Now: r.clock.Tick}); err != nil {
		t.Fatalf("AdvanceEpoch: %v", err)
	}
	head := r.pair.Local.Head(t)

	_, err := r.apply(t, dir)
	requireIs(t, err, vaultsync.ErrStaleEpoch, "applying a package from a superseded epoch")

	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("a refused package moved the content history")
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("a refused package left %d recovery entries behind", len(entries))
	}
}

// TestApplyRefusesAnIncompletePackage pins the wait case: a package whose bytes
// have not all arrived is ErrIncompletePackage and changes nothing, because the
// transport is still running while this code reads.
func TestApplyRefusesAnIncompletePackage(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	// Removing a delivered payload is how a test stands in for a transfer that
	// is still running: the manifest has arrived and the bytes it names have not.
	payload := manifest.Payloads[0].Name
	if err := os.Remove(filepath.Join(dir, "payloads", payload)); err != nil {
		t.Fatalf("remove payload: %v", err)
	}

	_, err := r.apply(t, dir)
	requireIs(t, err, vaultsync.ErrIncompletePackage, "applying a package whose payload has not arrived")
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("a package still in transit left %d recovery entries behind", len(entries))
	}
}

// TestApplyRefusesAPackageIntoAnUnenrolledVault pins that the enrollment record
// is what authorizes an import at all.
func TestApplyRefusesAPackageIntoAnUnenrolledVault(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	// The peer's vault never enrolled this package's origin — it *is* the origin
	// — so applying there is the misconfigured-transport case.
	_, err := Apply(r.pair.Peer.Root, dir, Options{Now: r.clock.Now})
	requireIs(t, err, vaultsync.ErrUnknownPeer, "applying a package on the device that produced it")
}

// TestApplyReportsAnAppliedImportItCouldNotAcknowledge is the one place a
// non-nil Result and a non-nil error come back together, and the reason that
// shape exists.
//
// Publishing the receipt is the last step, and by the time it runs the peer's
// change is on this disk, in this vault's history, and undoable. Reporting only
// an error would tell the caller nothing happened and would withhold the entry
// identity, which is the single thing that makes what did happen reversible. An
// empty ReceiptPath says publication failed. The minted receipt is retained in
// the import ledger, so a later delivery can publish the same statement even if
// another package has moved the note on by then.
func TestApplyReportsAnAppliedImportItCouldNotAcknowledge(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	blockOutgoingChannel(t, r.root())

	result := requireUnpublishedApplication(t, r, dir)
	record := importState(t, r).Applied[result.PackageID]
	if record.Receipt == nil || record.Receipt.PackageID != result.PackageID {
		t.Fatalf("the import ledger did not retain the unpublished receipt: %+v", record)
	}
	if receipt, err := vaultsync.ReadReceipt(vaultsync.OutgoingDir(r.root()), result.PackageID); err == nil || receipt != nil {
		t.Fatalf("the peer can read a receipt whose publication failed: %v, %+v", err, receipt)
	}

	// Everything the receipt would have promised is true anyway, which is why
	// the Result is worth returning.
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q, want the packaged body", got)
	}
	if result.JournalEntry == "" {
		t.Fatal("the result names no recovery entry, so nothing can undo the change it applied")
	}
	entryCovers(t, r, result.JournalEntry, "Alpha.md")
	requireProvenance(t, r, result.JournalEntry, result.OperationID)
	requireEqual(t, contentPaths(commitPaths(t, r.pair.Local, "HEAD")), []string{"Alpha.md"}, "the applying commit carries")
}
