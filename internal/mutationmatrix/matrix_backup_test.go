package mutationmatrix

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/backup"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// TestBackupRestoreIsPreflightedAndSnapshotted covers the one writer the matrix
// cannot describe in its own terms.
//
// A backup restore is wholesale by definition: it puts an archive over the vault
// and its removals are authorized by the operator running the command, not by an
// undo manifest. So it is the single documented exception that snapshots the
// working tree rather than exact paths — which means the two things worth
// proving about it are that the refusal still happens before anything is
// extracted, and that a successful restore is still exactly one commit.
func TestBackupRestoreIsPreflightedAndSnapshotted(t *testing.T) {
	h := newHarness(t, nil)

	archive := createBackup(t, h)
	h.writeFile("Alpha.md", "---\nstatus: done\ntitle: Alpha\n---\n\nAlpha body, edited.\n")
	h.rawGit("add", "Alpha.md")
	h.rawGit("commit", "-q", "-m", "user edit")
	before := h.commitCount()
	original := baseFiles["Alpha.md"]

	result, err := noteops.RestoreBackup(h.root, archive, backupRestoreOptions())
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if result == nil {
		t.Fatal("RestoreBackup returned no result")
	}
	if got := h.readFile("Alpha.md"); got != original {
		t.Fatalf("the restore did not put the archived note back:\n%s", got)
	}
	if got := h.commitCount(); got != before+1 {
		t.Fatalf("commit count %d -> %d, want exactly one new commit", before, got)
	}
	committed, ok := h.show("HEAD", "Alpha.md")
	if !ok || committed != original {
		t.Fatalf("the snapshot does not hold the restored note (present=%v):\n%s", ok, committed)
	}

	// The refusal half: a blocked vault must not be extracted over.
	h.breakSnapshots()
	if _, err := noteops.SetFrontmatter(h.root, "Alpha.md", statusMutations(t, h, "done"),
		&h.cfg.Frontmatter, noteops.WithFrontmatterCommand("bn set")); !noteops.IsSnapshotFailure(err) {
		t.Fatalf("failed to latch a block: %v", err)
	}
	h.repairSnapshots()
	vaultgit.ForgetShared(h.root)

	blockedTree := h.tree()
	_, err = noteops.RestoreBackup(h.root, archive, backupRestoreOptions())
	if err == nil {
		t.Fatal("a restore was allowed to run on a blocked vault")
	}
	if noteops.IsSnapshotFailure(err) {
		t.Fatalf("the refusal must precede the extraction, not follow it: %v", err)
	}
	if !vaultgit.IsBlocked(err) {
		t.Fatalf("the refusal is %v, want it to name the latched block", err)
	}
	if got := h.tree(); !reflect.DeepEqual(got, blockedTree) {
		t.Fatalf("a refused restore changed the vault:\nbefore %v\nafter  %v", blockedTree, got)
	}
}

// TestBackupRestoreWipeKeepsTheHistoryItCommitsInto is the one case where the
// most destructive command bn has meets the layer that is supposed to make
// destruction survivable.
//
// `bn restore --wipe` empties the vault before it extracts. An archive holds
// neither `backups/` nor `.git/` — both are excluded when it is written — so a
// wipe that removed either would delete the only copy of something and no
// archive anywhere could put it back. For `.git/` that is the entire content
// history, which is also what the restore is about to commit into: wiping it
// would mean the operation destroys its own receipt and the record of every
// version the wipe just discarded.
//
// So the assertion that matters is not that the working tree came back. It is
// that the note the wipe removed is still reachable in the history afterwards.
func TestBackupRestoreWipeKeepsTheHistoryItCommitsInto(t *testing.T) {
	h := newHarness(t, nil)
	archive := createBackup(t, h)

	// Delta is in the vault and in its history but not in the archive, so it is
	// what the wipe removes and therefore the deletion the restore has to record.
	const delta = "Delta.md"
	deltaBody := "---\nstatus: draft\ntitle: Delta\n---\n\nDelta body.\n"
	h.writeFile(delta, deltaBody)
	h.rawGit("add", delta)
	h.rawGit("commit", "-q", "-m", "user adds Delta")
	before := h.commitCount()

	result, err := noteops.RestoreBackup(h.root, archive, backup.RestoreOptions{Wipe: true})
	if err != nil {
		t.Fatalf("RestoreBackup --wipe: %v", err)
	}
	if result == nil || result.Mode != "wipe" {
		t.Fatalf("RestoreBackup returned %+v, want a wipe result", result)
	}

	if !h.exists(".git") {
		t.Fatal("the wipe removed the vault's Git repository")
	}
	if h.exists(delta) {
		t.Fatalf("%s is not in the archive, so the wipe should have removed it", delta)
	}
	if got := h.readFile("Alpha.md"); got != baseFiles["Alpha.md"] {
		t.Fatalf("the restore did not put the archived note back:\n%s", got)
	}

	if got := h.commitCount(); got != before+1 {
		t.Fatalf("commit count %d -> %d, want exactly one new commit", before, got)
	}
	if got := h.changedPaths("HEAD"); !reflect.DeepEqual(got, []string{delta}) {
		t.Fatalf("the restore commit touched %v, want only %q", got, delta)
	}
	if _, ok := h.show("HEAD", delta); ok {
		t.Fatalf("%s is still in the snapshot the restore took", delta)
	}

	// The point of preserving `.git`: what the wipe discarded is still there to
	// be recovered from.
	if committed, ok := h.show("HEAD~1", delta); !ok || committed != deltaBody {
		t.Fatalf("the wiped note is not recoverable from the history (present=%v):\n%s", ok, committed)
	}
}

// createBackup archives the vault as it stands. The archive lands under
// <vault>/backups, which the history excludes wholesale, so making one is not
// itself a mutation the engine records.
func createBackup(t *testing.T, h *harness) string {
	t.Helper()
	result, err := backup.Create(h.root, backup.CreateOptions{
		Now: func() time.Time { return time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("backup.Create: %v", err)
	}
	return result.ArchivePath
}

// backupRestoreOptions overlays rather than wipes, which keeps this case about
// preflight and the shape of the snapshot. What a wipe does to the history it
// commits into is its own case above.
func backupRestoreOptions() backup.RestoreOptions {
	return backup.RestoreOptions{}
}
