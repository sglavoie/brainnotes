package vaultsyncd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

func coverOutgoing(t *testing.T, root string) {
	t.Helper()
	packages, err := listPackages(vaultsync.OutgoingDir(root))
	if err != nil {
		t.Fatalf("list packages for backup coverage: %v", err)
	}
	coverage := vaultsync.BackupCoverage{
		Format:     vaultsync.BackupCoverageFormat,
		BackupID:   "test-verified-backup",
		VerifiedAt: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC),
	}
	for _, pkg := range packages {
		manifest, err := vaultsync.ReadManifest(pkg.dir)
		if err != nil {
			t.Fatalf("read package for backup coverage: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(pkg.dir, vaultsync.ManifestName))
		if err != nil {
			t.Fatalf("read manifest for backup coverage: %v", err)
		}
		coverage.Packages = append(coverage.Packages, vaultsync.BackupCoveredPackage{
			PackageID:      manifest.PackageID,
			ManifestSHA256: vaultsync.HashBytes(data),
		})
	}
	data, err := json.MarshalIndent(coverage, "", "  ")
	if err != nil {
		t.Fatalf("encode backup coverage: %v", err)
	}
	coveragePath := vaultsync.BackupCoveragePath(root)
	if err := os.MkdirAll(filepath.Dir(coveragePath), 0o755); err != nil {
		t.Fatalf("create backup coverage directory: %v", err)
	}
	if err := os.WriteFile(coveragePath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write backup coverage: %v", err)
	}
}

// world is the two-replica arrangement every test here needs: two Git-backed
// vaults, a real enrollment between them, and a controlled clock.
//
// Nothing is hand-written. The enrollment runs the flow a person would run, the
// packages are produced by internal/vaultexport and applied by
// internal/vaultimport, and the only thing this file fakes is the transport —
// which is the one component that is genuinely somebody else's program.
type world struct {
	t     *testing.T
	pair  *vaultfixture.Pair
	clock *vaultfixture.Clock
	local *vaultsync.Enrollment
	peer  *vaultsync.Enrollment
}

func enroll(t *testing.T) *world {
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
	return &world{t: t, pair: pair, clock: clock, local: local, peer: peer}
}

// unenrolled is a pair of ordinary vaults that synchronize with nothing, which
// is what almost every vault is.
func unenrolled(t *testing.T) *world {
	t.Helper()
	return &world{t: t, pair: vaultfixture.NewPair(t, nil), clock: vaultfixture.NewClock(t)}
}

func (w *world) root() string     { return w.pair.Local.Root }
func (w *world) peerRoot() string { return w.pair.Peer.Root }

func (w *world) vaultAt(root string) *vaultfixture.Vault {
	w.t.Helper()
	if root == w.pair.Peer.Root {
		return w.pair.Peer
	}
	return w.pair.Local
}

// options are the companion options every test runs with: the fixture clock, and
// no disconnection inference unless the test is about one.
func (w *world) options() Options {
	return Options{Now: w.clock.Now, DisconnectedAfter: -1}
}

// write puts content in a vault and commits it the way internal/vaultgit's scope
// policy would have staged it, which is the history the exporter reads.
//
// `.bnotes/sync` is excluded for internal/vaultexport's fixture reason: the
// enrollment record and the export cursor are never in the content history, and
// committing them would be asserting against a vault this codebase does not
// produce.
func (w *world) write(root, rel, body string) {
	w.t.Helper()
	v := w.vaultAt(root)
	v.Write(w.t, rel, body)
	v.Git(w.t, "add", "-A", "--", ".", ":(exclude).bnotes/sync")
	v.Git(w.t, "commit", "-q", "-m", "edit "+rel)
}

// once runs one pass against a vault and fails the test if the pass could not be
// run at all. A pass in which a worker failed is a legitimate outcome and is
// returned rather than fatal: several tests here are about exactly that.
func (w *world) once(root string) *Pass {
	w.t.Helper()
	return w.onceWith(root, w.options())
}

func (w *world) onceWith(root string, opts Options) *Pass {
	w.t.Helper()
	pass, err := Once(context.Background(), root, opts)
	if err != nil {
		w.t.Fatalf("Once(%s): %v", filepath.Base(root), err)
	}
	if pass == nil {
		w.t.Fatalf("Once(%s) returned no pass", filepath.Base(root))
	}
	return pass
}

// status reads a vault without running anything.
func (w *world) status(root string) *Status {
	w.t.Helper()
	status, err := Read(context.Background(), root, w.options().readOptions())
	if err != nil {
		w.t.Fatalf("Read(%s): %v", filepath.Base(root), err)
	}
	return status
}

// transport is Syncthing, done synchronously and in one direction: everything in
// src's outgoing channel appears in dst's incoming channel, and everything the
// sender has removed from its outgoing channel disappears from the receiver's
// incoming one.
//
// The mirroring of removals is the half that matters. It is how the receiver
// learns that a package was retired, which is what lets it release the receipt it
// published for that package — and a fixture that only ever copied forward would
// make that half of retirement untestable.
func (w *world) transport(src, dst string) {
	w.t.Helper()
	from, to := vaultsync.OutgoingDir(src), vaultsync.IncomingDir(dst)
	w.mirror(from, to)
	w.mirror(vaultsync.ReceiptsDir(from), vaultsync.ReceiptsDir(to))
}

// mirror makes to hold exactly what from holds, one level deep.
func (w *world) mirror(from, to string) {
	w.t.Helper()
	wanted := map[string]bool{}
	for _, entry := range w.entries(from) {
		// Receipts live in a subdirectory of the channel and are mirrored by their
		// own call, so the channel copy skips them rather than nesting.
		if entry.IsDir() && !vaultsync.PackageName(entry.Name()) {
			continue
		}
		wanted[entry.Name()] = true
		copyTree(w.t, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()))
	}
	for _, entry := range w.entries(to) {
		if wanted[entry.Name()] || (entry.IsDir() && !vaultsync.PackageName(entry.Name())) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(to, entry.Name())); err != nil {
			w.t.Fatalf("mirror removal: %v", err)
		}
	}
}

func (w *world) entries(dir string) []os.DirEntry {
	w.t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		w.t.Fatalf("read %q: %v", dir, err)
	}
	return entries
}

// copyTree copies a file or directory tree, leaving anything already at the
// destination alone: a published package is immutable, and a transport that
// rewrote one would be testing something no transport does.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	info, err := os.Stat(from)
	if err != nil {
		t.Fatalf("stat %q: %v", from, err)
	}
	if !info.IsDir() {
		copyFile(t, from, to, info.Mode())
		return
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", to, err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatalf("read %q: %v", from, err)
	}
	for _, entry := range entries {
		copyTree(t, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()))
	}
}

func copyFile(t *testing.T, from, to string, mode os.FileMode) {
	t.Helper()
	if _, err := os.Stat(to); err == nil {
		return
	}
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %q: %v", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", to, err)
	}
	if err := os.WriteFile(to, data, mode.Perm()); err != nil {
		t.Fatalf("write %q: %v", to, err)
	}
}

// channel lists the package identities in one channel directory, sorted.
func (w *world) channel(dir string) []string {
	w.t.Helper()
	packages, err := listPackages(dir)
	if err != nil {
		w.t.Fatalf("listPackages %q: %v", dir, err)
	}
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.name)
	}
	return names
}

// receipts lists the package identities a channel holds receipts for, sorted.
func (w *world) receipts(dir string) []string {
	w.t.Helper()
	published, err := listReceipts(dir)
	if err != nil {
		w.t.Fatalf("listReceipts %q: %v", dir, err)
	}
	names := make([]string, 0, len(published))
	for _, receipt := range published {
		names = append(names, receipt.packageID)
	}
	sort.Strings(names)
	return names
}

// unit returns one worker's result from a pass.
func (p *Pass) unit(t *testing.T, name WorkerName) UnitResult {
	t.Helper()
	for _, unit := range p.Units {
		if unit.Worker == name {
			return unit
		}
	}
	t.Fatalf("the pass has no %s unit: %+v", name, p.Units)
	return UnitResult{}
}

// imported, exported and retire return one worker's outcome, failing the test
// when that worker did not produce one — which is what a skip or an early
// failure looks like, and is never what a caller of these wanted.
func (p *Pass) imported(t *testing.T) *ImportOutcome {
	t.Helper()
	unit := p.unit(t, WorkerImport)
	if unit.Import == nil {
		t.Fatalf("the import worker produced nothing: %+v", unit)
	}
	return unit.Import
}

func (p *Pass) exported(t *testing.T) *ExportOutcome {
	t.Helper()
	unit := p.unit(t, WorkerExport)
	if unit.Export == nil {
		t.Fatalf("the export worker produced nothing: %+v", unit)
	}
	return unit.Export
}

func (p *Pass) retired(t *testing.T) *RetireOutcome {
	t.Helper()
	unit := p.unit(t, WorkerRetire)
	if unit.Retire == nil {
		t.Fatalf("the retire worker produced nothing: %+v", unit)
	}
	return unit.Retire
}

// requireStates fails unless exactly these states hold, in stateOrder.
func requireStates(t *testing.T, status *Status, want ...State) {
	t.Helper()
	if len(status.States) != len(want) {
		t.Fatalf("states: got %v, want %v", status.States, want)
	}
	for i := range want {
		if status.States[i] != want[i] {
			t.Fatalf("states: got %v, want %v", status.States, want)
		}
	}
}

// kinds returns the event kinds observed, in order, which is how a test asserts
// what a companion said rather than only what it did.
func kinds(events []Event) []EventKind {
	observed := make([]EventKind, 0, len(events))
	for _, event := range events {
		observed = append(observed, event.Kind)
	}
	return observed
}

func contains[T comparable](haystack []T, needle T) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
