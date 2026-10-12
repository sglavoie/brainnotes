package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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

// A context identifies true logical nesting, so an inner durable writer can use
// the outer admission without trying to take the barrier again.
func TestJoinContextIsNestedPerVaultRoot(t *testing.T) {
	root := newBarrierVault(t)

	ctx, outer, err := JoinContextWithin(context.Background(), root, shortWait)
	if err != nil {
		t.Fatalf("JoinContextWithin(%q): %v", root, err)
	}
	if !Joined(root) {
		t.Fatal("Joined reports false inside a join")
	}
	_, inner, err := JoinContextWithin(ctx, root, shortWait)
	if err != nil {
		t.Fatalf("nested JoinContextWithin(%q): %v", root, err)
	}

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

func TestReleasedContextCannotBypassLaterCheckpoint(t *testing.T) {
	root := newBarrierVault(t)
	ctx, release, err := JoinContextWithin(context.Background(), root, shortWait)
	if err != nil {
		t.Fatalf("JoinContextWithin: %v", err)
	}
	release()
	held := mustHold(t, root, "after context release")
	defer held.Release()
	_, nested, err := JoinContextWithin(ctx, root, shortWait)
	if !errors.Is(err, ErrBarrierHeld) {
		t.Fatalf("JoinContextWithin with released context = %v, want ErrBarrierHeld", err)
	}
	nested()
}

// A fresh logical writer in this process is not nesting merely because another
// goroutine owns the shared flock. Once a checkpoint announces, fresh work must
// wait while explicitly nested work may finish so the older writer can drain.
func TestAnnouncementStopsFreshInProcessWriterButNotNestedWork(t *testing.T) {
	root := newBarrierVault(t)
	ctx, outer, err := JoinContextWithin(context.Background(), root, shortWait)
	if err != nil {
		t.Fatalf("JoinContextWithin: %v", err)
	}
	defer outer()

	paths, err := resolve(root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	claim, acquired, err := vault.TryLockPathHandle(paths.claim)
	if err != nil || !acquired {
		t.Fatalf("claim checkpoint: acquired=%v err=%v", acquired, err)
	}
	defer claim.Release()
	rec, err := intent(Options{Reason: "wait for older writer"})
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	if err := writeRecord(paths.record, rec); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	defer clearRecord(paths.record)

	_, nested, err := JoinContextWithin(ctx, root, shortWait)
	if err != nil {
		t.Fatalf("nested writer under announced checkpoint: %v", err)
	}
	nested()

	fresh, err := JoinWithin(root, shortWait)
	if !errors.Is(err, ErrBarrierHeld) {
		t.Fatalf("fresh in-process writer = %v, want ErrBarrierHeld", err)
	}
	fresh()

	outer()
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
