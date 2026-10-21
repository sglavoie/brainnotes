package vaultsyncd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

func TestRestoredVaultIsQuarantinedBeforeAnyWorkerRuns(t *testing.T) {
	w := enroll(t)
	review, err := vaultsync.PrepareRestoreReviewContext(context.Background(), w.root(), strings.Repeat("a", 64), w.clock.Tick)
	if err != nil {
		t.Fatalf("PrepareRestoreReviewContext: %v", err)
	}
	pass := w.once(w.root())
	if len(pass.Units) != 0 {
		t.Fatalf("workers ran against restored causal state: %+v", pass.Units)
	}
	if !pass.Status.Is(StateRestoreReview) || !pass.Status.NeedsPerson() {
		t.Fatalf("restore review status = %+v", pass.Status)
	}
	if pass.Status.RestoreReview == nil || pass.Status.RestoreReview.ProposedDeviceID != review.ProposedDeviceID {
		t.Fatalf("restore review detail = %+v", pass.Status.RestoreReview)
	}
}

// TestABlockedHistoryStopsApplyingAndIsNeverSilent covers the failure that looks
// most like idleness. A blocked vault records nothing, so it produces nothing to
// export and no error to see, and a status model that reported only what the
// channels contain would call it up to date.
//
// The export still runs: it reads committed objects and writes only under
// `.bnotes/sync/`, so a blocked vault can still ship what it committed before it
// was blocked. Only the import stops, because every application it attempted
// would be refused identically.
func TestABlockedHistoryStopsApplyingAndIsNeverSilent(t *testing.T) {
	w := enroll(t)
	w.write(w.peerRoot(), "Sync/Peer.md", "written on the pi\n")
	w.once(w.peerRoot())
	w.transport(w.peerRoot(), w.root())
	w.write(w.root(), localNote, localBody)

	blockHistory(t, w.root())

	pass := w.once(w.root())
	if !pass.Status.Local.Blocked || pass.Status.Headline() != StateBlocked {
		t.Fatalf("a blocked vault reports %v: %+v", pass.Status.States, pass.Status.Local)
	}
	if pass.Status.Local.BlockReason == "" || !pass.Status.NeedsPerson() {
		t.Fatalf("a blocked vault explains nothing: %+v", pass.Status.Local)
	}

	imported := pass.unit(t, WorkerImport)
	if imported.Ran || imported.Skipped == "" {
		t.Fatalf("the import worker ran against a blocked vault: %+v", imported)
	}
	if pass.Status.Peer.AwaitingApplication != 1 {
		t.Fatalf("the peer's package was not left alone: %+v", pass.Status.Peer)
	}
	if exported := pass.exported(t); exported.Packages == 0 {
		t.Fatalf("a blocked vault published nothing it had already committed: %+v", exported)
	}
}

// TestAConflictIsHeldNamedAndWaitedOn. Two people edited the same note, so the
// vault that receives the second version cannot decide alone — and must say
// which note it is stuck on rather than only that it is stuck.
func TestAConflictIsHeldNamedAndWaitedOn(t *testing.T) {
	w := enroll(t)
	exchange(t, w)

	w.write(w.root(), "Alpha.md", "the mac's version\n")
	w.write(w.peerRoot(), "Alpha.md", "the pi's version\n")
	w.once(w.root())
	w.transport(w.root(), w.peerRoot())

	pass := w.once(w.peerRoot())
	held := pass.imported(t)
	if held.Conflicts != 1 || !contains(held.ConflictPaths, "Alpha.md") {
		t.Fatalf("the drain reported %+v", held)
	}
	if !pass.Status.Is(StateConflict) || pass.Status.Headline() != StateConflict {
		t.Fatalf("a held conflict reports %v", pass.Status.States)
	}
	if !pass.Status.NeedsPerson() {
		t.Fatal("a conflict is not waiting for a person")
	}
	if len(pass.Status.Peer.Conflicts) != 1 || !contains(pass.Status.Peer.Conflicts[0].Paths, "Alpha.md") {
		t.Fatalf("the status names %+v", pass.Status.Peer.Conflicts)
	}
	// Nothing was overwritten while it waits.
	if body := w.pair.Peer.Read(t, "Alpha.md"); body != "the pi's version\n" {
		t.Fatalf("the peer's own version was replaced by %q", body)
	}
}

// TestADisconnectionIsInferredFromSilenceAndClearedByAnything covers the one
// state this package infers rather than observes.
//
// The transport is a separate program that must never touch a live vault, so
// what can be seen from here is staleness: a package waiting a long time with
// nothing arriving in the meantime.
func TestADisconnectionIsInferredFromSilenceAndClearedByAnything(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	opts := w.options()
	opts.DisconnectedAfter = DefaultDisconnectedAfter

	published := w.onceWith(w.root(), opts)
	if published.Status.Is(StateDisconnected) {
		t.Fatalf("a package published a moment ago reports %v", published.Status.States)
	}

	w.clock.Advance(DefaultDisconnectedAfter + time.Minute)
	stale := w.readWith(w.root(), opts)
	if !stale.Is(StateDisconnected) || !stale.NeedsPerson() {
		t.Fatalf("after a long silence the vault reports %v", stale.States)
	}

	// Anything arriving from the peer ends it, even though it acknowledges
	// nothing: the inference is about the transport, not about this package.
	// LastDeliveryAt is a filesystem observation rather than this vault's clock,
	// which is what makes a file that has just been copied in read as recent.
	w.once(w.peerRoot())
	w.transport(w.peerRoot(), w.root())
	connected := w.readWith(w.root(), opts)
	if connected.Is(StateDisconnected) {
		t.Fatalf("a delivery did not end the silence: %v, %+v", connected.States, connected.Peer)
	}
	if connected.Transport.AwaitingReceipt != 1 {
		t.Fatalf("the package stopped awaiting a receipt: %+v", connected.Transport)
	}
}

// TestAnUnusableCompanionRecordIsDiscardedRatherThanRefused. The record holds
// scheduling state and nothing else, so the safe response to an unreadable one
// is to start it again — at the cost of one early retry — and say so.
func TestAnUnusableCompanionRecordIsDiscardedRatherThanRefused(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	corruptCompanionRecord(t, w.root())

	var events []Event
	opts := w.options()
	opts.Observe = func(event Event) { events = append(events, event) }
	pass := w.onceWith(w.root(), opts)

	if len(pass.Units) != len(workerOrder) {
		t.Fatalf("an unusable record stopped the pass: %+v", pass.Units)
	}
	if !contains(kinds(events), EventProblem) {
		t.Fatalf("the companion said nothing about a record it threw away: %v", kinds(events))
	}
	if _, err := LoadRecord(w.root()); err != nil {
		t.Fatalf("the record is still unusable after a pass: %v", err)
	}
}

// TestAnUnreadableCompanionRecordIsNeverReadAsAHealthyVault is the other half of
// the decision above, made by the other caller.
//
// A companion that cannot read the record loses one retry and carries on. A
// *reader* that did the same would report an empty worker list, which is
// indistinguishable from a vault whose workers have never failed — so a worker
// that has been failing for two days behind a truncated record would read as
// healthy and `bn-vault-sync -check` would exit zero.
func TestAnUnreadableCompanionRecordIsNeverReadAsAHealthyVault(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	corruptCompanionRecord(t, w.root())

	status := w.status(w.root())
	if len(status.Workers) != 0 {
		t.Fatalf("an unreadable record produced worker rows: %+v", status.Workers)
	}
	if !problemAbout(status, "companion record") {
		t.Fatalf("the read said nothing about the record it could not use: %+v", status.Problems)
	}
	if !status.NeedsPerson() {
		t.Fatalf("a vault whose scheduling cannot be read reports %v and needs nobody", status.States)
	}
}

// TestSeveralStatesHoldAtOnceAndNoneIsConflatedWithAnother builds one vault in
// four conditions simultaneously, which is the ordinary case rather than a
// contrived one: somebody is typing, an earlier edit is committed and not yet
// published, a package is published and not yet acknowledged, and the peer's
// package is on this disk and not yet in this vault.
//
// The states are a set for exactly this reason, and every count beside them is
// about one of the four and no other. None of it needs a person: all four are
// work in flight.
func TestSeveralStatesHoldAtOnceAndNoneIsConflatedWithAnother(t *testing.T) {
	w := enroll(t)

	// Published, and nothing has acknowledged it.
	w.write(w.root(), localNote, localBody)
	w.once(w.root())

	// Committed after that pass, so the content history holds it and the channel
	// does not.
	w.write(w.root(), "Sync/Second.md", "the second note\n")

	// Delivered by the transport and not applied, which is the peer's change on
	// this disk and not in this vault.
	w.write(w.peerRoot(), "Sync/Peer.md", "written on the pi\n")
	w.once(w.peerRoot())
	w.transport(w.peerRoot(), w.root())

	// On disk and not yet in the content history.
	openAnEditBurst(t, w, w.root())

	status := w.status(w.root())
	requireStates(t, status, StatePeerApplication, StateLocalSaved, StateExportPending, StateTransportPending)
	if status.Local.SavedAt.IsZero() {
		t.Fatalf("the open edit burst is invisible: %+v", status.Local)
	}
	if status.Export.PendingCommits == 0 {
		t.Fatalf("the unpublished commit is invisible: %+v", status.Export)
	}
	if status.Transport.AwaitingReceipt != 1 || status.Transport.Acknowledged != 0 {
		t.Fatalf("a package nobody acknowledged: %+v", status.Transport)
	}
	if status.Peer.AwaitingApplication != 1 || status.Peer.Applied != 0 {
		t.Fatalf("a delivered package counted as applied: %+v", status.Peer)
	}
	if status.NeedsPerson() {
		t.Fatalf("four kinds of work in flight asked for a person: %v, %+v", status.States, status.Problems)
	}
}

// TestReadingStatusNeverWaitsForACompanion. A status command is most needed
// while something is running, so a read that took the companion's own lock would
// be answerable exactly when nobody needs it.
func TestReadingStatusNeverWaitsForACompanion(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())

	release, acquired, err := vaultTryLock(w.root())
	if err != nil || !acquired {
		t.Fatalf("take the lock: acquired=%v err=%v", acquired, err)
	}
	defer release()

	status := w.status(w.root())
	if !status.Enrolled || status.Transport.Published != 1 {
		t.Fatalf("the read under a held lock reported %+v", status.Transport)
	}
}

// readWith reads status with options a test has adjusted.
func (w *world) readWith(root string, opts Options) *Status {
	w.t.Helper()
	status, err := Read(context.Background(), root, opts.readOptions())
	if err != nil {
		w.t.Fatalf("Read(%s): %v", filepath.Base(root), err)
	}
	return status
}

// corruptCompanionRecord replaces the scheduling record with something that is
// not one.
func corruptCompanionRecord(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(RecordPath(root), []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt the companion record: %v", err)
	}
}

// problemAbout reports whether the read named subject as something it could not
// determine.
func problemAbout(status *Status, subject string) bool {
	for _, problem := range status.Problems {
		if problem.Subject == subject {
			return true
		}
	}
	return false
}

// openAnEditBurst puts content on disk and tells internal/vaultgit's watcher
// about it, without committing.
//
// That watcher's record is the only place StateLocalSaved is read from, and
// bn-vault-guard is its only writer — see the package documentation. Doing it
// here is what makes this an edit somebody is in the middle of typing rather
// than an untracked file nothing has noticed.
func openAnEditBurst(t *testing.T, w *world, root string) {
	t.Helper()
	w.vaultAt(root).Write(t, "Sync/Unsaved.md", "still being typed\n")
	manager, err := vaultgit.Open(context.Background(), root, vaultgit.Options{Now: w.clock.Now})
	if err != nil {
		t.Fatalf("open the content history: %v", err)
	}
	watch, err := manager.NoteExternalChange()
	if err != nil {
		t.Fatalf("note an external change: %v", err)
	}
	if watch.FirstChangeAt.IsZero() {
		t.Fatalf("the watcher recorded no burst: %+v", watch)
	}
}

// blockHistory latches internal/vaultgit's fail-closed marker the way a vault
// reaches it in life: a tracked note vanishes with nothing in the journal
// authorizing its removal, and the scan that finds it refuses to commit.
func blockHistory(t *testing.T, root string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "Alpha.md")); err != nil {
		t.Fatalf("remove a tracked note: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "Alpha.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := vaultgit.Open(context.Background(), root, vaultgit.Options{})
	if err != nil {
		t.Fatalf("open the content history: %v", err)
	}
	if _, err := manager.CommitPending(context.Background()); err == nil {
		t.Fatal("an ambiguous type change was committed")
	}
	if _, blocked := manager.Blocked(); !blocked {
		t.Fatal("the ambiguous type change did not latch the block marker")
	}
}

// TestAHaltedProducerIsNeverReadAsAnUpToDateVault covers the one export failure
// that leaves nothing behind to measure.
//
// internal/vaultexport resolves an interrupted publication *before* it checks
// the enrollment epoch, and resolving one advances the cursor durably — so the
// vault a halted producer leaves has its cursor at the tip, nothing reserved,
// and nothing pending. Every number on the status says "up to date", the export
// worker is never asked again, and the failure streak that was the last evidence
// is cleared the moment it is skipped as idle. The halt itself has to be named,
// or the vault stops synchronizing and says nothing.
func TestAHaltedProducerIsNeverReadAsAnUpToDateVault(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())

	// The reading the halt would otherwise hide behind, taken before it exists.
	settled := w.status(w.root())
	if settled.Export.PendingCommits != 0 || settled.Export.Reserved || len(settled.Problems) != 0 {
		t.Fatalf("the vault had something left to publish before the halt: %+v", settled.Export)
	}

	stampAnotherEpoch(t, w.root())

	status := w.status(w.root())
	if !problemAbout(status, "export cursor") {
		t.Fatalf("a halted producer reported no problem: %+v", status.Problems)
	}
	if !status.NeedsPerson() {
		t.Fatalf("a halted producer reports %v and needs nobody", status.States)
	}

	// And it is loud rather than merely noted: a status that could not determine
	// something runs the export anyway, so the refusal becomes a recorded worker
	// failure carrying its own evidence.
	pass := w.once(w.root())
	if unit := pass.unit(t, WorkerExport); !unit.Ran || unit.Error == "" {
		t.Fatalf("the export worker was skipped over the halt: %+v", unit)
	}
	if !pass.Status.Is(StateFailing) {
		t.Fatalf("a halted producer reports %v", pass.Status.States)
	}
}

// stampAnotherEpoch strands the export cursor in a previous enrollment
// generation, by advancing the enrollment the way restoring a replica from a
// backup does. The cursor is left exactly as the last pass wrote it, so the
// record under test is the producer's own rather than one a test composed.
func stampAnotherEpoch(t *testing.T, root string) {
	t.Helper()
	state, err := vaultexport.LoadState(root)
	if err != nil {
		t.Fatalf("load the export record: %v", err)
	}
	if state.Commit == "" {
		t.Fatalf("the export record has no cursor to strand: %+v", state)
	}
	if _, err := vaultsync.AdvanceEpoch(root, vaultsync.Options{}); err != nil {
		t.Fatalf("advance the enrollment epoch: %v", err)
	}
}

// TestABranchResetBehindTheCursorIsNotReadAsAnUpToDateVault is the third halt,
// and the one no field on the status carries.
//
// A branch moved back behind the export cursor leaves `cursor..tip` empty, so
// the producer position reads as "nothing left to publish" while every export
// from here refuses: the cursor is no longer an ancestor of the tip and
// internal/vaultexport halts until somebody re-baselines. The count the cursor
// reading already takes is enough to tell, so it is inferred rather than asked
// for with another git call.
func TestABranchResetBehindTheCursorIsNotReadAsAnUpToDateVault(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	behind := strings.TrimSpace(w.vaultAt(w.root()).Git(t, "rev-parse", "HEAD"))

	w.write(w.root(), "Sync/Second.md", "the second note\n")
	w.once(w.root())
	settled := w.status(w.root())
	if settled.Export.PendingCommits != 0 || len(settled.Problems) != 0 {
		t.Fatalf("the vault had something left to publish before the reset: %+v", settled.Export)
	}

	w.vaultAt(w.root()).Git(t, "reset", "--hard", behind)

	status := w.status(w.root())
	if status.Export.PendingCommits != 0 {
		t.Fatalf("a tip behind the cursor measured %d pending commits", status.Export.PendingCommits)
	}
	if !problemAbout(status, "export cursor") {
		t.Fatalf("a branch reset behind the cursor reported no problem: %+v", status.Problems)
	}
	if !status.NeedsPerson() {
		t.Fatalf("a halted producer reports %v and needs nobody", status.States)
	}
}
