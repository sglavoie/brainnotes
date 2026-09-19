package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortWait is the budget a test gives a writer it expects to be refused. It is
// long enough to survive a loaded machine's scheduling and short enough that the
// suite does not pay DefaultWriterWait for every refusal.
const shortWait = 200 * time.Millisecond

// newBarrierVault returns a disposable vault root with nothing in it.
//
// The barrier needs no vault content and deliberately creates none: resolve only
// requires the root to exist, and a test that had to build a Git repository to
// ask whether a lock excludes would be testing the fixture.
func newBarrierVault(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp root: %v", err)
	}
	return root
}

// mustJoin joins within shortWait and fails the test if the barrier refused.
func mustJoin(t *testing.T, root string) func() {
	t.Helper()
	release, err := JoinWithin(root, shortWait)
	if err != nil {
		t.Fatalf("JoinWithin(%q): %v", root, err)
	}
	return release
}

// mustHold takes a checkpoint with a short drain and fails if it could not.
func mustHold(t *testing.T, root, reason string) *Held {
	t.Helper()
	held, err := Hold(root, Options{Reason: reason, Drain: shortWait})
	if err != nil {
		t.Fatalf("Hold(%q): %v", root, err)
	}
	return held
}

// mustStatus reads the vault's checkpoint state.
func mustStatus(t *testing.T, root string) State {
	t.Helper()
	state, err := Status(root)
	if err != nil {
		t.Fatalf("Status(%q): %v", root, err)
	}
	return state
}

// recordExists reports whether the vault carries an announcement at all, which
// is the fact every "it cleaned up after itself" assertion is about.
func recordExists(t *testing.T, root string) bool {
	t.Helper()
	path, err := RecordPath(root)
	if err != nil {
		t.Fatalf("RecordPath(%q): %v", root, err)
	}
	_, err = os.Lstat(path)
	switch {
	case err == nil:
		return true
	case os.IsNotExist(err):
		return false
	default:
		t.Fatalf("stat checkpoint record: %v", err)
		return false
	}
}

// A join is counted rather than nested, so the shared flock is taken once per
// process per vault however many writers this process is running. The count is
// what bn serve depends on: two concurrent request handlers writing to one vault
// must not deadlock the first checkpoint that arrives.
func TestJoinIsCountedPerVaultRoot(t *testing.T) {
	root := newBarrierVault(t)

	outer := mustJoin(t, root)
	if !Joined(root) {
		t.Fatal("Joined reports false inside a join")
	}
	inner := mustJoin(t, root)

	inner()
	if !Joined(root) {
		t.Fatal("releasing the inner join ended the outer one")
	}
	// Idempotent: a caller may defer the release and also call it early.
	inner()
	if !Joined(root) {
		t.Fatal("a second inner release decremented the count again")
	}

	outer()
	if Joined(root) {
		t.Fatal("Joined still reports true after the last release")
	}
	// The flock is genuinely gone, not merely the count: a checkpoint can be
	// taken, which is the only thing that proves it.
	held := mustHold(t, root, "after the join drained")
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// Two vaults are two barriers. A process writing to one must not be refused a
// checkpoint on the other, which is what keeps the per-root registry from
// becoming a process-wide lock.
func TestJoinsInDifferentVaultsAreIndependent(t *testing.T) {
	first := newBarrierVault(t)
	second := newBarrierVault(t)

	release := mustJoin(t, first)
	defer release()

	if Joined(second) {
		t.Fatal("joining one vault reported the other as joined")
	}
	held := mustHold(t, second, "the other vault")
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// assertHeldStatus checks that the vault's own account of itself names the
// checkpoint holding it. That account is the only thing a person looking at a
// stalled command has to go on, so it is asserted as carefully as the refusal.
func assertHeldStatus(t *testing.T, root string, held *Held, reason string) {
	t.Helper()
	state := mustStatus(t, root)
	if !state.Announced || !state.Live || !state.Blocking {
		t.Fatalf("status while held = %+v, want announced, live and blocking", state)
	}
	if state.ID != held.ID() || state.Phase != PhaseHeld {
		t.Fatalf("status while held = %+v, want id %q and phase %q", state, held.ID(), PhaseHeld)
	}
	if state.Reason != reason {
		t.Fatalf("status reason = %q, want the reason Hold was given", state.Reason)
	}
}

// While a checkpoint is held no new writer may start, and the refusal is the
// retryable one: nothing was locked, nothing was written, and the caller is told
// to come back.
func TestHoldRefusesNewWritersUntilRelease(t *testing.T) {
	root := newBarrierVault(t)
	held := mustHold(t, root, "copying the vault")

	release, err := JoinWithin(root, shortWait)
	if !errors.Is(err, ErrBarrierHeld) {
		t.Fatalf("JoinWithin during a checkpoint = %v, want ErrBarrierHeld", err)
	}
	// The failed join still hands back a callable release, so a caller that
	// deferred it before checking the error does not panic.
	release()

	assertHeldStatus(t, root, held, "copying the vault")

	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustJoin(t, root)()

	if recordExists(t, root) {
		t.Fatal("the announcement outlived the checkpoint that wrote it")
	}
	if state := mustStatus(t, root); state.Announced || state.Blocking {
		t.Fatalf("status after release = %+v, want an empty state", state)
	}
}

// A vault can be quiet for one checkpoint at a time, so a second client is
// refused rather than queued — and refused without disturbing the first, whose
// announcement a queued client would eventually overwrite.
func TestHoldRefusesASecondCheckpoint(t *testing.T) {
	root := newBarrierVault(t)
	first := mustHold(t, root, "first")
	defer first.Release()

	second, err := Hold(root, Options{Reason: "second", Drain: shortWait})
	if !errors.Is(err, ErrCheckpointActive) {
		t.Fatalf("second Hold = (%v, %v), want ErrCheckpointActive", second, err)
	}
	if state := mustStatus(t, root); state.ID != first.ID() || state.Reason != "first" {
		t.Fatalf("status after a refused second checkpoint = %+v, want the first checkpoint's", state)
	}
}

// A checkpoint taken from inside this process's own write would wait out its
// drain budget against its own shared hold and then report a timeout that names
// nobody. It is refused immediately instead, and named for what it is.
func TestHoldRefusesFromInsideAJoin(t *testing.T) {
	root := newBarrierVault(t)
	release := mustJoin(t, root)
	defer release()

	held, err := Hold(root, Options{Reason: "nested", Drain: shortWait})
	if !errors.Is(err, ErrNested) {
		t.Fatalf("Hold inside a join = (%v, %v), want ErrNested", held, err)
	}
	if recordExists(t, root) {
		t.Fatal("a refused nested Hold announced itself")
	}
}

// Release is idempotent, so a client may defer it and also call it explicitly
// before it does something slow.
func TestReleaseIsIdempotent(t *testing.T) {
	root := newBarrierVault(t)
	held := mustHold(t, root, "twice released")

	if err := held.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if held.Root() != root {
		t.Fatalf("Root() = %q, want %q", held.Root(), root)
	}
	if held.Deadline().IsZero() {
		t.Fatal("Deadline() is zero; a checkpoint records the lease it announced")
	}
	mustJoin(t, root)()
}

// A vault nobody is checkpointing answers the status query without creating
// anything. A status command run against somebody else's vault must not be the
// reason that vault acquires a .bnotes directory.
func TestStatusOnAQuietVaultCreatesNothing(t *testing.T) {
	root := newBarrierVault(t)

	if state := mustStatus(t, root); state.Announced || state.Live || state.Blocking {
		t.Fatalf("status on a quiet vault = %+v, want an empty state", state)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read vault root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Status created %v in a vault it was only inspecting", entries)
	}
}

// writeDeadAnnouncement leaves the record a client that died would have left:
// durable, plausible, and with nothing holding the claim lock.
func writeDeadAnnouncement(t *testing.T, root string, phase Phase) {
	t.Helper()
	paths, err := resolve(root)
	if err != nil {
		t.Fatalf("resolve(%q): %v", root, err)
	}
	at := time.Now().Add(-time.Minute)
	rec := &record{
		Version:     recordVersion,
		ID:          "ckpt_deadclient",
		Phase:       phase,
		Reason:      "a client that is no longer running",
		PID:         os.Getpid(),
		Host:        "somewhere",
		AnnouncedAt: at,
		Deadline:    at.Add(DefaultLease),
	}
	if err := writeRecord(paths.record, rec); err != nil {
		t.Fatalf("write the dead client's record: %v", err)
	}
}

// The record alone cannot say whether the client that wrote it still exists, and
// a vault that refused every write until somebody deleted a file by hand would
// be a worse failure than the one the barrier prevents. A free claim lock is the
// kernel's answer, and writers act on it.
func TestADeadClientsAnnouncementDoesNotBlockWriters(t *testing.T) {
	for _, phase := range []Phase{PhaseDraining, PhaseHeld} {
		t.Run(string(phase), func(t *testing.T) {
			root := newBarrierVault(t)
			writeDeadAnnouncement(t, root, phase)

			state := mustStatus(t, root)
			if !state.Announced {
				t.Fatal("status does not see the record on disk")
			}
			if state.Live || state.Blocking {
				t.Fatalf("status = %+v, want announced but neither live nor blocking", state)
			}

			// Not merely permitted — permitted without waiting. A writer that
			// paid the full budget first would make every command in a vault with
			// stale litter pause for no reason.
			start := time.Now()
			mustJoin(t, root)()
			if waited := time.Since(start); waited > shortWait {
				t.Fatalf("a writer waited %s on a dead client's announcement", waited)
			}
		})
	}
}

// A writer ignores a dead client's record but never removes it: a writer that
// deleted an announcement could delete a fresh one a new client had just
// written. The next Hold owns the claim lock and is therefore the one authority
// that cannot race, so it is the one that clears it.
func TestHoldReplacesADeadClientsAnnouncement(t *testing.T) {
	root := newBarrierVault(t)
	writeDeadAnnouncement(t, root, PhaseHeld)

	held := mustHold(t, root, "the live client")
	defer held.Release()

	state := mustStatus(t, root)
	if state.ID == "ckpt_deadclient" {
		t.Fatal("Hold adopted the dead client's identity instead of minting its own")
	}
	if state.ID != held.ID() || !state.Blocking || state.Reason != "the live client" {
		t.Fatalf("status after replacing a stale record = %+v, want the live checkpoint's", state)
	}
}

// writeUndecidableIntent leaves the one thing internal/vault's recovery refuses
// to decide: a transaction record written by a build this one does not
// understand. Everything else it finds it either rolls forward or discards.
func writeUndecidableIntent(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".bnotes", "txn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create intent directory: %v", err)
	}
	body := []byte(`{"version":999999}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "undecidable.intent"), body, 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}
}

// Hold drives internal/vault's roll-forward pass before it says the vault is
// quiet, and reports what that pass could not decide rather than swallowing it.
// A vault holding an undecided half-applied transaction refuses further writes,
// and a copy of it would be a copy of that refusal.
//
// The failure must also leave nothing behind. A checkpoint that announced,
// failed to settle, and left its announcement on disk would block every writer
// in the vault for as long as the process lived.
func TestHoldReportsATransactionItCannotSettle(t *testing.T) {
	root := newBarrierVault(t)
	writeUndecidableIntent(t, root)

	held, err := Hold(root, Options{Reason: "unsettleable", Drain: shortWait})
	if err == nil {
		_ = held.Release()
		t.Fatal("Hold succeeded on a vault holding an undecided transaction")
	}
	if !strings.Contains(err.Error(), "settle") {
		t.Fatalf("Hold error = %v, want it to name the settle pass", err)
	}
	if recordExists(t, root) {
		t.Fatal("a Hold that could not settle left its announcement behind")
	}
	if state := mustStatus(t, root); state.Announced || state.Blocking {
		t.Fatalf("status after a failed Hold = %+v, want an empty state", state)
	}
	// The vault is no worse than it was: the barrier is free, so a writer is
	// refused by its own transaction recovery rather than by a checkpoint that
	// is not there.
	mustJoin(t, root)()
}

// The record describes one machine's momentary intent, so it lives where neither
// the content history nor synchronization will pick it up. Both exclusions are
// by construction rather than by an allowlist entry, and this is what says so.
func TestTheRecordLivesOutsideWhatIsCommittedOrSent(t *testing.T) {
	if !strings.HasPrefix(RecordRel, ".bnotes/") {
		t.Fatalf("RecordRel = %q, want it under .bnotes/", RecordRel)
	}
	root := newBarrierVault(t)
	path, err := RecordPath(root)
	if err != nil {
		t.Fatalf("RecordPath: %v", err)
	}
	if want := filepath.Join(root, filepath.FromSlash(RecordRel)); path != want {
		t.Fatalf("RecordPath = %q, want %q", path, want)
	}
}

// A vault root that does not exist, or no vault root at all, is refused with a
// sentence rather than by creating one.
func TestAnUnresolvableRootIsRefused(t *testing.T) {
	missing := filepath.Join(newBarrierVault(t), "not-a-vault")

	if _, err := JoinWithin("", shortWait); err == nil {
		t.Fatal("JoinWithin accepted an empty vault root")
	}
	if _, err := Hold(missing, Options{Drain: shortWait}); err == nil {
		t.Fatal("Hold accepted a vault root that does not exist")
	}
	if _, err := Status(missing); err == nil {
		t.Fatal("Status accepted a vault root that does not exist")
	}
	if Joined(missing) {
		t.Fatal("Joined reported true for a vault root that does not exist")
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("a refused call created %q", missing)
	}
}

// The zero Options is the documented default, and so is any non-positive
// duration — a caller passing a flag it never set must get the package's budget
// rather than a zero one that would refuse instantly.
func TestNonPositiveDurationsMeanTheDefaults(t *testing.T) {
	for _, opts := range []Options{{}, {Drain: -time.Second, Lease: -time.Second}} {
		if got := opts.drain(); got != DefaultDrain {
			t.Fatalf("Options%+v.drain() = %s, want %s", opts, got, DefaultDrain)
		}
		if got := opts.lease(); got != DefaultLease {
			t.Fatalf("Options%+v.lease() = %s, want %s", opts, got, DefaultLease)
		}
	}
}

// The recorded lease is advisory and exists so a person can tell a slow client
// from a stuck one. It is anchored to the clock the caller supplied, which is
// what makes it assertable at all.
func TestTheAnnouncementRecordsTheLeaseItWasGiven(t *testing.T) {
	root := newBarrierVault(t)
	at := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)

	held, err := Hold(root, Options{
		Reason: "leased",
		Drain:  shortWait,
		Lease:  90 * time.Second,
		Now:    func() time.Time { return at },
	})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	defer held.Release()

	want := at.Add(90 * time.Second)
	if !held.Deadline().Equal(want) {
		t.Fatalf("Deadline() = %s, want %s", held.Deadline(), want)
	}
	state := mustStatus(t, root)
	if !state.AnnouncedAt.Equal(at) || !state.Deadline.Equal(want) {
		t.Fatalf("status times = (%s, %s), want (%s, %s)", state.AnnouncedAt, state.Deadline, at, want)
	}
}
