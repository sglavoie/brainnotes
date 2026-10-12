package checkpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestManagedVaultRecordLivesInGitPrivateStorage(t *testing.T) {
	root := newBarrierVault(t)
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	path, err := RecordPath(root)
	if err != nil {
		t.Fatalf("RecordPath: %v", err)
	}
	want := filepath.Join(root, ".git", "brainnotes-safety", "checkpoint-locks", "checkpoint.json")
	if path != want {
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
