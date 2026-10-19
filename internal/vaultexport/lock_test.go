package vaultexport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// holdProducer takes the producer guard the way a running pass holds it, and
// releases it when the test ends.
func holdProducer(t *testing.T, root string) {
	t.Helper()
	release, acquired, err := vault.TryLockPath(vaultsync.StateLockPath(root, vaultsync.ExportLockName))
	if err != nil || !acquired {
		t.Fatalf("take the producer guard: acquired=%v err=%v", acquired, err)
	}
	t.Cleanup(release)
}

// openExporter builds a pass over the fixture without running it, so a test can
// interleave another writer between the record being read and being written.
func (f *fixture) openExporter() *exporter {
	f.t.Helper()
	history, err := vaultgit.OpenHistory(context.Background(), f.root(), vaultgit.HistoryOptions{})
	if err != nil {
		f.t.Fatalf("OpenHistory: %v", err)
	}
	exp, err := newExporter(f.root(), f.local, history, Options{ChannelDir: f.channel, Now: f.clock.Now})
	if err != nil {
		f.t.Fatalf("newExporter: %v", err)
	}
	return exp
}

// TestProducersSerializeOnTheGuardAndImportsDoNot pins who waits for whom.
//
// A second export, a Reset, and a rebaseline all write the cursor, so each
// refuses while a pass holds the producer guard. RecordImport writes only the
// import ledger and must not wait for a pass at all: an import whose provenance
// timed out behind an export would be committed unrecognized and echoed.
func TestProducersSerializeOnTheGuardAndImportsDoNot(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	holdProducer(t, f.root())

	if _, err := f.export(Options{}); !errors.Is(err, ErrExportBusy) {
		t.Fatalf("export under a held guard: got %v, want ErrExportBusy", err)
	}
	if err := Reset(f.root()); !errors.Is(err, ErrExportBusy) {
		t.Fatalf("reset under a held guard: got %v, want ErrExportBusy", err)
	}
	if err := RebaselineAfterReviewContext(context.Background(), f.root(), 0); !errors.Is(err, ErrExportBusy) {
		t.Fatalf("rebaseline under a held guard: got %v, want ErrExportBusy", err)
	}

	started := time.Now()
	record := Import{OperationID: "op-" + strings.Repeat("e", 32), OriginDevice: f.peer.DeviceID}
	if err := RecordImport(f.root(), "entry-during-pass", record); err != nil {
		t.Fatalf("RecordImport during a pass: %v", err)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Fatalf("RecordImport waited %s behind the producer guard", waited)
	}
	if got := f.state().Imported["entry-during-pass"]; got != record {
		t.Fatalf("ledger: got %+v, want %+v", got, record)
	}
}

// TestAPassWriteKeepsProvenanceRecordedUnderIt is the merge the per-write lock
// makes necessary. The pass read the record before the import recorded its
// provenance; writing its copy back as it was would drop the entry, and the
// import's commit would then be published to the peer that sent it.
func TestAPassWriteKeepsProvenanceRecordedUnderIt(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	exp := f.openExporter()

	record := Import{OperationID: "op-" + strings.Repeat("f", 32), OriginDevice: f.peer.DeviceID}
	if err := RecordImport(f.root(), "entry-late", record); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}
	// The commit the import makes next is in the pass's range, so the pass must
	// recognize it even though its own copy of the ledger predates the entry.
	imported, isImport, err := exp.importedOperation([]string{"entry-late"})
	if err != nil || !isImport || imported != record {
		t.Fatalf("importedOperation: got %+v, %v, %v; want the late entry", imported, isImport, err)
	}

	next := exp.state.clone()
	next.Paths["Unrelated.md"] = "op-" + strings.Repeat("1", 32)
	delete(next.Imported, "entry-late")
	if err := exp.commitState(next); err != nil {
		t.Fatalf("commitState: %v", err)
	}
	if _, still := f.state().Imported["entry-late"]; still {
		t.Fatal("an entry the pass consumed was merged back from the disk")
	}

	again := Import{OperationID: "op-" + strings.Repeat("2", 32), OriginDevice: f.peer.DeviceID}
	if err := RecordImport(f.root(), "entry-later", again); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}
	if err := exp.commitState(exp.state.clone()); err != nil {
		t.Fatalf("commitState: %v", err)
	}
	if got := f.state().Imported["entry-later"]; got != again {
		t.Fatalf("a pass write dropped provenance recorded under it: got %+v", got)
	}
}

// TestAPassRefusesToWriteOverAMovedCursor covers the one producer write the guard
// cannot exclude: a record replaced from outside the guard mid-pass. Writing the
// pass's copy over it would put the old cursor back.
func TestAPassRefusesToWriteOverAMovedCursor(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	exp := f.openExporter()

	moved := f.state()
	moved.Commit = ""
	moved.Paths = map[string]string{}
	f.putState(moved)

	if err := exp.commitState(exp.state.clone()); !errors.Is(err, ErrStateMoved) {
		t.Fatalf("commitState over a moved cursor: got %v, want ErrStateMoved", err)
	}
	if f.state().Commit != "" {
		t.Fatal("the refused write put the old cursor back")
	}
}
