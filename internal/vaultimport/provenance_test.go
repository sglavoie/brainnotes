package vaultimport

import (
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestProvenanceIsDurableBeforeTheCommitThatNeedsIt drives the applier's steps
// one at a time, because the ordering it checks is only observable between two
// of them.
//
// Apply's contract is that the import ledger knows about the peer's operation
// *before* the commit that carries it exists. Recorded afterwards, a crash in
// between leaves a commit the exporter reads as this device's own work and
// publishes straight back to the peer that sent it, which the peer applies and
// returns. A test that only called Apply would see both facts true at the end
// and could not tell which one arrived first.
//
// What it therefore proves is that the steps *can* be sequenced this way and that
// the window between them looks like this, which is what the contract is about.
// What it cannot prove is that apply() still calls them in this order: that is
// one edit away and nothing here would fail. Catching that needs a crash seam
// between commit and finalize, like internal/vault's txnCrash, and neither
// noteops nor this package has one.
func TestProvenanceIsDurableBeforeTheCommitThatNeedsIt(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	head := r.pair.Local.Head(t)

	session, txn := planned(t, r, dir)
	operation := begun(t, r, session)
	if err := session.commit(txn, operation); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// The peer's bytes are on this disk and nothing else knows it yet. This is
	// the window the ordering exists to close.
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q after the transaction committed", got)
	}
	requireUncommitted(t, r, head)
	if imported := r.imported(t); len(imported) != 0 {
		t.Fatalf("the ledger already holds %v before provenance was recorded", imported)
	}

	if err := session.recordProvenance(operation); err != nil {
		t.Fatalf("recordProvenance: %v", err)
	}

	entryID := requireLedgerNamesTheEntry(t, r, session, operation)
	// The whole point: durable, and the commit it protects still does not exist.
	requireUncommitted(t, r, head)

	entry, err := session.finalize(operation)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if entry.ID != entryID {
		t.Fatalf("finalize produced entry %s, but the ledger names %s", entry.ID, entryID)
	}
	if r.pair.Local.Head(t) == head {
		t.Fatal("finalizing recorded no commit, so the ledger names an entry no history carries")
	}
	requireEqual(t, contentPaths(commitPaths(t, r.pair.Local, "HEAD")), []string{"Alpha.md"}, "the applying commit carries")
}

// TestApplyFinalizesAnImportWhoseProvenanceCouldNotBeRecorded is the other side
// of the ordering, and the failure boundary it creates.
//
// By the time the ledger is written the peer's bytes are on this disk, so there
// is no ordering left to choose: an applied change outside the recovery history
// is the worse of the two failures, and the entry is finalized and snapshotted
// anyway. What the vault is left with is a real, undoable commit that the
// exporter does not know came from a peer — so it will publish the peer's own
// change back to it once, and the origin will see a conflict on a change it
// authored. That is a repair a person makes with one RecordImport call, which is
// why the error has to name its three arguments rather than leave them to be
// reconstructed from a journal listing.
func TestApplyFinalizesAnImportWhoseProvenanceCouldNotBeRecorded(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	head := r.pair.Local.Head(t)
	corruptExportState(t, r.root())

	result, err := r.apply(t, dir)
	if err == nil {
		t.Fatal("an import whose provenance was not recorded was reported as a success")
	}
	if result != nil {
		t.Fatalf("this failure names its entry in the error, so it returns no result: %+v", result)
	}

	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q, want the packaged body", got)
	}
	if r.pair.Local.Head(t) == head {
		t.Fatal("the applied change was left uncommitted, which is the state this ordering exists to avoid")
	}
	entries := r.entries(t)
	if len(entries) != 1 {
		t.Fatalf("the vault holds %d recovery entries, want the one that can undo this", len(entries))
	}
	entryCovers(t, r, entries[0].ID, "Alpha.md")
	requireNames(t, err, entries[0].ID, manifest.Operations[0].OperationID, manifest.OriginDevice)

	// No acknowledgment, so the peer keeps its own copy and sends it again. The
	// channel is not merely empty: PublishReceipt is what creates it, so an
	// import that never got that far has not even opened one.
	if published, readErr := os.ReadDir(vaultsync.OutgoingDir(r.root())); readErr == nil {
		t.Fatalf("an unrecorded import opened an outgoing channel holding %d things", len(published))
	} else if !os.IsNotExist(readErr) {
		t.Fatalf("read outgoing channel: %v", readErr)
	}
}

// corruptExportState makes the export ledger unreadable, which is the cheapest
// honest way to fail RecordImport: it is a real condition the exporter already
// has a sentinel for, rather than a permission bit a root-owned test would skip
// straight past.
func corruptExportState(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(vaultexport.StatePath(root), []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("corrupt export state: %v", err)
	}
}

// requireNames checks that a failure carries every argument its repair needs.
func requireNames(t *testing.T, err error, wanted ...string) {
	t.Helper()
	for _, want := range wanted {
		if want == "" {
			t.Fatal("a repair argument was empty, so this assertion would pass on anything")
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%v does not name %q", err, want)
		}
	}
}

// requireLedgerNamesTheEntry checks that the export ledger maps this operation's
// recovery entry to the peer operation it applied, and returns the entry so the
// caller can hold the finalized one against it.
func requireLedgerNamesTheEntry(t *testing.T, r *replicas, session *applier, operation *noteops.Journal) string {
	t.Helper()
	entryID := operation.EntryID()
	if entryID == "" {
		t.Fatal("the committed transaction captured no recovery entry to name")
	}
	record, ok := r.imported(t)[entryID]
	if !ok {
		t.Fatalf("the ledger does not name recovery entry %s", entryID)
	}
	if record.OperationID != session.operation.OperationID || record.OriginDevice != session.manifest.OriginDevice {
		t.Fatalf("the ledger records %+v, want the packaged operation and origin", record)
	}
	return entryID
}

// requireUncommitted asserts the vault's content history has not moved, which is
// how this file distinguishes "recorded" from "committed".
func requireUncommitted(t *testing.T, r *replicas, head string) {
	t.Helper()
	if now := r.pair.Local.Head(t); now != head {
		t.Fatalf("the applying commit landed early: HEAD moved from %s to %s", head, now)
	}
}
