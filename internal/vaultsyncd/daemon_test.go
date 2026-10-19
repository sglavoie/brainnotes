package vaultsyncd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestABackoffIsDurableAndIsADelayRatherThanALatch drives a worker into a real,
// repeatable failure and then restarts the companion around it.
//
// Every pass here is a separate process as far as the schedule is concerned:
// Once builds a fresh companion and reads the record back from disk, so a
// deferral in the second pass is evidence that the backoff survived a restart
// and not that something was remembered in memory.
func TestABackoffIsDurableAndIsADelayRatherThanALatch(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())

	// A corrupt export record is a failure no retry clears and every pass meets
	// identically, which is exactly the shape a backoff exists for.
	corruptExportRecord(t, w.root())

	first := w.once(w.root())
	if first.unit(t, WorkerExport).Error == "" {
		t.Fatalf("the export worker did not fail: %+v", first.unit(t, WorkerExport))
	}
	if !first.Status.Is(StateFailing) || !first.Status.NeedsPerson() {
		t.Fatalf("a failing worker reports %v", first.Status.States)
	}
	requireBackedOff(t, first.Status, 1, w.clock.Now().Add(MinBackoff))

	second := w.once(w.root())
	if deferred := second.unit(t, WorkerExport); deferred.Ran || deferred.Skipped == "" {
		t.Fatalf("the export worker was not deferred: %+v", deferred)
	}

	// The moment it has elapsed, the worker runs again — and fails again, which
	// lengthens the wait rather than ending it.
	w.clock.Advance(MinBackoff)
	third := w.once(w.root())
	if third.unit(t, WorkerExport).Error == "" {
		t.Fatalf("the export worker did not run again: %+v", third.unit(t, WorkerExport))
	}
	requireBackedOff(t, third.Status, 2, w.clock.Now().Add(2*MinBackoff))
}

// requireBackedOff asserts the durable half of the schedule: how many times in a
// row the export worker has failed, and the moment it may be tried again.
//
// The expected moment is passed in rather than computed from backoffFor, so the
// assertion is against the doubling this test claims and not against the
// function that produces it.
func requireBackedOff(t *testing.T, status *Status, failures int, nextAttempt time.Time) {
	t.Helper()
	worker := workerStatus(t, status, WorkerExport)
	if worker.Failures != failures {
		t.Fatalf("failures: %d, want %d", worker.Failures, failures)
	}
	if !worker.NextAttemptAfter.Equal(nextAttempt) {
		t.Fatalf("next attempt %s, want %s", worker.NextAttemptAfter, nextAttempt)
	}
}

// TestAWorkerThatIsDueAndFindsNothingToDoStopsReportingAFailure covers the one
// way out of a failure streak that is not a success.
//
// A streak is otherwise cleared only by a worker running cleanly, so a worker
// that failed and then stopped being asked would carry StateFailing — and
// `bn-vault-sync -check`'s exit 1 — forever, on the evidence of a failure
// nothing is retrying. Being due, being asked, and having nothing to do is not a
// failing worker.
func TestAWorkerThatIsDueAndFindsNothingToDoStopsReportingAFailure(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	// The record as it stood with everything published, so the repair below is a
	// real one rather than a second plausible file.
	repair := keepFile(t, vaultexport.StatePath(w.root()))

	corruptExportRecord(t, w.root())
	requireExportFailing(t, w.once(w.root()))

	// A person repairs it, and by the time the backoff elapses there is nothing
	// left for the worker to publish.
	repair()
	w.clock.Advance(MinBackoff)

	pass := w.once(w.root())
	if skipped := pass.unit(t, WorkerExport); skipped.Ran || skipped.Skipped == "" {
		t.Fatalf("the export worker ran: %+v", skipped)
	}
	requireStreakCleared(t, w, pass.Status)
}

// requireExportFailing asserts that the export worker failed and that the vault
// says so loudly enough to reach a person.
func requireExportFailing(t *testing.T, pass *Pass) {
	t.Helper()
	if unit := pass.unit(t, WorkerExport); unit.Error == "" {
		t.Fatalf("the export worker did not fail: %+v", unit)
	}
	if !pass.Status.Is(StateFailing) || !pass.Status.NeedsPerson() {
		t.Fatalf("a failing worker reports %v", pass.Status.States)
	}
}

// requireStreakCleared asserts that no evidence of the failure is left — in the
// status, in the states, or in the record a restarted companion would load.
func requireStreakCleared(t *testing.T, w *world, status *Status) {
	t.Helper()
	worker := workerStatus(t, status, WorkerExport)
	if worker.Failures != 0 || worker.LastError != "" || !worker.NextAttemptAfter.IsZero() {
		t.Fatalf("the streak outlived the failure it was evidence of: %+v", worker)
	}
	if status.Is(StateFailing) || status.NeedsPerson() {
		t.Fatalf("a vault with nothing to do reports %v", status.States)
	}
	record, err := LoadRecord(w.root())
	if err != nil {
		t.Fatalf("load the companion record: %v", err)
	}
	if !record.ready(WorkerExport, w.clock.Now()) {
		t.Fatalf("the saved record still defers the export worker: %+v", record.worker(WorkerExport))
	}
}

// keepFile copies a file's bytes and reports the function that puts them back.
func keepFile(t *testing.T, path string) func() {
	t.Helper()
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return func() {
		t.Helper()
		if err := os.WriteFile(path, saved, 0o644); err != nil {
			t.Fatalf("restore %q: %v", path, err)
		}
	}
}

// TestAFailingWorkerDoesNotStopTheOthers is the other half of the same
// arrangement: the units are independent, so a producer that cannot run must not
// take the consumer down with it.
func TestAFailingWorkerDoesNotStopTheOthers(t *testing.T) {
	w := enroll(t)
	published := exchange(t, w)
	corruptExportRecord(t, w.root())

	pass := w.once(w.root())
	if pass.unit(t, WorkerExport).Error == "" {
		t.Fatalf("the export worker did not fail: %+v", pass.unit(t, WorkerExport))
	}
	if retired := pass.retired(t).Retired; !contains(retired, published) {
		t.Fatalf("retired %v, want %s among them despite the export failure", retired, published)
	}
}

// TestASecondCompanionIsRefusedRatherThanQueued covers single-instance
// enforcement, in both directions: held, a companion refuses; released, the next
// one runs.
func TestASecondCompanionIsRefusedRatherThanQueued(t *testing.T) {
	w := enroll(t)
	release, acquired, err := vaultTryLock(w.root())
	if err != nil {
		t.Fatalf("take the lock: %v", err)
	}
	if !acquired {
		t.Fatal("the lock on a fresh vault was not free")
	}

	pass, err := Once(context.Background(), w.root(), w.options())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("a second companion got %v, want ErrAlreadyRunning", err)
	}
	if pass != nil {
		t.Fatalf("a refused companion reported a pass: %+v", pass)
	}

	release()
	if _, err := Once(context.Background(), w.root(), w.options()); err != nil {
		t.Fatalf("after the lock was released: %v", err)
	}
}

// TestTheLoopStopsWhenItIsAskedTo covers graceful shutdown: the loop returns,
// returns no error, and says on the way out that it stopped.
//
// The interval is deliberately far shorter than the work, so a loop that ignored
// the cancellation would keep passing rather than hanging — a failure this test
// would see as extra events rather than as a timeout.
func TestTheLoopStopsWhenItIsAskedTo(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []Event
	opts := w.options()
	opts.Interval = time.Millisecond
	opts.Observe = func(event Event) {
		events = append(events, event)
		if event.Kind == EventStatus {
			cancel()
		}
	}

	if err := Run(ctx, w.root(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	observed := kinds(events)
	if len(observed) < 2 || observed[0] != EventStarted || observed[len(observed)-1] != EventStopped {
		t.Fatalf("the loop said %v", observed)
	}
	if !contains(observed, EventExported) {
		t.Fatalf("the loop stopped without doing its first pass: %v", observed)
	}
	// One pass ran, so the cancellation was seen at the first opportunity and not
	// several ticks later.
	if count := countKind(observed, EventStatus); count != 1 {
		t.Fatalf("the loop ran %d passes after being cancelled once", count)
	}
}

// TestAnUnenrolledVaultIsLeftEntirelyAlone. Almost every vault is this one, and
// the companion running against it must be a read and a log line — no workers,
// no record, nothing written.
func TestAnUnenrolledVaultIsLeftEntirelyAlone(t *testing.T) {
	w := unenrolled(t)
	var events []Event
	opts := w.options()
	opts.Observe = func(event Event) { events = append(events, event) }

	pass := w.onceWith(w.root(), opts)
	if pass.Enrolled {
		t.Fatal("an unenrolled vault reported an enrollment")
	}
	if len(pass.Units) != 0 {
		t.Fatalf("workers ran against an unenrolled vault: %+v", pass.Units)
	}
	requireStates(t, pass.Status, StateUnenrolled)
	if observed := kinds(events); !contains(observed, EventUnenrolled) {
		t.Fatalf("the companion said %v", observed)
	}
	if _, err := os.Stat(RecordPath(w.root())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a companion record was written for an unenrolled vault: %v", err)
	}
}

// TestBackoffSaturates is the arithmetic on its own: it doubles, it never
// exceeds the ceiling, and it never returns something a scheduler would treat as
// "immediately".
func TestBackoffSaturates(t *testing.T) {
	// No failures is no delay rather than a short one: a worker that has not
	// failed is not being backed off.
	if got := backoffFor(0); got != 0 {
		t.Fatalf("backoffFor(0) = %s, want no delay at all", got)
	}
	if got := backoffFor(1); got != MinBackoff {
		t.Fatalf("backoffFor(1) = %s, want %s", got, MinBackoff)
	}
	previous := time.Duration(0)
	for failures := 1; failures < 64; failures++ {
		got := backoffFor(failures)
		switch {
		case got < MinBackoff:
			t.Fatalf("backoffFor(%d) = %s, below the floor", failures, got)
		case got > MaxBackoff:
			t.Fatalf("backoffFor(%d) = %s, above the ceiling", failures, got)
		case got < previous:
			t.Fatalf("backoffFor(%d) = %s, shorter than the previous %s", failures, got, previous)
		}
		previous = got
	}
	if got := backoffFor(1_000_000); got != MaxBackoff {
		t.Fatalf("backoffFor(1000000) = %s, want the ceiling %s", got, MaxBackoff)
	}
}

// corruptExportRecord replaces the producer's durable record with something that
// is not one.
func corruptExportRecord(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(vaultexport.StatePath(root), []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt the export record: %v", err)
	}
}

func workerStatus(t *testing.T, status *Status, name WorkerName) WorkerStatus {
	t.Helper()
	for _, worker := range status.Workers {
		if worker.Name == name {
			return worker
		}
	}
	t.Fatalf("the status carries no %s worker: %+v", name, status.Workers)
	return WorkerStatus{}
}

func countKind(observed []EventKind, want EventKind) int {
	count := 0
	for _, kind := range observed {
		if kind == want {
			count++
		}
	}
	return count
}

// TestARefusalThatOnlyProvesTheVaultIsBusyKeepsTheStreak is the boundary of the
// clearing above, and the reason it is drawn at "the worker looked" rather than
// at "the worker did not run".
//
// Another import in this vault holds the import guard, so the
// worker is due, is asked, and refuses — without ever getting as far as finding
// out whether the thing it failed at last time would fail again. Clearing a
// streak on that would both report a failing import as healthy and drop its
// backoff, so a vault that is busy every pass would retry at the tick interval
// for as long as the failure lasts.
func TestARefusalThatOnlyProvesTheVaultIsBusyKeepsTheStreak(t *testing.T) {
	w := enroll(t)
	corruptImportRecord(t, w.root())

	first := w.once(w.root())
	if unit := first.unit(t, WorkerImport); unit.Error == "" {
		t.Fatalf("the import worker did not fail: %+v", unit)
	}
	// A vault that has applied nothing has no import record, so removing the
	// damaged one is the repair rather than a second plausible file.
	if err := os.Remove(vaultimport.StatePath(w.root())); err != nil {
		t.Fatalf("repair the import record: %v", err)
	}

	release, acquired, err := vault.TryLockPath(importGuardPath(w.root()))
	if err != nil || !acquired {
		t.Fatalf("take the import guard: acquired=%v err=%v", acquired, err)
	}
	defer release()

	w.clock.Advance(MinBackoff)
	pass := w.once(w.root())
	unit := pass.unit(t, WorkerImport)
	if unit.Ran || unit.Skipped == "" {
		t.Fatalf("a guarded vault did not refuse the import worker: %+v", unit)
	}
	worker := workerStatus(t, pass.Status, WorkerImport)
	if worker.Failures == 0 || worker.LastError == "" {
		t.Fatalf("a refusal the worker never looked past cleared the streak: %+v", worker)
	}
	if !pass.Status.Is(StateFailing) {
		t.Fatalf("a vault with a failing import reports %v", pass.Status.States)
	}
}

// TestOnlyTheRefusalsThatProveThereWasNothingToDoClearAStreak states the split
// as a membership, because the two sets are one `errors.Is` apart and the whole
// difference between them is which one ErrImportBusy is in.
func TestOnlyTheRefusalsThatProveThereWasNothingToDoClearAStreak(t *testing.T) {
	cases := []struct {
		name              string
		err               error
		skippable, vacant bool
	}{
		{"unenrolled", vaultsync.ErrNotEnrolled, true, true},
		{"unmanaged", vaultgit.ErrUnmanaged, true, true},
		{"unborn", vaultgit.ErrUnbornBranch, true, true},
		{"busy", vaultimport.ErrImportBusy, true, false},
		{"anything else", errors.New("the object store is unreadable"), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("vaultsyncd: %w", tc.err)
			if got := skippable(wrapped); got != tc.skippable {
				t.Fatalf("skippable(%v) = %v, want %v", tc.err, got, tc.skippable)
			}
			if got := vacant(wrapped); got != tc.vacant {
				t.Fatalf("vacant(%v) = %v, want %v", tc.err, got, tc.vacant)
			}
		})
	}
}

// corruptImportRecord replaces the consumer's durable record with something that
// is not one.
func corruptImportRecord(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(vaultimport.StatePath(root), []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt the import record: %v", err)
	}
}

// importGuardPath is internal/vaultimport's whole-import guard, named here
// because that package keeps its path private. Holding it is what another
// import in this vault, such as a web sync resolution, does.
func importGuardPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), "import.lock")))
}
