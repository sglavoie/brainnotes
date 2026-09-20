package mutationmatrix

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/backup"
	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/restorestate"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

const (
	restoreCrashRootEnv    = "BN_TEST_RESTORE_CRASH_ROOT"
	restoreCrashArchiveEnv = "BN_TEST_RESTORE_CRASH_ARCHIVE"
	restoreCrashPointEnv   = "BN_TEST_RESTORE_CRASH_POINT"
	restoreCrashWipeEnv    = "BN_TEST_RESTORE_CRASH_WIPE"
)

func TestBackupRestoreCrashHelper(t *testing.T) {
	root, archive, point := os.Getenv(restoreCrashRootEnv), os.Getenv(restoreCrashArchiveEnv), os.Getenv(restoreCrashPointEnv)
	if root == "" || archive == "" || point == "" {
		t.Skip("helper")
	}
	noteops.SetRestoreCrashHookForTest(func(got string) {
		if got == point {
			os.Exit(23)
		}
	})
	wipe := os.Getenv(restoreCrashWipeEnv) != "0"
	if _, err := noteops.RestoreBackup(root, archive, backup.RestoreOptions{Wipe: wipe}); err != nil {
		t.Fatalf("RestoreBackup helper: %v", err)
	}
	t.Fatal("restore did not reach crash point")
}

func TestBackupRestoreRecoversProcessDeathAcrossContentAndHistory(t *testing.T) {
	for _, point := range []string{"content", "history"} {
		t.Run(point, func(t *testing.T) { assertRestoreCrashRecovery(t, point) })
	}
}

func TestBackupRestoreResumesAfterRestrictiveModesBeforeHistoryPhase(t *testing.T) {
	for _, wipe := range []bool{false, true} {
		t.Run(restoreModeName(wipe), func(t *testing.T) {
			assertRestrictiveRestoreRecovery(t, wipe)
		})
	}
}

func assertRestrictiveRestoreRecovery(t *testing.T, wipe bool) {
	t.Helper()
	h := newHarness(t, nil)
	archive := createRestrictiveRestoreArchive(t)
	h.writeFile("Alpha.md", "changed after archive\n")
	h.rawGit("add", "Alpha.md")
	h.rawGit("commit", "-q", "-m", "newer content")
	before := h.commitCount()

	cmd := exec.Command(os.Args[0], "-test.run=^TestBackupRestoreCrashHelper$")
	cmd.Env = append(os.Environ(), restoreCrashRootEnv+"="+h.root, restoreCrashArchiveEnv+"="+archive, restoreCrashPointEnv+"=content-applied", restoreCrashWipeEnv+"="+restoreWipeValue(wipe))
	assertRestoreHelperExit(t, cmd.Run(), "content-applied")
	locked := filepath.Join(h.root, "Locked")
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if info, err := os.Stat(locked); err != nil || info.Mode().Perm() != 0 {
		t.Fatalf("mode after interrupted restore = %v, err=%v", info, err)
	}
	assertPartialRestoreRefusesCheckpoint(t, h.root)
	assertRestrictiveRestoreResume(t, h, archive, locked, before, wipe)
}

func restoreModeName(wipe bool) string {
	if wipe {
		return "wipe"
	}
	return "overlay"
}

func restoreWipeValue(wipe bool) string {
	if wipe {
		return "1"
	}
	return "0"
}

func assertRestrictiveRestoreResume(t *testing.T, h *harness, archive, locked string, before int, wipe bool) {
	t.Helper()
	result, err := noteops.RestoreBackup(h.root, archive, backup.RestoreOptions{Wipe: wipe})
	if err != nil {
		t.Fatalf("resume restrictive restore: %v", err)
	}
	if result == nil || result.ArchivePath != archive {
		t.Fatalf("resume result = %+v", result)
	}
	if got := h.readFile("Alpha.md"); got != baseFiles["Alpha.md"] {
		t.Fatalf("resumed content = %q", got)
	}
	if got := h.commitCount(); got != before+1 {
		t.Fatalf("commit count %d -> %d, want one restore receipt", before, got)
	}
	if pending, err := restorestate.Pending(h.root); err != nil || pending {
		t.Fatalf("pending restore after recovery = %v, err=%v", pending, err)
	}
	if info, err := os.Stat(locked); err != nil || info.Mode().Perm() != 0 {
		t.Fatalf("final restrictive mode = %v, err=%v", info, err)
	}
}

func createRestrictiveRestoreArchive(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restrictive.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	entries := []struct {
		header tar.Header
		body   string
	}{
		{header: tar.Header{Name: "Alpha.md", Mode: 0o644, Size: int64(len(baseFiles["Alpha.md"])), Typeflag: tar.TypeReg}, body: baseFiles["Alpha.md"]},
		{header: tar.Header{Name: "Locked/", Mode: 0o000, Typeflag: tar.TypeDir}},
		{header: tar.Header{Name: "Locked/inside.md", Mode: 0o644, Size: int64(len("inside\n")), Typeflag: tar.TypeReg}, body: "inside\n"},
	}
	for index := range entries {
		if err := tw.WriteHeader(&entries[index].header); err != nil {
			t.Fatal(err)
		}
		if entries[index].body != "" {
			if _, err := tw.Write([]byte(entries[index].body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRestoreCrashRecovery(t *testing.T, point string) {
	h := newHarness(t, nil)
	archive := createBackup(t, h)
	h.writeFile("Alpha.md", "changed after archive\n")
	h.rawGit("add", "Alpha.md")
	h.rawGit("commit", "-q", "-m", "newer content")
	before := h.commitCount()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackupRestoreCrashHelper$")
	cmd.Env = append(os.Environ(), restoreCrashRootEnv+"="+h.root, restoreCrashArchiveEnv+"="+archive, restoreCrashPointEnv+"="+point)
	assertRestoreHelperExit(t, cmd.Run(), point)
	assertPartialRestoreRefusesCheckpoint(t, h.root)
	assertPartialRestoreRefusesWriter(t, h)
	result, err := noteops.RestoreBackup(h.root, archive, backup.RestoreOptions{Wipe: true})
	if err != nil {
		t.Fatalf("resume restore: %v", err)
	}
	if result == nil || result.ArchivePath != archive {
		t.Fatalf("resume result = %+v", result)
	}
	if got := h.readFile("Alpha.md"); got != baseFiles["Alpha.md"] {
		t.Fatalf("resumed content = %q", got)
	}
	if got := h.commitCount(); got != before+1 {
		t.Fatalf("commit count %d -> %d, want one restore receipt", before, got)
	}
	if pending, err := restorestate.Pending(h.root); err != nil || pending {
		t.Fatalf("pending restore after recovery = %v, err=%v", pending, err)
	}
}

func assertPartialRestoreRefusesWriter(t *testing.T, h *harness) {
	beforeContent, beforeHead := h.readFile("Alpha.md"), h.head()
	if err := setStatus(t, h, "Alpha.md", "done"); err == nil {
		t.Fatal("ordinary writer entered a pending restore")
	}
	if got := h.readFile("Alpha.md"); got != beforeContent {
		t.Fatalf("refused writer changed content: %q", got)
	}
	if got := h.head(); got != beforeHead {
		t.Fatalf("refused writer changed history: %s -> %s", beforeHead, got)
	}
}

func assertRestoreHelperExit(t *testing.T, err error, point string) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("restore helper at %s = %v, want exit 23", point, err)
	}
}

func assertPartialRestoreRefusesCheckpoint(t *testing.T, root string) {
	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "must refuse partial restore"})
	if held != nil {
		_ = held.Release()
	}
	if !errors.Is(err, checkpoint.ErrRestorePending) {
		t.Fatalf("ordinary checkpoint after death = %v, want ErrRestorePending", err)
	}
}

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
