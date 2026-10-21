package vaultexport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// commitTreeEntry records one commit that adds rel holding body to HEAD's tree,
// through git's plumbing and without touching the working tree.
//
// It exists because the pair these tests are about cannot be put on this
// machine's disk at all. `Archive/Notes.md` and `archive/notes.md` are one name on
// a case-insensitive filesystem, and a producer running on one never produces the
// clash — the producer that does is running on Linux, and the replica that suffers
// for it is running here. Writing the entries as objects is the only way to hand
// this exporter the history that machine would have handed it.
//
// The branch moves and the working tree does not follow, which is fine for
// everything this package does: it reads committed objects and nothing else. A
// test that calls this should not commit through the fixture afterwards.
func (f *fixture) commitTreeEntry(message, rel, body string) string {
	f.t.Helper()
	return f.commitTreeEntries(message, rel, body)
}

// commitTreeEntries is the several-entries form, for a commit whose entries
// cannot all exist in one working tree at once — a name and the folded directory
// beside it, which is the pair this file is about.
func (f *fixture) commitTreeEntries(message string, relBody ...string) string {
	f.t.Helper()
	if len(relBody)%2 != 0 {
		f.t.Fatalf("commitTreeEntries wants path/body pairs, got %d arguments", len(relBody))
	}
	f.vault.Git(f.t, "read-tree", "HEAD")
	for i := 0; i < len(relBody); i += 2 {
		scratch := filepath.Join(f.t.TempDir(), "blob")
		if err := os.WriteFile(scratch, []byte(relBody[i+1]), 0o644); err != nil {
			f.t.Fatalf("stage blob content: %v", err)
		}
		oid := f.vault.Git(f.t, "hash-object", "-w", "--", scratch)
		f.vault.Git(f.t, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+relBody[i])
	}
	tree := f.vault.Git(f.t, "write-tree")
	commit := f.vault.Git(f.t, "commit-tree", tree, "-p", "HEAD", "-m", message)
	f.vault.Git(f.t, "update-ref", "HEAD", commit)
	return commit
}

// TestCollidingPathsNeverBothTravel is the producer-side half of the guarantee
// vaultsync.CollisionKey exists for.
//
// vaultsync refuses the pair inside one manifest, which is all a receiver can see.
// It is not all there is: one commit is one package here, so two paths added in
// two commits are two manifests that each validate and one file on the replica,
// where the second silently overwrites the first and every receipt says the
// transfer succeeded. The path ledger is the only place both halves are visible,
// and this is the check that looks there.
func TestCollidingPathsNeverBothTravel(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	if _, sent := f.state().Paths["Alpha.md"]; !sent {
		t.Fatal("the baseline did not send Alpha.md, so there is nothing to collide with")
	}
	before := f.channelPaths()

	head := f.commitTreeEntry("add a second Alpha under another fold", "alpha.md", "a different note\n")

	_, err := f.export(Options{})
	if !errors.Is(err, vaultsync.ErrPathCollision) {
		t.Fatalf("error: got %v, want ErrPathCollision", err)
	}
	// Both names, so a person can see which two files the vault has to choose
	// between, and the remedy, because no edit at the tip clears this one: the
	// clash is in a commit that already happened.
	for _, want := range []string{"alpha.md", "Alpha.md", "rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	requirePaths(t, f.channelPaths(), before)
	if got := f.state().Commit; got == head {
		t.Fatal("the cursor advanced past the refused commit")
	}
}

// TestARecreatedPathUnderAnotherFoldIsNotACollision is the case the ledger alone
// would get wrong.
//
// A path entry survives its own deletion, because a note recreated after a
// tombstone has to name that tombstone. So a note removed and later added back
// under a different fold looks exactly like a collision in the ledger and is not
// one: the peer applies the tombstone first and the write second, and ends with
// one file, which is what it would have had all along. Asking the commit whether
// the older name is still in its tree is what tells the two apart.
func TestARecreatedPathUnderAnotherFoldIsNotACollision(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	body := f.vault.Read(t, "Alpha.md")

	f.journalDeletion("20260304T050611-rm", "Alpha.md", body)
	f.remove("Alpha.md")
	f.commit("bn rm Alpha.md")
	if !f.changeFor(f.mustExport(Options{}), "Alpha.md").Tombstone() {
		t.Fatal("Alpha.md did not travel as a tombstone")
	}
	if _, kept := f.state().Paths["Alpha.md"]; !kept {
		t.Fatal("the ledger dropped the deleted path, so this test proves nothing")
	}

	const recreated = "a new note under the other fold\n"
	head := f.commitTreeEntry("recreate it under another fold", "alpha.md", recreated)

	result := f.mustExport(Options{})
	requirePaths(t, f.paths(result), []string{"alpha.md"})
	if got := f.payload(result, "alpha.md"); got != recreated {
		t.Fatalf("alpha.md payload: got %q, want %q", got, recreated)
	}
	if got := f.state().Commit; got != head {
		t.Fatalf("cursor: got %q, want %q", got, head)
	}
}

// TestACollisionIsFoundWhenTheLedgerHoldsBothNames is the case that makes the
// ledger index a list rather than one entry per key.
//
// The recreate case above is what legitimately puts two names under one collision
// key: the tombstoned `Alpha.md` and the live `alpha.md`. Bring `Alpha.md` back
// and both are live, and the answer now depends on which of the two the producer
// compares against — so it has to compare against both. Held one-per-key, this
// published or halted at random from one run to the next, and the run that
// published overwrote a note on the replica and reported success.
func TestACollisionIsFoundWhenTheLedgerHoldsBothNames(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	// Delete Alpha.md, then recreate it under the other fold — the legitimate
	// sequence, which leaves the ledger holding both names under one key.
	body := f.vault.Read(t, "Alpha.md")
	f.journalDeletion("20260304T050611-rm", "Alpha.md", body)
	f.remove("Alpha.md")
	f.commit("bn rm Alpha.md")
	f.mustExport(Options{})
	f.commitTreeEntry("recreate it under another fold", "alpha.md", "the surviving note\n")
	f.mustExport(Options{})

	ledger := f.state().Paths
	for _, want := range []string{"Alpha.md", "alpha.md"} {
		if _, held := ledger[want]; !held {
			t.Fatalf("the ledger does not hold %q, so this test proves nothing: %v", want, ledger)
		}
	}
	before := f.channelPaths()

	// Now bring the tombstoned name back. Both are live in this commit's tree and
	// they are one file on the replica.
	head := f.commitTreeEntry("bring the older fold back too", "Alpha.md", "a second note\n")

	_, err := f.export(Options{})
	if !errors.Is(err, vaultsync.ErrPathCollision) {
		t.Fatalf("error: got %v, want ErrPathCollision", err)
	}
	if !strings.Contains(err.Error(), "alpha.md") || !strings.Contains(err.Error(), "Alpha.md") {
		t.Errorf("the refusal does not name both paths: %v", err)
	}
	requirePaths(t, f.channelPaths(), before)
	if got := f.state().Commit; got == head {
		t.Fatal("the cursor advanced past the refused commit")
	}
}

// TestANameCannotBeAFileHereAndADirectoryThere covers the other half of
// vaultsync's rule, carried across packages.
//
// `Notes/Gamma.md` travelled in the baseline, so the replica holds a directory
// `Notes`. A later commit that adds a *file* the replica folds to that same name
// has no collision key in common with anything sent — `notes` and
// `notes/gamma.md` are different keys — and is still a package the replica cannot
// apply at all: a name is a file or a directory and not both. Only the producer
// can see the two halves.
func TestANameCannotBeAFileHereAndADirectoryThere(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	if _, sent := f.state().Paths["Notes/Gamma.md"]; !sent {
		t.Fatal("the baseline did not send Notes/Gamma.md, so there is no directory to clash with")
	}
	before := f.channelPaths()

	head := f.commitTreeEntry("add a file the replica folds onto the Notes directory", "notes", "not a directory\n")

	_, err := f.export(Options{})
	if !errors.Is(err, vaultsync.ErrPathCollision) {
		t.Fatalf("error: got %v, want ErrPathCollision", err)
	}
	for _, want := range []string{`"notes"`, "Notes/Gamma.md", "directory", "rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	requirePaths(t, f.channelPaths(), before)
	if got := f.state().Commit; got == head {
		t.Fatal("the cursor advanced past the refused commit")
	}
}

// TestAFileAlreadySentCannotBecomeADirectoryThere is the same clash approached
// from the other side, which finds its rival through a different index: here the
// outgoing path needs a directory whose folded name is a file this device already
// sent, rather than being that file itself.
func TestAFileAlreadySentCannotBecomeADirectoryThere(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	// An extensionless file at the vault root is ordinary and this machine can
	// hold it; only the directory that folds onto it needs plumbing.
	f.write("Archive", "a note with no extension\n")
	f.commit("add an extensionless note")
	f.mustExport(Options{})
	if _, sent := f.state().Paths["Archive"]; !sent {
		t.Fatal("Archive did not travel, so there is no file to clash with")
	}
	before := f.channelPaths()

	head := f.commitTreeEntry("add a note inside a directory of the same folded name", "archive/notes.md", "inside\n")

	_, err := f.export(Options{})
	if !errors.Is(err, vaultsync.ErrPathCollision) {
		t.Fatalf("error: got %v, want ErrPathCollision", err)
	}
	for _, want := range []string{"archive/notes.md", `"Archive"`, "directory", "rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	requirePaths(t, f.channelPaths(), before)
	if got := f.state().Commit; got == head {
		t.Fatal("the cursor advanced past the refused commit")
	}
}

// TestADirectoryRivalIsStillSubjectToLiveness proves the file/directory half
// goes through the same tombstone gate as the name half, rather than refusing
// every vault that ever had a file where a directory now is.
//
// The deleted file is spelled exactly as the directory that replaces it, which is
// the hard case and the reason the gate asks for an object kind rather than for
// a yes or no: the commit does hold something at "archive", and it is the very
// directory the outgoing path needs. A gate that only asked whether the name was
// occupied would refuse this vault forever, with a halt no edit at the tip
// clears.
func TestADirectoryRivalIsStillSubjectToLiveness(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	const body = "a note with no extension\n"
	f.write("archive", body)
	f.commit("add an extensionless note")
	f.mustExport(Options{})

	f.journalDeletion("20260304T050611-rm", "archive", body)
	f.remove("archive")
	f.commit("bn rm archive")
	if !f.changeFor(f.mustExport(Options{}), "archive").Tombstone() {
		t.Fatal("archive did not travel as a tombstone")
	}

	// The ledger still holds archive as a file, and the commit holds a directory
	// under that name. The peer applies the tombstone before this write and ends
	// up with the directory.
	head := f.commitTreeEntry("put a directory where the file was", "archive/notes.md", "inside\n")

	result := f.mustExport(Options{})
	requirePaths(t, f.paths(result), []string{"archive/notes.md"})
	if got := f.state().Commit; got != head {
		t.Fatalf("cursor: got %q, want %q", got, head)
	}
}

// TestARivalIsNotHiddenByAnotherRivalUnderneathIt is about how the rivals are
// asked about rather than about which ones are found.
//
// A path's rivals routinely include a name *and* names beneath it — the folded
// name itself, and everything this device sent from inside a directory of that
// name. Asked as git pathspecs, those two are not independent questions: the
// deeper one makes git descend through the directory instead of reporting it, so
// the entry that matters disappears from the answer and the gate reads "that name
// is free". Here `archive` is a live directory and `archive/old.md` is a
// tombstone, and losing the first publishes `Archive` silently, with a receipt
// saying it worked. `rivalName` is the conservative arm on purpose: an occupied
// name is occupied, whatever the peer has left in its copy of that directory.
func TestARivalIsNotHiddenByAnotherRivalUnderneathIt(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	// A file named archive travels, then dies. The ledger keeps it, because a
	// resurrection has to be able to name its own tombstone.
	const outer = "a note with no extension\n"
	f.write("archive", outer)
	f.commit("add an extensionless note")
	f.mustExport(Options{})
	f.journalDeletion("20260304T050611-rm", "archive", outer)
	f.remove("archive")
	f.commit("bn rm archive")
	f.mustExport(Options{})

	// A directory takes the name, and a note inside it travels and dies too, so the
	// ledger now holds a dead path *underneath* a dead name.
	const inner = "an archived note\n"
	f.write("archive/old.md", inner)
	f.commit("file a note under archive")
	f.mustExport(Options{})
	f.journalDeletion("20260304T050613-rm", "archive/old.md", inner)
	f.remove("archive/old.md")
	f.commit("bn rm archive/old.md")
	f.mustExport(Options{})

	// The directory comes back holding something that never travels, so `archive`
	// is a live tree while every ledger entry under it stays dead. A portable file
	// would have to go in this same commit — the two names cannot share a working
	// tree here — and would then be a live rival of its own, which is a different
	// refusal than the one this test is about.
	f.commitTreeEntry("leave a Finder sidecar under archive", "archive/.DS_Store", "\x00\x01")
	f.mustExport(Options{})

	before := f.channelPaths()
	f.commitTreeEntry("add a file beside the folded directory", "Archive", "a second note under another fold\n")

	_, err := f.export(Options{})
	if !errors.Is(err, vaultsync.ErrPathCollision) {
		t.Fatalf("error: got %v, want ErrPathCollision", err)
	}
	// The rival named must be the directory, not the tombstone beneath it: a
	// refusal that pointed at `archive/old.md` would be pointing at a path the peer
	// has already removed.
	for _, want := range []string{`"Archive"`, `"archive"`, "rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "archive/old.md") {
		t.Errorf("the refusal names a tombstone as the live rival: %v", err)
	}
	requirePaths(t, f.channelPaths(), before)
}

// TestBaselineSlicesCollideWithEachOther covers the same clash arriving through
// the other producer, where the two paths are in one tree rather than in two
// commits. Nothing about the check changes; the point is that the baseline goes
// through the same door.
func TestBaselineSlicesCollideWithEachOther(t *testing.T) {
	f := newFixture(t)
	f.commitTreeEntry("add a second Alpha under another fold", "alpha.md", "a different note\n")

	// One path per package, so the two names are certain to land in different
	// slices rather than in one manifest vaultsync would refuse by itself.
	_, err := f.export(Options{MaxBaselineBytes: 1})
	if !errors.Is(err, vaultsync.ErrPathCollision) {
		t.Fatalf("error: got %v, want ErrPathCollision", err)
	}
	if got := f.state().Commit; got != "" {
		t.Fatalf("the refused baseline finished at %q", got)
	}
}
