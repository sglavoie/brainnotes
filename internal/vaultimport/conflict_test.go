package vaultimport

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// The bodies one replica writes while the other is writing its own. They differ
// from apply_test.go's, which are what the *peer* sends: a conflict needs a third
// version of the path, and a test that reused the packaged body would be
// asserting about convergence without noticing.
const (
	localAlphaBody = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, edited here and not committed.\n"
	localGammaBody = "---\nstatus: draft\ntitle: Gamma\n---\n\nGamma body, edited here and not committed.\n"
	peerGammaBody  = "---\nstatus: review\ntitle: Gamma\n---\n\nGamma body, rewritten on the peer.\n"
)

// TestApplyHoldsAConcurrentEditWithEveryVersionOfThePath is the session's
// central claim in its plainest form: both replicas edited one note from one
// starting point, the import is held rather than refused-and-forgotten, and all
// three versions survive somewhere a resolution can reach them.
//
// The baseline assertion is the one that is easy to lose. The remote branch is
// in the package and the local branch is on disk, so both are trivially
// available; the version the two diverged *from* exists only in this vault's
// content history, and a three-way merge without it is a two-way merge.
func TestApplyHoldsAConcurrentEditWithEveryVersionOfThePath(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto a note edited here")

	record := heldOn(t, r, manifest.PackageID)
	if record.OperationID == "" || record.OriginDevice != manifest.OriginDevice {
		t.Fatalf("the hold records %+v, want the package's own operation and origin", record)
	}
	if record.Sequence != manifest.Sequence {
		t.Fatalf("the hold records sequence %d, want %d", record.Sequence, manifest.Sequence)
	}
	if record.DetectedAt.IsZero() {
		t.Fatal("the hold records no detection time, so nothing can say how long it has waited")
	}

	contested := onlyContested(t, record)
	if contested.Path != "Alpha.md" || contested.Kind != ConflictConcurrentEdit {
		t.Fatalf("held %q as %q, want a concurrent edit on Alpha.md", contested.Path, contested.Kind)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, alphaBody, "the remote branch")
	requireBranch(t, r, manifest.PackageID, contested.Local, localAlphaBody, "the local branch")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
	requireHeldQuietly(t, r, head, commits)
}

// TestApplyHoldsADeletionThisVaultHasEditedSince is the delete/edit race. The
// peer removed a note this vault has since changed, so applying the tombstone
// would delete the only copy of an edit nothing else holds.
func TestApplyHoldsADeletionThisVaultHasEditedSince(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Notes/Gamma.md")
	packaged := r.believes(t, "Notes/Gamma.md")
	r.pair.Local.Write(t, "Notes/Gamma.md", localGammaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a tombstone onto a note edited here")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Gamma.md" || contested.Kind != ConflictDeleteEdit {
		t.Fatalf("held %q as %q, want a delete/edit race on Notes/Gamma.md", contested.Path, contested.Kind)
	}
	// A deletion is a branch, and it is not an unknown one: the record says the
	// peer's side of this path is "gone" rather than declining to answer.
	requireAbsentBranch(t, contested.Remote, "the remote branch of a deletion")
	requireBranch(t, r, manifest.PackageID, contested.Local, localGammaBody, "the local branch")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if got := r.pair.Local.Read(t, "Notes/Gamma.md"); got != localGammaBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
}

// TestApplyHoldsAWriteOntoANoteThisVaultDeleted is the mirror. Applying it would
// resurrect a note somebody removed on purpose, which no expectation the package
// carries can distinguish from an ordinary create.
func TestApplyHoldsAWriteOntoANoteThisVaultDeleted(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Notes/Gamma.md")
	packaged := r.believes(t, "Notes/Gamma.md")
	if err := os.Remove(r.pair.Local.Path("Notes/Gamma.md")); err != nil {
		t.Fatalf("delete the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Gamma.md", []byte(peerGammaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a write onto a note deleted here")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Gamma.md" || contested.Kind != ConflictEditDelete {
		t.Fatalf("held %q as %q, want an edit/delete race on Notes/Gamma.md", contested.Path, contested.Kind)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, peerGammaBody, "the remote branch")
	requireAbsentBranch(t, contested.Local, "the local branch of a deleted note")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if exists(t, r.pair.Local, "Notes/Gamma.md") {
		t.Fatal("a held import resurrected the deleted note")
	}
}

// TestApplyHoldsARenameThisVaultDivergedFrom covers the case a per-path
// comparison gets wrong on its own. The source of the rename is gone here, which
// reads as "already done"; it is only by asking about the *destination* that the
// vault can tell an identical rename from this vault having moved the same note
// somewhere else entirely.
func TestApplyHoldsARenameThisVaultDivergedFrom(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")

	// This vault moved the same note under a name of its own choosing.
	r.pair.Local.Write(t, "Archive/Alpha.md", moved)
	if err := os.Remove(r.pair.Local.Path("Alpha.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a rename of a note this vault moved elsewhere")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Alpha.md" || contested.Kind != ConflictDivergentRename {
		t.Fatalf("held %q as %q, want a divergent rename of Alpha.md", contested.Path, contested.Kind)
	}
	if contested.RenamedTo != "Notes/Alpha.md" {
		t.Fatalf("the hold names %q as the destination, want Notes/Alpha.md", contested.RenamedTo)
	}
	// Both live branches of a rename are absences — the note is gone from the
	// source here and was never created at the destination — so the baseline is
	// the only content there is to keep, and the package itself is carrying it.
	requireAbsentBranch(t, contested.Remote, "the remote branch of a rename source")
	requireAbsentBranch(t, contested.Local, "the local branch of a moved note")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, moved, "the baseline")

	if exists(t, r.pair.Local, "Notes/Alpha.md") {
		t.Fatal("a held rename created a second copy under the peer's chosen name")
	}
	if got := r.pair.Local.Read(t, "Archive/Alpha.md"); got != moved {
		t.Fatalf("this vault's own copy holds %q, want it untouched", got)
	}
}

// TestApplyDoesNotCallItADivergentRenameWhenTheDestinationIsTaken is the
// boundary of the rule above. The source of the rename is gone here too, so the
// signature looks the same from the source alone — but the destination is
// occupied, by content this vault put there and the peer never saw.
//
// That is a real disagreement and it is the destination's own change that says
// so. Reporting the source as a divergent rename as well would hold one
// operation twice for one event, and would tell a person this vault moved the
// note somewhere unknown while it is plainly sitting at the name the package
// chose.
func TestApplyDoesNotCallItADivergentRenameWhenTheDestinationIsTaken(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")

	// This vault made its own move to the very name the peer chose, carrying
	// content of its own rather than the bytes the peer is sending.
	r.pair.Local.Write(t, "Notes/Alpha.md", localAlphaBody)
	if err := os.Remove(r.pair.Local.Path("Alpha.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a rename onto a destination this vault has filled")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Alpha.md" || contested.Kind != ConflictConcurrentEdit {
		t.Fatalf("held %q as %q, want a concurrent edit on Notes/Alpha.md", contested.Path, contested.Kind)
	}
	if contested.RenamedTo != "" {
		t.Fatalf("the hold names %q as a rename destination, want none", contested.RenamedTo)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, moved, "the remote branch")
	requireBranch(t, r, manifest.PackageID, contested.Local, localAlphaBody, "the local branch")
	requireAbsentBranch(t, contested.Baseline, "the baseline of a path the peer believed was free")

	if exists(t, r.pair.Local, "Alpha.md") {
		t.Fatal("a held import restored the note at the source it was moved from")
	}
	if got := r.pair.Local.Read(t, "Notes/Alpha.md"); got != localAlphaBody {
		t.Fatalf("the destination holds %q, want this vault's own content untouched", got)
	}
}

// TestApplyDoesNotGuessWhichCopyARenameMeantWhenThereAreTwo is the limit of
// recovering a rename from a tombstone and a creation of the same bytes.
//
// One operation that removes a note and creates its content at two names — a
// note moved and also duplicated in one commit — offers two equally good
// matches, and picking one is a coin toss decided by the manifest's ordering.
// The unpaired tombstone is judged as the deletion it plainly is, which is the
// answer that is merely incomplete rather than the answer that is wrong about
// which note went where: here the note is already gone locally, so the deletion
// has converged and both copies land.
func TestApplyDoesNotGuessWhichCopyARenameMeantWhenThereAreTwo(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	// Removed and committed rather than merely deleted: an unexplained missing
	// tracked path is what the Git preflight exists to refuse, and this test is
	// about the rename pairing rather than about that.
	r.pair.Local.Git(t, "rm", "--quiet", "Alpha.md")
	r.pair.Local.Commit(t, "remove the note this vault no longer wants")

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
		b.Write("Archive/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	r.mustApply(t, dir)

	if state := importState(t, r); len(state.Conflicts) != 0 {
		t.Fatalf("a duplicated creation was held as a divergent rename: %v", state.Conflicts)
	}
	for _, rel := range []string{"Notes/Alpha.md", "Archive/Alpha.md"} {
		if got := r.pair.Local.Read(t, rel); got != moved {
			t.Fatalf("%s holds %q, want the moved note", rel, got)
		}
	}
	if exists(t, r.pair.Local, "Alpha.md") {
		t.Fatal("the tombstone was paired with a creation instead of being applied")
	}
}

// TestAHoldDropsTheRetainedVersionsItNoLongerNames is what keeps a conflict that
// nobody resolves from growing without bound.
//
// A hold is re-decided on every pass and the local branch is re-read from the
// vault each time, so a person editing the contested note while they think about
// it produces a new local version per edit. The sweep cannot collect them: the
// record still names the package, so the directory is correctly skipped. The
// record is the only thing that knows which copies it has stopped pointing at.
func TestAHoldDropsTheRetainedVersionsItNoLongerNames(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto a note edited here")
	superseded := onlyContested(t, heldOn(t, r, manifest.PackageID)).Local
	if superseded.Bytes == "" {
		t.Fatal("the first hold kept no local branch, so there is nothing for the second to supersede")
	}

	// The person edits the contested note again while the conflict waits, and the
	// transport delivers the package a second time.
	reconsidered := localAlphaBody + "\nA further edit, made while the conflict waited.\n"
	r.pair.Local.Write(t, "Alpha.md", reconsidered)
	_, err = r.apply(t, dir)
	requireIs(t, err, ErrConflict, "re-applying a package onto a note edited again")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	requireBranch(t, r, manifest.PackageID, contested.Local, reconsidered, "the local branch after the second edit")

	kept := retainedFiles(t, r, manifest.PackageID)
	want := sortedNames(contested.Remote.Bytes, contested.Local.Bytes, contested.Baseline.Bytes)
	requireEqual(t, kept, want, "the retained branches after the second hold")
	for _, name := range kept {
		if name == superseded.Bytes {
			t.Fatalf("the superseded local branch %s is still retained", superseded.Bytes)
		}
	}
}

// TestCollisionFindsAnotherSpellingWhateverTheFilesystemDoes drives the
// collision scan through its cached listing rather than through a directory.
//
// It is the one rule here that a local filesystem can hide entirely. APFS and
// NTFS answer the packaged path with the note they already hold, so the ordinary
// content comparison catches the case and this branch never runs — which means
// the code that matters on a case-sensitive replica would otherwise be asserted
// by nothing on the machine it is developed on. The listing is supplied, so this
// is about the rule and not about the disk.
func TestCollisionFindsAnotherSpellingWhateverTheFilesystemDoes(t *testing.T) {
	// One note named "Caf\u00e9.md", spelled the two ways a pair of replicas
	// spell it: composed, and decomposed as a Mac-created note reaches Linux.
	const composed, decomposed = "Caf\u00e9.md", "Cafe\u0301.md"
	listing := map[string][]string{
		"Notes": {"Gamma.md", "Delta.md", composed},
		"":      {"Alpha.md"},
	}
	write := func(rel string) vaultsync.Change {
		return vaultsync.Change{Path: rel, Action: vaultsync.ActionWrite, ResultSHA256: vaultsync.HashBytes([]byte(peerGammaBody))}
	}
	cases := []struct {
		name string

		change vaultsync.Change
		state  pathState
		want   string
	}{
		{
			name:   "a spelling that differs only in case",
			change: write("Notes/gamma.md"),
			want:   "Notes/Gamma.md",
		},
		{
			name:   "a spelling that differs only in normalization",
			change: write("Notes/" + decomposed),
			want:   "Notes/" + composed,
		},
		{
			name:   "a name nothing in the directory collides with",
			change: write("Notes/Epsilon.md"),
			want:   "",
		},
		{
			name:   "a name at the vault root",
			change: write("alpha.md"),
			want:   "Alpha.md",
		},
		{
			name:   "a path this vault already holds, which the content comparison answers",
			change: write("Notes/gamma.md"),
			state:  pathState{present: true, regular: true, sha256: vaultsync.HashBytes([]byte(localGammaBody))},
			want:   "",
		},
		{
			name:   "a tombstone, which a similarly named neighbour says nothing about",
			change: vaultsync.Change{Path: "Notes/gamma.md", Action: vaultsync.ActionDelete},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scan := &inspection{dirs: listing}
			collides, err := scan.collision(tc.change, tc.state)
			if err != nil {
				t.Fatalf("collision: %v", err)
			}
			if collides != tc.want {
				t.Fatalf("collision reported %q, want %q", collides, tc.want)
			}
		})
	}
}

// retainedFiles lists what one held package's conflict directory actually holds,
// sorted, which is the disk's own answer to what the record claims to name.
func retainedFiles(t *testing.T, r *replicas, packageID string) []string {
	t.Helper()
	dir, err := conflictDir(r.root(), packageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("list the retained branches: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// sortedNames is the set of retained files a record names, in the order the disk
// lists them. Branches that could not be kept — a baseline older than this
// vault's history, a local copy that lost a race with the person editing it —
// name nothing and are dropped, so a legitimately unretained branch produces a
// failure about the files rather than about an empty string.
func sortedNames(names ...string) []string {
	sorted := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" {
			sorted = append(sorted, name)
		}
	}
	sort.Strings(sorted)
	return sorted
}

// TestApplyConvergesOnARenameThisVaultAlreadyMade is the other side of that
// question: the same rename, made here first. Nothing is applied, nothing is
// held, and no commit is produced — the identical-outcome case has to stay cheap
// or every replica would re-commit its own content back over itself.
func TestApplyConvergesOnARenameThisVaultAlreadyMade(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)

	r.pair.Local.Write(t, "Notes/Alpha.md", moved)
	if err := os.Remove(r.pair.Local.Path("Alpha.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	result := r.mustApply(t, dir)
	requireConvergedResult(t, r, manifest, result)
	requireHeldQuietly(t, r, head, commits)

	// A redelivery is answered from the ledger rather than re-decided, which is
	// what keeps the peer's retries free.
	again := r.mustApply(t, dir)
	requireConvergedResult(t, r, manifest, again)
}

// TestApplyConvergesOnAPackageThisVaultHasAlreadyApplied is the plain
// identical-outcome case: the same edit, made independently on both sides.
func TestApplyConvergesOnAPackageThisVaultHasAlreadyApplied(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)
	r.pair.Local.Write(t, "Alpha.md", alphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	result := r.mustApply(t, dir)
	requireConvergedResult(t, r, manifest, result)
	requireHeldQuietly(t, r, head, commits)
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("the converged note holds %q, want the body both replicas wrote", got)
	}
}

// TestAConvergedPackageRetriesAnUnpublishedAcknowledgment keeps the no-write
// path under the same delivery guarantee as an ordinary application: a blocked
// channel is recorded without inventing a recovery entry, and a redelivery
// publishes the acknowledgment once the channel is writable again.
func TestAConvergedPackageRetriesAnUnpublishedAcknowledgment(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", alphaBody)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})
	blockOutgoingChannel(t, r.root())

	result, err := r.apply(t, dir)
	if err == nil || result == nil || !result.Converged {
		t.Fatalf("blocked convergence returned result %+v and error %v", result, err)
	}
	if result.Receipt != nil || result.ReceiptPath != "" {
		t.Fatalf("an unpublished convergence receipt is reported as %v at %q", result.Receipt, result.ReceiptPath)
	}
	record := importState(t, r).Applied[manifest.PackageID]
	if !record.Converged || record.Receipt != nil {
		t.Fatalf("the blocked convergence record is %+v", record)
	}
	if err := os.Remove(vaultsync.OutgoingDir(r.root())); err != nil {
		t.Fatalf("unblock outgoing channel: %v", err)
	}

	retried := r.mustApply(t, dir)
	requireConvergedResult(t, r, manifest, retried)
	if !retried.Duplicate {
		t.Fatal("the retried convergence was not answered from the import ledger")
	}
}

// TestApplyAppliesOnlyWhatThisVaultHasNotAlreadyDone is the partial case, and it
// is where convergence has to be more than an early exit. One path of the
// package is already at its result and the other is not: the transaction must
// stage only the second, and the receipt must still answer for both, because a
// receipt states what this vault holds rather than what it wrote.
func TestApplyAppliesOnlyWhatThisVaultHasNotAlreadyDone(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	commits := commitCount(t, r.pair.Local)
	r.pair.Local.Write(t, "Alpha.md", alphaBody)

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	result := r.mustApply(t, dir)
	if result.Converged {
		t.Fatal("a package with work left to do was recorded as converged")
	}
	if result.Receipt == nil || len(result.Receipt.Applied) != 2 {
		t.Fatalf("the receipt acknowledges %v, want both of the package's paths", result.Receipt)
	}
	for _, applied := range result.Receipt.Applied {
		onDisk := vaultsync.HashBytes([]byte(r.pair.Local.Read(t, applied.Path)))
		if applied.SHA256 != onDisk {
			t.Fatalf("the receipt acknowledges %s for %q, the file holds %s", applied.SHA256, applied.Path, onDisk)
		}
	}
	// Exactly one commit, holding only the path the transaction actually wrote.
	// The converged note was already at its result, so committing it here would
	// put this vault's own uncommitted work into an import's commit.
	if got := commitCount(t, r.pair.Local); got != commits+1 {
		t.Fatalf("the vault holds %d commits, want %d", got, commits+1)
	}
	changed := contentPaths(commitPaths(t, r.pair.Local, "HEAD"))
	requireEqual(t, changed, []string{"Notes/Delta.md"}, "the applying commit carries")
}

// TestApplyHoldsANameOccupiedBySomethingThatIsNotANote is the filename-collision
// kind in the form every filesystem agrees on: a directory standing where the
// package expects to create a note. The remedy is clearing the name rather than
// choosing between two files, which is why it is not a content conflict.
func TestApplyHoldsANameOccupiedBySomethingThatIsNotANote(t *testing.T) {
	r := enroll(t)
	if err := os.Mkdir(r.pair.Local.Path("Notes/Delta.md"), 0o755); err != nil {
		t.Fatalf("occupy the name locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a note onto a name a directory holds")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Delta.md" || contested.Kind != ConflictFilenameCollision {
		t.Fatalf("held %q as %q, want a filename collision on Notes/Delta.md", contested.Path, contested.Kind)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, deltaBody, "the remote branch")
	// A name occupied by something that is not a file exists and has no content,
	// which is the one branch shape that is present and unretainable.
	if !contested.Local.Exists || contested.Local.SHA256 != "" || contested.Local.Bytes != "" {
		t.Fatalf("the local branch is %+v, want present with no content", contested.Local)
	}
	requireAbsentBranch(t, contested.Baseline, "the baseline of a creation")

	info, err := os.Lstat(r.pair.Local.Path("Notes/Delta.md"))
	if err != nil || !info.IsDir() {
		t.Fatalf("the occupied name is %v (err %v), want the directory untouched", info, err)
	}
}

// TestApplyHoldsANameThatCollidesUnderADifferentSpelling is the collision a
// case-sensitive replica cannot see by looking at the path it was given. It is
// also the one assertion in this file that the filesystem gets a say in, and the
// split is the point rather than a concession: a folding replica answers the
// packaged path with the note it already holds, so the ordinary content
// comparison has already caught it and there is nothing for the collision scan
// to add. The refusal and the untouched note are required of both.
func TestApplyHoldsANameThatCollidesUnderADifferentSpelling(t *testing.T) {
	r := enroll(t)
	gamma := r.pair.Local.Read(t, "Notes/Gamma.md")

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/gamma.md", []byte(peerGammaBody), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a note this vault spells differently")

	if got := r.pair.Local.Read(t, "Notes/Gamma.md"); got != gamma {
		t.Fatalf("the existing note holds %q, want it untouched", got)
	}
	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if foldsNames(t, r.pair.Dir) {
		if contested.Kind != ConflictConcurrentEdit {
			t.Fatalf("held %q on a folding filesystem, want the content conflict it is there", contested.Kind)
		}
		return
	}
	if contested.Kind != ConflictFilenameCollision {
		t.Fatalf("held %q as %q, want a filename collision", contested.Path, contested.Kind)
	}
	if contested.Collides != "Notes/Gamma.md" {
		t.Fatalf("the hold names %q as the colliding note, want Notes/Gamma.md", contested.Collides)
	}
	if exists(t, r.pair.Local, "Notes/gamma.md") {
		t.Fatal("a held import created a second file for what is one name on the other replica")
	}
}

// TestApplyHoldsAConfigurationChangeThisVaultAlsoMade separates the declarations
// from the notes they govern. The content comparison would have reported this as
// an ordinary concurrent edit; the kind is what stops a resolution offering
// "keep mine" for a file that decides what every note in the vault must carry.
func TestApplyHoldsAConfigurationChangeThisVaultAlsoMade(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, config.FileName)
	shared := vaultsync.Existing(vaultsync.HashBytes([]byte(baseline)))
	local := baseline + "\n[frontmatter.properties.area]\ntype = \"string\"\n"
	remote := baseline + "\n[frontmatter.properties.org]\ntype = \"string\"\n"
	r.pair.Local.Write(t, config.FileName, local)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write(config.FileName, []byte(remote), shared)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying declarations this vault has also changed")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != config.FileName || contested.Kind != ConflictConfiguration {
		t.Fatalf("held %q as %q, want a configuration conflict on %s", contested.Path, contested.Kind, config.FileName)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, remote, "the remote branch")
	requireBranch(t, r, manifest.PackageID, contested.Local, local, "the local branch")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if got := r.pair.Local.Read(t, config.FileName); got != local {
		t.Fatalf("the declarations hold %q, want this vault's own untouched", got)
	}
}

// TestAHoldIsLiftedWhenTheDisagreementIsResolved is the half that makes holding
// survivable. A hold is a statement about the disk rather than a latch, so a
// person resolving the conflict by editing the note is the whole of the
// resolution protocol — no command, no state to clear by hand — and the retained
// branches go with the application that no longer needs them.
func TestAHoldIsLiftedWhenTheDisagreementIsResolved(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto a note edited here")
	heldOn(t, r, manifest.PackageID)
	retained, err := conflictDir(r.root(), manifest.PackageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}
	if _, err := os.Lstat(retained); err != nil {
		t.Fatalf("the retained branches are not on disk: %v", err)
	}

	// The person decides in favour of the peer by putting the note back to what
	// both replicas last agreed on.
	r.pair.Local.Write(t, "Alpha.md", baseline)

	result := r.mustApply(t, dir)
	if result.Receipt == nil {
		t.Fatal("the resolved import minted no receipt")
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("the resolved note holds %q, want the packaged body", got)
	}
	state := importState(t, r)
	if len(state.Conflicts) != 0 {
		t.Fatalf("the hold survived the application it was waiting for: %v", state.Conflicts)
	}
	if _, err := os.Lstat(retained); !os.IsNotExist(err) {
		t.Fatalf("the retained branches survived the application (%v)", err)
	}
}

// TestRecoveryLeavesAConvergedPathAlone is the hazard convergence introduces and
// the reason Attempt.Converged is recorded rather than recomputed.
//
// A converged path reads exactly like one the import installed — it holds the
// package's result — so a recovering process planning a rollback from the disk
// alone would "undo" it to the package's baseline, destroying local content the
// interrupted application never touched. The interruption is staged by driving
// the applier's own steps and stopping where a crash after the commit would:
// the attempt is durable, the content is applied, and the journal entry exists
// but was never finalized.
func TestRecoveryLeavesAConvergedPathAlone(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", alphaBody)

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	interruptAfterCommit(t, r, dir)

	report, err := Recover(r.root(), Options{Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Outcome != OutcomeRolledBack {
		t.Fatalf("recovery reported %q, want a rollback", report.Outcome)
	}
	requireEqual(t, report.Paths, []string{"Notes/Delta.md"}, "the rolled back paths")

	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("the converged note holds %q, want this vault's own content untouched", got)
	}
	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("the rollback left the path the import had created")
	}
}

// interruptAfterCommit drives one package through the applier as far as the
// content transaction and stops there, leaving the disk a crash at crashApplied
// would leave. It is done in-process rather than through the subprocess helper
// because the state under test is the attempt record's contents rather than the
// timing, and the steps are the production ones either way.
func interruptAfterCommit(t *testing.T, r *replicas, packageDir string) {
	t.Helper()
	session, err := accept(r.root(), packageDir, Options{Now: r.clock.Now})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := session.read(packageDir); err != nil {
		t.Fatalf("read: %v", err)
	}
	found, err := session.inspect()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(found.conflicts) != 0 {
		t.Fatalf("the package conflicts before it was applied: %v", found.conflicts)
	}
	session.mark(found)
	entryID, err := journal.NewEntryID(r.clock.Now())
	if err != nil {
		t.Fatalf("NewEntryID: %v", err)
	}
	operation, err := noteops.BeginOperation(r.root(), operationToken, "bn sync apply --test",
		session.targets(), session.opts.recorderOptions(entryID)...)
	if err != nil {
		t.Fatalf("BeginOperation: %v", err)
	}
	txn, err := session.plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := session.declare(entryID); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := session.commit(txn, operation); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// importState reads this vault's import record the way a second process would.
func importState(t *testing.T, r *replicas) *State {
	t.Helper()
	state, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state
}

// heldOn returns the durable hold recorded for one package, failing when there
// is none: a refusal nobody wrote down is the state this session exists to end.
func heldOn(t *testing.T, r *replicas, packageID string) *ConflictRecord {
	t.Helper()
	state := importState(t, r)
	record, held := state.Conflicts[packageID]
	if !held {
		t.Fatalf("%s was refused without being held; the record holds %v", packageID, state.Conflicts)
	}
	if record.PackageID != packageID {
		t.Fatalf("the hold for %s names %s", packageID, record.PackageID)
	}
	return record
}

// onlyContested returns the single contested path of a hold.
func onlyContested(t *testing.T, record *ConflictRecord) PathConflict {
	t.Helper()
	if len(record.Paths) != 1 {
		t.Fatalf("the hold names %d contested paths, want 1: %+v", len(record.Paths), record.Paths)
	}
	return record.Paths[0]
}

// requireBranch asserts that one branch was both named and kept, and that the
// bytes it points at are the bytes it claims. Recording a digest is cheap and a
// record that only did that would pass an assertion about existence while
// offering a person nothing to merge.
func requireBranch(t *testing.T, r *replicas, packageID string, branch Branch, want, context string) {
	t.Helper()
	if !branch.Exists {
		t.Fatalf("%s: recorded as absent, want the content %q", context, want)
	}
	if branch.SHA256 != vaultsync.HashBytes([]byte(want)) {
		t.Fatalf("%s: recorded digest %s, which is not the digest of %q", context, branch.SHA256, want)
	}
	if branch.Bytes == "" {
		t.Fatalf("%s: named %s but kept no copy of it", context, branch.SHA256)
	}
	dir, err := conflictDir(r.root(), packageID)
	if err != nil {
		t.Fatalf("%s: conflictDir: %v", context, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, branch.Bytes))
	if err != nil {
		t.Fatalf("%s: read the retained copy: %v", context, err)
	}
	if string(data) != want {
		t.Fatalf("%s: the retained copy holds %q, want %q", context, data, want)
	}
}

func requireAbsentBranch(t *testing.T, branch Branch, context string) {
	t.Helper()
	if branch.Exists || branch.SHA256 != "" || branch.Bytes != "" {
		t.Fatalf("%s: recorded %+v, want an absence", context, branch)
	}
}

// requireHeldQuietly asserts that a decision which applied nothing also left
// nothing: no recovery entry, no attempt for a later import to trip over, no
// movement in the content history, and nothing of the retained store inside it.
func requireHeldQuietly(t *testing.T, r *replicas, head string, commits int) {
	t.Helper()
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("an import that applied nothing left %d recovery entries behind", len(entries))
	}
	if state := importState(t, r); state.Pending != nil {
		t.Fatal("an import that applied nothing declared an attempt, which strands every later import")
	}
	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("an import that applied nothing moved the content history")
	}
	if got := commitCount(t, r.pair.Local); got != commits {
		t.Fatalf("the vault holds %d commits, want %d", got, commits)
	}
	// The retained branches are copies of notes that are not those notes, so the
	// store they live in must be outside what the content history tracks.
	store := path.Join(vaultsync.StateDir(), conflictsName)
	if vaultgit.InScope(store) {
		t.Fatalf("%q is inside the content history's scope", store)
	}
}

// requireConvergedResult asserts the shape of an application that had nothing to
// do: recorded as applied so a dependent can proceed, and acknowledged as
// convergence without claiming a recovery entry for content it never wrote.
func requireConvergedResult(t *testing.T, r *replicas, manifest *vaultsync.Manifest, result *Result) {
	t.Helper()
	if !result.Converged {
		t.Fatal("a package this vault had already applied was not reported as converged")
	}
	requireConvergedReceipt(t, result.Receipt)
	if result.ReceiptPath == "" {
		t.Fatal("the convergence acknowledgment was not published")
	}
	if result.PackageID != manifest.PackageID {
		t.Fatalf("the result names %s, want %s", result.PackageID, manifest.PackageID)
	}
	record, applied := importState(t, r).Applied[manifest.PackageID]
	if !applied {
		t.Fatal("a converged package was not recorded as applied, so a dependent would wait on it forever")
	}
	requireConvergedRecord(t, record)
}

func requireConvergedReceipt(t *testing.T, receipt *vaultsync.Receipt) {
	t.Helper()
	if receipt == nil || !receipt.Converged || receipt.RecoveryEntry != "" {
		t.Fatalf("a converged package returned receipt %+v, want an explicit convergence acknowledgment", receipt)
	}
}

func requireConvergedRecord(t *testing.T, record Record) {
	t.Helper()
	if !record.Converged || record.JournalEntry != "" || record.Receipt == nil || !record.Receipt.Converged {
		t.Fatalf("the record is %+v, want a converged one naming no entry and holding its receipt", record)
	}
}

// foldsNames reports whether this filesystem answers one name for two spellings.
// It is asked of a throwaway directory beside the fixture vaults rather than of
// the vault itself, so the probe cannot be mistaken for a note.
func foldsNames(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "bn-fold-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("probe the filesystem: %v", err)
	}
	defer func() {
		if err := os.Remove(probe); err != nil {
			t.Fatalf("remove the probe: %v", err)
		}
	}()
	_, err := os.Lstat(filepath.Join(dir, "BN-FOLD-PROBE"))
	return err == nil
}
