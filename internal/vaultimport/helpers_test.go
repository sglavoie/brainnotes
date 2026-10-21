package vaultimport

import (
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// journalPrefix is where the undo manifests live. A snapshot carries them for
// any journaled operation — they are what authorizes committing a deletion at
// all — so a commit-content assertion has to allow them and nothing else.
const journalPrefix = config.DefaultJournalDir + "/"

// replicas is the two-device world every test here needs: a real enrollment, a
// controlled clock, and two independent Git-backed vaults seeded identically.
//
// Nothing is hand-written. The enrollment goes through the flow a person would
// run and every package goes through vaultsync.Builder and Publish, so a test
// that passes is evidence about the code under test rather than about a fixture
// that agreed with it.
type replicas struct {
	pair  *vaultfixture.Pair
	clock *vaultfixture.Clock
	local *vaultsync.Enrollment
	peer  *vaultsync.Enrollment
	// sequence is the peer's export cursor, advanced per package so two packages
	// in one test never claim one position in the producer's stream.
	sequence uint64
}

func enroll(t *testing.T) *replicas {
	t.Helper()
	pair := vaultfixture.NewPair(t, nil)
	clock := vaultfixture.NewClock(t)

	local, err := vaultsync.Establish(pair.Local.Root, vaultsync.Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	clock.Tick()
	peer, err := vaultsync.Join(pair.Peer.Root, local.Invite(), vaultsync.Options{Label: "pi", Now: clock.Now})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	clock.Tick()
	local, err = vaultsync.RecordPeer(pair.Local.Root, vaultsync.Peer{DeviceID: peer.DeviceID, Label: peer.Label})
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	return &replicas{pair: pair, clock: clock, local: local, peer: peer}
}

// root is the vault every import in these tests is applied to.
func (r *replicas) root() string { return r.pair.Local.Root }

// deliver builds one package on the peer and publishes it into the local
// vault's incoming channel, which is where the transport would have left it.
//
// Into the real channel rather than a scratch directory on purpose: the channel
// lives under `.bnotes/sync/`, which internal/vaultgit excludes from the content
// history, so every test here also asserts — by never seeing it in a commit or a
// status line — that a delivered package cannot dirty the vault it is about.
func (r *replicas) deliver(t *testing.T, build func(*vaultsync.Builder)) (*vaultsync.Manifest, string) {
	t.Helper()
	builder := r.build(t)
	builder.BeginOperation("", r.clock.Now())
	build(builder)
	return r.publish(t, builder)
}

// build starts a package at the peer's next sequence position.
func (r *replicas) build(t *testing.T) *vaultsync.Builder {
	t.Helper()
	r.sequence++
	builder, err := vaultsync.NewBuilder(r.peer, r.sequence, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return builder
}

func (r *replicas) publish(t *testing.T, builder *vaultsync.Builder) (*vaultsync.Manifest, string) {
	t.Helper()
	manifest, dir, err := builder.PublishTo(vaultsync.IncomingDir(r.root()))
	if err != nil {
		t.Fatalf("PublishTo: %v", err)
	}
	return manifest, dir
}

// apply runs the code under test with the fixture clock wired in.
func (r *replicas) apply(t *testing.T, packageDir string) (*Result, error) {
	t.Helper()
	return Apply(r.root(), packageDir, Options{Command: "bn sync apply --test", Now: r.clock.Now})
}

// mustApply fails the test unless the package applies.
func (r *replicas) mustApply(t *testing.T, packageDir string) *Result {
	t.Helper()
	result, err := r.apply(t, packageDir)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return result
}

// planned drives the applier's first three steps and stops with the transaction
// staged in the caller's hands.
//
// Several properties here are only observable between two of Apply's steps — the
// configuration pin fires only if the configuration changes after planning — so
// those tests drive the steps rather than calling Apply.
func planned(t *testing.T, r *replicas, packageDir string) (*applier, *vault.FileTxn) {
	t.Helper()
	session, err := accept(r.root(), packageDir, Options{Now: r.clock.Now})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := session.read(packageDir); err != nil {
		t.Fatalf("read: %v", err)
	}
	txn, err := session.plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return session, txn
}

// begun opens the operation lifecycle a planned transaction commits through,
// with the same token and command Apply would have used.
func begun(t *testing.T, r *replicas, session *applier) *noteops.Journal {
	t.Helper()
	operation, err := noteops.BeginOperation(r.root(), operationToken, "bn sync apply --test", session.targets())
	if err != nil {
		t.Fatalf("BeginOperation: %v", err)
	}
	return operation
}

// believes is the expectation the producer carries for a path both replicas
// still agree on. It is read from the *peer*, because that is the device whose
// belief a package states.
func (r *replicas) believes(t *testing.T, rel string) vaultsync.Expectation {
	t.Helper()
	return vaultsync.Existing(vaultsync.HashBytes([]byte(r.pair.Peer.Read(t, rel))))
}

// entries lists the undo entries the local vault holds, which is how a test
// asserts that a refused import created none.
func (r *replicas) entries(t *testing.T) []journal.Entry {
	t.Helper()
	entries, _, err := journal.ListEntries(noteops.JournalRoot(r.root()))
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	return entries
}

// imported returns the export ledger's record of applied peer operations. It is
// the anti-echo half of an import: an entry here is what stops the exporter
// sending the peer its own change back.
func (r *replicas) imported(t *testing.T) map[string]vaultexport.Import {
	t.Helper()
	state, err := vaultexport.LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state.Imported
}

// commitPaths returns the vault-relative paths one commit changed, sorted.
//
// --no-renames throughout: a journaled deletion commits the removal alongside a
// snapshot of the identical bytes, which Git's similarity detection is delighted
// to call a 100% rename of the note into the journal. That reads as one path
// where two changed, and it would hide the deletion this package must commit.
func commitPaths(t *testing.T, v *vaultfixture.Vault, rev string) []string {
	t.Helper()
	out := v.Git(t, "show", "--no-renames", "--name-only", "--pretty=format:", rev)
	return splitLines(out)
}

// commitStatus returns the `status\tpath` lines one commit carries, so a test
// can assert that a tombstone was committed as a deletion rather than as some
// other change to the same path.
func commitStatus(t *testing.T, v *vaultfixture.Vault, rev string) []string {
	t.Helper()
	out := v.Git(t, "show", "--no-renames", "--name-status", "--pretty=format:", rev)
	return splitLines(out)
}

// dirtyInScope returns the working-tree changes that are the vault's own
// content history — which is the only sense in which an operation can be said to
// have left something behind.
//
// A raw `git status` in an enrolled vault always reports `.bnotes/locks/` and
// `.bnotes/sync/` as untracked, and always will: internal/vaultgit excludes
// `.bnotes` wholesale bar the journal, so a delivered package and a held lock
// are outside the history by design. Asking InScope rather than listing those
// two names keeps this agreeing with the policy instead of with today's
// directory layout.
func dirtyInScope(t *testing.T, v *vaultfixture.Vault) []string {
	t.Helper()
	kept := make([]string, 0)
	for _, line := range v.Dirty(t) {
		if len(line) < 4 {
			t.Fatalf("unparsable status line %q", line)
		}
		if vaultgit.InScope(line[3:]) {
			kept = append(kept, line)
		}
	}
	return kept
}

func splitLines(out string) []string {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	sort.Strings(lines)
	return lines
}

// contentPaths drops the undo manifests a snapshot legitimately carries, leaving
// the note paths a commit-content assertion is actually about.
func contentPaths(paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if !strings.HasPrefix(path, journalPrefix) {
			kept = append(kept, path)
		}
	}
	return kept
}

// commitCount is how many commits the branch holds, which is how a test asserts
// that one package produced exactly one commit in this vault's own history.
func commitCount(t *testing.T, v *vaultfixture.Vault) int {
	t.Helper()
	out := v.Git(t, "rev-list", "--count", "HEAD")
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("parse commit count %q: %v", out, err)
	}
	return count
}

// exists reports whether a vault-relative path is there at all, symlinks
// included.
func exists(t *testing.T, v *vaultfixture.Vault, rel string) bool {
	t.Helper()
	_, err := os.Lstat(v.Path(rel))
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("lstat %q: %v", rel, err)
	return false
}

// requireIs fails unless err is the expected sentinel. Every refusal in this
// package is identified by its sentinel rather than by its message, so the
// messages stay free to improve.
func requireIs(t *testing.T, err, want error, context string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected %v, got no error", context, want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s: expected %v, got %v", context, want, err)
	}
}

func requireEqual(t *testing.T, got, want []string, context string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", context, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", context, got, want)
		}
	}
}
