package vaultimport

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// A redelivery is the ordinary case, not the exceptional one: the transport
// copies whole directories and knows nothing about receipts, so the same package
// reappears whenever a channel is re-copied or a peer is restored from a backup.
//
// The test is stated as "nothing moved" across every record at once, because the
// second application would not fail loudly if it happened. The package describes
// a starting point the package itself already replaced, so a naive re-apply
// reports the peer as being in conflict with its own change — a confusing and
// unresolvable answer to a delivery that is in fact already satisfied.
func TestApplyingAPackageTwiceAnswersWithoutApplying(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
		b.Delete("Notes/Gamma.md", r.believes(t, "Notes/Gamma.md"))
	})

	first := r.mustApply(t, dir)
	if first.Duplicate {
		t.Fatal("a first delivery was reported as a duplicate")
	}
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)

	second := r.mustApply(t, dir)
	requireRepeatedAnswer(t, second, first)

	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q after a redelivery", got)
	}
	if exists(t, r.pair.Local, "Notes/Gamma.md") {
		t.Fatal("a redelivery restored a note the package deleted")
	}
	r.requireUnmoved(t, head, commits)
}

// requireRepeatedAnswer asserts that a redelivery was answered with the original
// application described rather than a second one performed.
func requireRepeatedAnswer(t *testing.T, second, first *Result) {
	t.Helper()
	if !second.Duplicate {
		t.Fatal("a redelivery was not reported as one")
	}
	if second.JournalEntry != first.JournalEntry {
		t.Fatalf("a redelivery named entry %s, want the original %s", second.JournalEntry, first.JournalEntry)
	}
	if second.OperationID != first.OperationID {
		t.Fatalf("a redelivery named operation %s, want %s", second.OperationID, first.OperationID)
	}
}

// requireUnmoved asserts that nothing a single application writes was written a
// second time: the content history is where it was, and the two ledgers a peer
// reads back — recovery entries and provenance records — still hold one record
// each for the one import that happened.
func (r *replicas) requireUnmoved(t *testing.T, head string, commits int) {
	t.Helper()
	if now := r.pair.Local.Head(t); now != head {
		t.Fatal("a redelivery moved the content history")
	}
	if now := commitCount(t, r.pair.Local); now != commits {
		t.Fatalf("a redelivery added %d commits", now-commits)
	}
	if entries := r.entries(t); len(entries) != 1 {
		t.Fatalf("a redelivery left %d recovery entries, want 1", len(entries))
	}
	if imported := r.imported(t); len(imported) != 1 {
		t.Fatalf("a redelivery left %d provenance records, want 1: %v", len(imported), imported)
	}
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after a redelivery: %v", dirty)
	}
}

// The reason a producer re-sends is almost always that the reply went missing,
// so the missing reply is worth its own test: a channel can be re-created from a
// backup, cleaned by a person, or simply not synchronized yet.
//
// What comes back must be the *original* receipt, byte for byte. A receipt says
// "this vault durably applied this package under this recovery entry", and the
// producer may drop its only copy of the change on the strength of it; a fresh
// one minted now would carry a new timestamp for an application that happened
// earlier, and — if the entry had since been undone — would be a statement this
// vault is no longer entitled to make. Republishing is the only honest answer
// because it repeats a statement rather than making one.
func TestARedeliveryRepublishesTheReceiptThatWentMissing(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	first := r.mustApply(t, dir)
	if first.Receipt == nil {
		t.Fatal("a successful import published no receipt")
	}

	channel := vaultsync.OutgoingDir(r.root())
	path, original := publishedBytes(t, channel, manifest.PackageID)
	if err := os.Remove(path); err != nil {
		t.Fatalf("discard the receipt: %v", err)
	}
	if _, err := vaultsync.ReadReceipt(channel, manifest.PackageID); !errors.Is(err, vaultsync.ErrIncompletePackage) {
		t.Fatalf("the receipt is still readable after being discarded: %v", err)
	}
	r.clock.Tick()

	second := r.mustApply(t, dir)
	requireRepeatedAnswer(t, second, first)
	if second.ReceiptPath != path {
		t.Fatalf("the republished receipt is at %q, want %q", second.ReceiptPath, path)
	}
	if _, err := vaultsync.ReadReceipt(channel, manifest.PackageID); err != nil {
		t.Fatalf("the receipt was not republished: %v", err)
	}
	if _, republished := publishedBytes(t, channel, manifest.PackageID); !bytes.Equal(republished, original) {
		t.Fatalf("the republished receipt differs from the original:\n got %s\nwant %s", republished, original)
	}
	if got := commitCount(t, r.pair.Local); got != 2 {
		t.Fatalf("answering a lost reply left %d commits, want 2", got)
	}
}

// Records written before unpublished receipts were retained have only the
// recovery entry to prove what the application wrote. A later valid package may
// have moved the live path on, so retrying from today's bytes would strand the
// old package forever. The recovery entry's post-command hash is the durable
// evidence for that older application.
func TestALegacyUnpublishedReceiptIsRecoveredAfterALaterPackage(t *testing.T) {
	r := enroll(t)
	firstManifest, firstDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	blockOutgoingChannel(t, r.root())

	requireUnpublishedApplication(t, r, firstDir)
	if err := os.Remove(vaultsync.OutgoingDir(r.root())); err != nil {
		t.Fatalf("unblock outgoing channel: %v", err)
	}

	// Reproduce the legacy ledger shape already present on an upgraded device:
	// the application and entry were recorded, but the unpublished receipt was
	// discarded.
	state := importState(t, r)
	record := state.Applied[firstManifest.PackageID]
	record.Receipt = nil
	state.Applied[firstManifest.PackageID] = record
	if err := saveState(r.root(), state); err != nil {
		t.Fatalf("save legacy import state: %v", err)
	}

	const laterBody = "---\nstatus: done\ntitle: Alpha\n---\n\nA later package moved Alpha on.\n"
	_, laterDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(laterBody), vaultsync.Existing(vaultsync.HashBytes([]byte(alphaBody))))
	})
	r.mustApply(t, laterDir)

	retried := r.mustApply(t, firstDir)
	if !retried.Duplicate || retried.Receipt == nil {
		t.Fatalf("legacy redelivery returned %+v", retried)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != laterBody {
		t.Fatalf("recovering the old receipt moved the live note back to %q", got)
	}
	if got, want := retried.Receipt.Applied[0].SHA256, vaultsync.HashBytes([]byte(alphaBody)); got != want {
		t.Fatalf("recovered receipt hash = %s, want original application %s", got, want)
	}
	stored := importState(t, r).Applied[firstManifest.PackageID]
	if stored.Receipt == nil || stored.Receipt.Applied[0].SHA256 != retried.Receipt.Applied[0].SHA256 {
		t.Fatalf("the recovered receipt was not retained: %+v", stored)
	}
}

// A legacy convergence record is itself the durable proof that every package
// result was observed: rememberConvergence is reached only after the receipt was
// minted, and old builds discarded that receipt when channel publication failed.
// A later package may move the path on before the old package is redelivered, so
// today's bytes cannot be used to recreate the earlier statement.
func TestALegacyUnpublishedConvergenceReceiptIsRecoveredAfterALaterPackage(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", alphaBody)
	firstManifest, firstDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})
	requireLegacyUnpublishedConvergence(t, r, firstManifest.PackageID, firstDir)

	const laterBody = "---\nstatus: done\ntitle: Alpha\n---\n\nA later package moved Alpha on.\n"
	_, laterDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(laterBody), vaultsync.Existing(vaultsync.HashBytes([]byte(alphaBody))))
	})
	r.mustApply(t, laterDir)

	retried := r.mustApply(t, firstDir)
	if !retried.Duplicate || !retried.Converged || retried.Receipt == nil {
		t.Fatalf("legacy convergence redelivery returned %+v", retried)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != laterBody {
		t.Fatalf("recovering the convergence receipt moved the live note back to %q", got)
	}
	if got, want := retried.Receipt.Applied[0].SHA256, vaultsync.HashBytes([]byte(alphaBody)); got != want {
		t.Fatalf("recovered convergence receipt hash = %s, want original observation %s", got, want)
	}
	stored := importState(t, r).Applied[firstManifest.PackageID]
	if stored.Receipt == nil || stored.Receipt.Applied[0].SHA256 != retried.Receipt.Applied[0].SHA256 {
		t.Fatalf("the recovered convergence receipt was not retained: %+v", stored)
	}
}

func requireLegacyUnpublishedConvergence(t *testing.T, r *replicas, packageID, packageDir string) {
	t.Helper()
	blockOutgoingChannel(t, r.root())
	result, err := r.apply(t, packageDir)
	if err == nil || result == nil || !result.Converged || result.Receipt == nil {
		t.Fatalf("blocked convergence returned result %+v and error %v", result, err)
	}
	if err := os.Remove(vaultsync.OutgoingDir(r.root())); err != nil {
		t.Fatalf("unblock outgoing channel: %v", err)
	}

	// Reproduce the record written by the old build: convergence was proven, but
	// the receipt minted before publication failed was not retained.
	state := importState(t, r)
	record := state.Applied[packageID]
	record.Receipt = nil
	state.Applied[packageID] = record
	if err := saveState(r.root(), state); err != nil {
		t.Fatalf("save legacy convergence state: %v", err)
	}
}

// publishedBytes reads the receipt for packageID as the peer would: the bytes of
// the file in the channel, not the decoded record. "The same receipt" is a claim
// about what the peer reads, so the file is what the test has to compare.
func publishedBytes(t *testing.T, channel, packageID string) (string, []byte) {
	t.Helper()
	path, err := vaultsync.ReceiptPath(channel, packageID)
	if err != nil {
		t.Fatalf("ReceiptPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the published receipt %s: %v", packageID, err)
	}
	return path, data
}

// Republishing runs again on the delivery after that one, and the name it
// publishes under can only be taken once — so the second republication meets its
// own bytes sitting in the channel. That is the state it was trying to reach, and
// it has to be reported as success rather than as ErrPackageExists, or a peer
// whose channel is intact would be told its package failed.
func TestRepublishingOverAReceiptThatIsStillThereSucceeds(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	first := r.mustApply(t, dir)
	channel := vaultsync.OutgoingDir(r.root())
	_, original := publishedBytes(t, channel, manifest.PackageID)

	for range 2 {
		r.clock.Tick()
		again := r.mustApply(t, dir)
		if again.Receipt == nil || !reflect.DeepEqual(again.Receipt, first.Receipt) {
			t.Fatalf("a repeated redelivery answered with a different receipt: %+v", again.Receipt)
		}
	}
	if _, published := publishedBytes(t, channel, manifest.PackageID); !bytes.Equal(published, original) {
		t.Fatal("repeated redeliveries replaced the published receipt")
	}
}

// A conflict is a refusal, and a refusal has to leave the vault exactly as it
// found it — including the parts of the vault nobody looks at.
//
// The attempt record is the part that matters here and the reason this test
// drives Apply rather than the applier's steps. The attempt is declared before
// the transaction commits, so every failure after that point leaves one behind,
// and an attempt that outlives its failure is indistinguishable on disk from a
// process that died: the next import refuses, and a person is asked to resolve
// something that already resolved itself. The entry directory is checked
// separately because journal.ListEntries cannot see it — a directory with no
// manifest is not a recovery entry — so a count of entries would pass over
// exactly the residue this asserts is gone.
func TestAConflictLeavesNoAttemptAndNoResidue(t *testing.T) {
	r := enroll(t)
	stale := vaultsync.Existing(vaultsync.HashBytes([]byte("what Alpha.md has not said since before the fixture")))
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), stale)
	})
	before := r.pair.Local.Read(t, "Alpha.md")
	head := r.pair.Local.Head(t)

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package whose expectation is stale")

	state, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending != nil {
		t.Fatalf("a conflict left attempt %q pending", state.Pending.PackageID())
	}
	if len(state.Applied) != 0 {
		t.Fatalf("a conflict recorded %d applications", len(state.Applied))
	}
	if dirs := entryDirs(t, r.root()); len(dirs) != 0 {
		t.Fatalf("a conflict left entry directories behind: %v", dirs)
	}
	if imported := r.imported(t); len(imported) != 0 {
		t.Fatalf("a conflict left %d provenance records: %v", len(imported), imported)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != before {
		t.Fatalf("a conflict changed Alpha.md to %q", got)
	}
	if now := r.pair.Local.Head(t); now != head {
		t.Fatal("a conflict moved the content history")
	}
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after a conflict: %v", dirty)
	}

	// And the vault is usable immediately, with no recovery step in between: a
	// refusal that needs cleaning up before the next package is a refusal that
	// did not finish.
	_, corrected := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	r.mustApply(t, corrected)
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q after the corrected package", got)
	}
}

// snapshotNeeded is the guard on the one recovery step that can make things
// worse, so it gets tested as the predicate it is rather than only through the
// paths that consult it.
//
// Roll-forward is the right answer for an entry whose manifest is durable, and
// the manifest is durable strictly before the commit — so a recovery pass
// genuinely cannot tell from the journal alone whether the commit happened. It
// has to ask the content history, and asking is cheap — cheaper than taking the
// vault's write lock to discover there is nothing to write. See the sibling test
// below for why it is not the *only* thing asking.
func TestSnapshotNeededAsksTheContentHistoryNotTheJournal(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", r.believes(t, "Notes/Gamma.md"))
	})
	result := r.mustApply(t, dir)

	needed, err := snapshotNeeded(r.root(), result.JournalEntry)
	if err != nil {
		t.Fatalf("snapshotNeeded: %v", err)
	}
	if needed {
		t.Fatal("an entry whose manifest is at the tip was reported as needing a snapshot")
	}
	// An entry the history has never heard of needs one. Recovery reaches this
	// branch when the process died between the manifest and the commit.
	needed, err = snapshotNeeded(r.root(), "20200101T000000Z-000000")
	if err != nil {
		t.Fatalf("snapshotNeeded for an unknown entry: %v", err)
	}
	if !needed {
		t.Fatal("an entry absent from the content history was reported as already committed")
	}

	// ensureSnapshot is the guard applied: running it against an entry already in
	// history must cost nothing and must move nothing.
	head := r.pair.Local.Head(t)
	if err := ensureSnapshot(r.root(), result.JournalEntry, operationToken); err != nil {
		t.Fatalf("ensureSnapshot over an entry already in history: %v", err)
	}
	if now := r.pair.Local.Head(t); now != head {
		t.Fatal("ensureSnapshot committed an entry the history already held")
	}
}

// Asking first is not what keeps the vault safe, and this is where that is
// established.
//
// An entry that deleted a note carries the only authorization for committing
// that deletion, and the authorization only counts while it is uncommitted.
// Committing such an entry a second time meets a path gone from the working tree
// *and* gone from HEAD, which is the shape internal/vaultgit reads as a deletion
// nobody explained — the one failure that latches the vault closed until a person
// clears it. snapshotNeeded stands between a recovery pass and that, but it
// cannot be the only thing standing there: it is answered before the commit takes
// the vault's write lock, and the watcher commits finalized entries too. So the
// unguarded call is the one that has to be harmless, and this calls it directly.
func TestReSnapshottingAnEntryTheHistoryHoldsChangesNothing(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", r.believes(t, "Notes/Gamma.md"))
	})
	result := r.mustApply(t, dir)
	head := r.pair.Local.Head(t)

	if err := noteops.SnapshotEntry(r.root(), result.JournalEntry, operationToken); err != nil {
		t.Fatalf("re-committing an entry the history already holds: %v", err)
	}
	if now := r.pair.Local.Head(t); now != head {
		t.Fatal("re-committing an entry the history already holds moved the history")
	}

	// The damage this would have done is persistent rather than local — a latched
	// vault refuses the *next* mutation before attempting it — so the assertion
	// that nothing was latched has to be a later mutation succeeding.
	_, next := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	if _, err := r.apply(t, next); err != nil {
		t.Fatalf("the vault refuses imports after a repeated snapshot (blocked=%v): %v",
			vaultgit.IsBlocked(err), err)
	}
}

// entryDirs lists the journal's directory entries rather than its recovery
// entries. The difference is the point: journal.ListEntries reports only
// finalized entries, so it is blind to a half-built one, which is precisely the
// residue a failed application has to be shown not to leave.
func entryDirs(t *testing.T, root string) []string {
	t.Helper()
	listed, err := os.ReadDir(noteops.JournalRoot(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read journal root: %v", err)
	}
	names := make([]string, 0, len(listed))
	for _, item := range listed {
		if item.IsDir() {
			names = append(names, filepath.Base(item.Name()))
		}
	}
	sort.Strings(names)
	return names
}
