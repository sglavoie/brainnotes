package vaultexport

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestBaselineDescribesThePortableTree covers the first export a vault ever
// makes: the whole committed tree, minus everything the portable policy
// excludes, as one package whose every change expects the path to be absent.
func TestBaselineDescribesThePortableTree(t *testing.T) {
	f := newFixture(t)

	result := f.mustExport(Options{})

	if !result.Baseline {
		t.Fatal("the first export must report itself as a baseline")
	}
	if len(result.Packages) != 1 {
		t.Fatalf("packages: got %d, want 1", len(result.Packages))
	}
	requirePaths(t, f.paths(result), sortedSeed())
	if result.Cursor != f.vault.Head(t) {
		t.Fatalf("cursor: got %q, want the tip %q", result.Cursor, f.vault.Head(t))
	}
	if result.PendingPaths != 0 {
		t.Fatalf("pending paths: got %d, want 0", result.PendingPaths)
	}
	for _, change := range f.manifest(result.Packages[0].Dir).Changes() {
		if change.ExpectExists {
			t.Errorf("%s: a baseline change must expect the path absent", change.Path)
		}
		if len(change.Predecessors) != 0 {
			t.Errorf("%s: a baseline change must name no predecessor, got %v", change.Path, change.Predecessors)
		}
	}
	if payload := f.payload(result, "Alpha.md"); payload != f.vault.Read(t, "Alpha.md") {
		t.Fatalf("Alpha.md payload does not match the committed bytes:\n%q", payload)
	}
}

// TestExportIsRepeatableAcrossRestart is the session's first completion
// criterion. Each call is a fresh process as far as this package is concerned —
// nothing is cached between them — so a second call that published anything
// would be a second call that told the peer something twice.
func TestExportIsRepeatableAcrossRestart(t *testing.T) {
	f := newFixture(t)

	first := f.mustExport(Options{})
	if len(first.Packages) != 1 {
		t.Fatalf("baseline packages: got %d, want 1", len(first.Packages))
	}

	second := f.mustExport(Options{})
	if len(second.Packages) != 0 {
		t.Fatalf("a repeated export published %d packages", len(second.Packages))
	}
	if second.Cursor != first.Cursor {
		t.Fatalf("cursor moved on a no-op export: %q -> %q", first.Cursor, second.Cursor)
	}

	f.write("Alpha.md", "---\nstatus: draft\ntitle: Alpha\n---\n\nEdited.\n")
	head := f.commit("edit Alpha")

	third := f.mustExport(Options{})
	requirePaths(t, f.paths(third), []string{"Alpha.md"})
	if third.Cursor != head {
		t.Fatalf("cursor: got %q, want %q", third.Cursor, head)
	}
	if got := f.payload(third, "Alpha.md"); !strings.Contains(got, "Edited.") {
		t.Fatalf("Alpha.md payload: got %q", got)
	}

	fourth := f.mustExport(Options{})
	if len(fourth.Packages) != 0 {
		t.Fatalf("the same commit was exported twice (%d packages)", len(fourth.Packages))
	}
	// Alpha.md twice across the whole channel — once in the baseline, once as the
	// edit — and nothing else twice.
	requirePaths(t, f.channelPaths(), []string{
		"Alpha.md", "Alpha.md", "Beta.md", "Notes/Gamma.md", vaultfixture.UnrelatedNote, "brainnotes.toml",
	})
}

// TestUncommittedEditsRemainPending is the second completion criterion. The
// exporter reads committed objects only, so a note being typed cannot travel —
// and the bytes that do travel are the committed ones even while the working
// tree holds something else.
func TestUncommittedEditsRemainPending(t *testing.T) {
	f := newFixture(t)
	committed := f.vault.Read(t, "Alpha.md")

	f.write("Alpha.md", "half-typed\n")
	f.write("Draft.md", "a note that was never saved to history\n")

	baseline := f.mustExport(Options{})
	requirePaths(t, f.paths(baseline), sortedSeed())
	if got := f.payload(baseline, "Alpha.md"); got != committed {
		t.Fatalf("the uncommitted edit travelled: got %q, want %q", got, committed)
	}

	if next := f.mustExport(Options{}); len(next.Packages) != 0 {
		t.Fatalf("an uncommitted edit produced %d packages", len(next.Packages))
	}

	head := f.commit("save the edits")
	result := f.mustExport(Options{})
	requirePaths(t, f.paths(result), []string{"Alpha.md", "Draft.md"})
	if result.Cursor != head {
		t.Fatalf("cursor: got %q, want %q", result.Cursor, head)
	}
	if got := f.payload(result, "Alpha.md"); got != "half-typed\n" {
		t.Fatalf("Alpha.md payload after the commit: got %q", got)
	}
	// The prior expectation is the parent's bytes, which is what lets the peer
	// detect a conflict rather than silently overwrite its own copy.
	change := f.changeFor(result, "Alpha.md")
	if !change.ExpectExists || change.ExpectSHA256 != vaultsync.HashBytes([]byte(committed)) {
		t.Fatalf("Alpha.md expectation: exists=%v sha=%q", change.ExpectExists, change.ExpectSHA256)
	}
	if got := f.changeFor(result, "Draft.md"); got.ExpectExists {
		t.Fatal("a newly created note must expect the path absent")
	}
}

// TestImportedOperationsDoNotEcho is the third completion criterion. Applying a
// peer's operation produces a local commit, and publishing that commit would
// send the peer its own change back — which the peer would apply, commit, and
// (holding no record either) send back again.
func TestImportedOperationsDoNotEcho(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	const entryID = "20260304T050610-apply"
	peerOperation := "op-" + strings.Repeat("ab", 16)
	if err := RecordImport(f.root(), entryID, Import{OperationID: peerOperation, OriginDevice: f.peer.DeviceID}); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}

	const peerBody = "---\nstatus: draft\ntitle: Beta\n---\n\nThe peer wrote this.\n"
	f.write("Beta.md", peerBody)
	f.journalWrite(entryID, "Beta.md", peerBody)
	applied := f.commit("apply a peer operation")

	result := f.mustExport(Options{})
	if len(result.Packages) != 0 {
		t.Fatalf("the peer's own change was echoed back in %d packages", len(result.Packages))
	}
	requireSkipped(t, result.Skipped, []Skipped{{Commit: applied, Reason: SkipImported}})

	state := f.state()
	if state.Commit != applied {
		t.Fatalf("cursor: got %q, want %q", state.Commit, applied)
	}
	if got := state.Paths["Beta.md"]; got != peerOperation {
		t.Fatalf("path ledger for Beta.md: got %q, want the peer operation %q", got, peerOperation)
	}
	if _, still := state.Imported[entryID]; still {
		t.Fatal("the consumed import record was not cleared with the cursor advance")
	}

	// The causal edge is the reason for the ledger update: this device's later
	// edit has to say it had seen the peer's operation first.
	f.write("Beta.md", "---\nstatus: draft\ntitle: Beta\n---\n\nAnd then this device edited it.\n")
	f.commit("edit after the import")
	next := f.mustExport(Options{})
	requirePredecessors(t, f.changeFor(next, "Beta.md"), peerOperation)
}

// TestPrivateStateNeverEntersPackages is the fourth completion criterion. The
// export record itself is the sharpest case: it is a list of every portable path
// in the vault, and it is written into the very directory it must not describe.
func TestPrivateStateNeverEntersPackages(t *testing.T) {
	f := newFixture(t)

	stateDir := filepath.Join(f.root(), filepath.FromSlash(vaultsync.StateDir()))
	if got := StatePath(f.root()); filepath.Dir(got) != stateDir {
		t.Fatalf("the export record is at %q, outside the synchronization state directory %q", got, stateDir)
	}
	requireNotPortable(t,
		".bnotes/sync/export.json",
		".bnotes/sync/enrollment.json",
		".bnotes/sync/outgoing/pkg-0123456789abcdef0123456789abcdef/manifest.json",
		".bnotes/journal/20260304T050607-rm/manifest.json",
		".bnotes/recall.json",
		"brainnotes.local.toml",
	)

	f.mustExport(Options{})

	// Private state that the content history legitimately carries.
	f.write(".bnotes/recall.json", "{\"version\":1}\n")
	private := f.commit("record local state")
	// And private state it does not, forced in, so the exporter is asked about a
	// history it did not produce.
	f.write("brainnotes.local.toml", "editor = \"vi\"\n")
	forced := f.commitForced("force in the local overlay", "brainnotes.local.toml")

	result := f.mustExport(Options{})
	if len(result.Packages) != 0 {
		t.Fatalf("a private-only commit published %d packages", len(result.Packages))
	}
	requireSkipped(t, result.Skipped, []Skipped{
		{Commit: private, Reason: SkipPrivate},
		{Commit: forced, Reason: SkipPrivate},
	})
	if result.Cursor != forced {
		t.Fatalf("cursor: got %q, want %q", result.Cursor, forced)
	}

	requirePortableOnly(t, "the channel", f.channelPaths())
	requirePortableOnly(t, "the path ledger", f.ledgerPaths())
}

// TestCommitBoundariesAreOperationBoundaries pins the rule that one commit is
// one operation and one package: the boundary a peer applies whole is the one
// the vault recorded, not one this producer invented.
func TestCommitBoundariesAreOperationBoundaries(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "one\n")
	f.write("Beta.md", "two\n")
	first := f.commit("a two-file operation")
	f.write("Notes/Gamma.md", "three\n")
	second := f.commit("a one-file operation")

	result := f.mustExport(Options{})
	if len(result.Packages) != 2 {
		t.Fatalf("packages: got %d, want one per commit", len(result.Packages))
	}
	if result.Packages[0].Commit != first || result.Packages[1].Commit != second {
		t.Fatalf("packages describe %q and %q, want %q and %q",
			result.Packages[0].Commit, result.Packages[1].Commit, first, second)
	}
	if result.Packages[0].Sequence+1 != result.Packages[1].Sequence {
		t.Fatalf("sequences are not consecutive: %d then %d",
			result.Packages[0].Sequence, result.Packages[1].Sequence)
	}
	manifest := f.manifest(result.Packages[0].Dir)
	if len(manifest.Operations) != 1 || len(manifest.Operations[0].Changes) != 2 {
		t.Fatalf("the two-file commit is not one operation over two paths: %+v", manifest.Operations)
	}
	if f.operationFor(result, "Alpha.md") != f.operationFor(result, "Beta.md") {
		t.Fatal("two paths changed by one commit were given different operation identities")
	}
	if f.operationFor(result, "Notes/Gamma.md") == f.operationFor(result, "Alpha.md") {
		t.Fatal("two commits were collapsed into one operation")
	}
}

// TestOperationIdentityIsDerivedFromTheCommit covers the crash-and-retry case
// session 5 left open: a publication that landed and was never recorded is
// republished, and the peer must be able to see the two as one operation rather
// than apply it twice.
func TestOperationIdentityIsDerivedFromTheCommit(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "edited once\n")
	head := f.commit("edit Alpha")
	// Exactly what a pass that died after publishing and before recording
	// anything left behind: the record as it stood before the publication.
	before := f.state().clone()

	first := f.mustExport(Options{})
	identity := f.operationFor(first, "Alpha.md")

	f.putState(before)
	second := f.mustExport(Options{})
	if got := f.operationFor(second, "Alpha.md"); got != identity {
		t.Fatalf("republishing the same commit minted a new operation identity: %q then %q", identity, got)
	}
	if second.Packages[0].PackageID == first.Packages[0].PackageID {
		t.Fatal("a republication reused a package identity, which is immutable once published")
	}
	if got := vaultsync.DeriveOperationID(f.local.VaultID, head); got != identity {
		t.Fatalf("operation identity %q is not derived from the vault and the commit (%q)", identity, got)
	}
}

// TestUnbornBranchExportsNothing covers a vault between `git init` and its first
// snapshot. It has nothing to say, and saying so as an error would make every
// freshly enrolled vault report a failure on its first synchronization.
func TestUnbornBranchExportsNothing(t *testing.T) {
	f := newFixture(t)
	// A fresh repository over the same working tree: the files are there, no
	// commit is.
	if err := removeAll(f.vault.Path(".git")); err != nil {
		t.Fatalf("remove repository: %v", err)
	}
	f.vault.Git(t, "-c", "init.defaultBranch=main", "init", "-q")

	result := f.mustExport(Options{})
	if len(result.Packages) != 0 || result.Cursor != "" || result.Baseline {
		t.Fatalf("an unborn branch produced %+v", result)
	}
	if paths := f.channelPaths(); len(paths) != 0 {
		t.Fatalf("an unborn branch published %v", paths)
	}
}
