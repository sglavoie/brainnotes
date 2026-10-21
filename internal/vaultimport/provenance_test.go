package vaultimport

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// TestProvenanceIsDurableBeforeTheVaultIsTouched looks at the disk from inside
// Apply, at the instant the attempt becomes durable and before the transaction
// commits.
//
// Apply's contract is that the import ledger knows about the peer's operation
// *before* the commit that carries it exists. Recorded afterwards, a crash in
// between — or a ledger write that failed — leaves a commit the exporter reads as
// this device's own work and publishes straight back to the peer that sent it,
// which the peer applies and returns. A test that only called Apply would see
// both facts true at the end and could not tell which one arrived first, so the
// assertion is made from the crash seam rather than after the call: that is what
// pins apply()'s order rather than merely showing the order is possible.
func TestProvenanceIsDurableBeforeTheVaultIsTouched(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	head := r.pair.Local.Head(t)
	original := r.pair.Local.Read(t, "Alpha.md")

	var pinned string
	importCrashHook = func(point string) {
		if point != crashDeclared {
			return
		}
		pinned = importState(t, r).Pending.JournalEntry
		if _, known := r.imported(t)[pinned]; !known {
			t.Fatalf("the attempt is declared under %s and the ledger does not name it", pinned)
		}
		if got := r.pair.Local.Read(t, "Alpha.md"); got != original {
			t.Fatalf("Alpha.md already holds %q before the transaction committed", got)
		}
		requireUncommitted(t, r, head)
	}
	t.Cleanup(func() { importCrashHook = nil })

	result := r.mustApply(t, dir)
	if pinned == "" {
		t.Fatal("the declared crash point never fired, so nothing above was asserted")
	}
	if result.JournalEntry != pinned {
		t.Fatalf("the application finalized entry %s, but the ledger was told %s", result.JournalEntry, pinned)
	}
	requireEqual(t, contentPaths(commitPaths(t, r.pair.Local, "HEAD")), []string{"Alpha.md"}, "the applying commit carries")
}

// TestApplyRefusesAnImportWhoseProvenanceCouldNotBeRecorded is the failure the
// early ordering buys. The ledger write happens before anything else does, so a
// ledger that cannot be written refuses an import that has touched nothing —
// rather than, as when the ledger was written after the transaction, finalizing
// an undoable commit the exporter would then echo to the peer that authored it.
func TestApplyRefusesAnImportWhoseProvenanceCouldNotBeRecorded(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	corruptExportState(t, r.root())

	_, err := r.apply(t, dir)
	if err == nil {
		t.Fatal("an import whose provenance was not recorded was reported as a success")
	}
	requireNames(t, err, manifest.PackageID, manifest.Operations[0].OperationID, manifest.OriginDevice)
	requireUntouched(t, r)
}

// TestApplyDuringAnExportNeitherWaitsOnItNorEchoes runs an import while an
// export pass holds the producer guard, the way the companion's export and a
// conflict resolution from the browser overlap. RecordImport takes only the
// export record's per-write lock, so the import proceeds; and the commit it made
// is recognized by the next export rather than sent back.
func TestApplyDuringAnExportNeitherWaitsOnItNorEchoes(t *testing.T) {
	r := enroll(t)
	r.exportLocal(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	release, acquired, err := vault.TryLockPath(vaultsync.StateLockPath(r.root(), vaultsync.ExportLockName))
	if err != nil || !acquired {
		t.Fatalf("hold the producer guard: acquired=%v err=%v", acquired, err)
	}
	result := r.mustApply(t, dir)
	release()

	if _, known := r.imported(t)[result.JournalEntry]; !known {
		t.Fatalf("the ledger does not name entry %s", result.JournalEntry)
	}
	requireNoEcho(t, r)
}

// TestApplyRefusesCleanlyWhileTheExportRecordStaysLocked is the bounded case the
// per-write lock still leaves: a writer that holds export.json's own lock past
// the budget. The import is refused before it touches anything — no attempt, no
// entry, no content — and the next pass applies it with its provenance intact.
func TestApplyRefusesCleanlyWhileTheExportRecordStaysLocked(t *testing.T) {
	r := enroll(t)
	r.exportLocal(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	release, err := vault.LockPaths(r.root(), vaultexport.StatePath(r.root()))
	if err != nil {
		t.Fatalf("hold the export record's lock: %v", err)
	}
	_, applyErr := r.apply(t, dir)
	release()
	if !vault.IsTxnLockBusy(applyErr) {
		t.Fatalf("Apply under a held export record: got %v, want a busy lock", applyErr)
	}
	requireUntouched(t, r)

	result := r.mustApply(t, dir)
	if _, known := r.imported(t)[result.JournalEntry]; !known {
		t.Fatalf("the retried import left no provenance for entry %s", result.JournalEntry)
	}
	requireNoEcho(t, r)
}

// exportLocal runs one export pass over the local vault into its own channel,
// so a later pass describes commits rather than a baseline.
func (r *replicas) exportLocal(t *testing.T) *vaultexport.Result {
	t.Helper()
	result, err := vaultexport.Export(context.Background(), vaultexport.Options{Root: r.root(), Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return result
}

// requireNoEcho exports the local vault and checks the import's commit was
// skipped as the peer's rather than published back to it.
func requireNoEcho(t *testing.T, r *replicas) {
	t.Helper()
	result := r.exportLocal(t)
	if len(result.Packages) != 0 {
		t.Fatalf("the peer's own change was echoed back in %d packages", len(result.Packages))
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reason != vaultexport.SkipImported {
		t.Fatalf("export skipped %+v, want the import's commit as imported", result.Skipped)
	}
}

// requireUntouched asserts a refused import left nothing behind: the content,
// the content history, the recovery history, the attempt record, and the
// outgoing channel are all as they were.
func requireUntouched(t *testing.T, r *replicas) {
	t.Helper()
	if got := r.pair.Local.Read(t, "Alpha.md"); got == alphaBody {
		t.Fatal("Alpha.md holds the packaged body after a refused import")
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("a refused import left %d recovery entries", len(entries))
	}
	if state := importState(t, r); state.Pending != nil {
		t.Fatalf("a refused import left a pending attempt: %+v", state.Pending)
	}
	receipts, err := os.ReadDir(vaultsync.ReceiptsDir(vaultsync.OutgoingDir(r.root())))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read receipts channel: %v", err)
	}
	if len(receipts) != 0 {
		t.Fatalf("a refused import published %d receipts", len(receipts))
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

// requireUncommitted asserts the vault's content history has not moved, which is
// how this file distinguishes "recorded" from "committed".
func requireUncommitted(t *testing.T, r *replicas, head string) {
	t.Helper()
	if now := r.pair.Local.Head(t); now != head {
		t.Fatalf("the applying commit landed early: HEAD moved from %s to %s", head, now)
	}
}
