package vaultimport

import (
	"os"
	"sort"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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
	if result.Receipt == nil || result.ReceiptPath != "" {
		t.Fatalf("the unpublished convergence receipt is reported as %v at %q", result.Receipt, result.ReceiptPath)
	}
	record := importState(t, r).Applied[manifest.PackageID]
	if !record.Converged || record.Receipt == nil {
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
