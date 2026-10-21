package mutationmatrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/backup"
	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// TestBackupRestoreReportsAFailedCheckpointRelease proves a restore that landed
// still says the checkpoint record outlived it: the next writer would otherwise
// wait on a hold nobody owns, with nothing having said so.
func TestBackupRestoreReportsAFailedCheckpointRelease(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root from removing the record")
	}
	h := newHarness(t, nil)
	archive := createBackup(t, h)
	h.writeFile("Alpha.md", "changed after archive\n")
	h.rawGit("add", "Alpha.md")
	h.rawGit("commit", "-q", "-m", "newer content")
	record, err := checkpoint.RecordPath(h.root)
	if err != nil {
		t.Fatalf("RecordPath: %v", err)
	}
	recordDir := filepath.Dir(record)

	// Once content and history are restored, the record can no longer be removed.
	t.Cleanup(noteops.SetRestoreCrashHookForTest(func(point string) {
		if point == "history" {
			if err := os.Chmod(recordDir, 0o555); err != nil { // #nosec G302 -- read-only on purpose
				t.Errorf("make the record directory read-only: %v", err)
			}
		}
	}))
	t.Cleanup(func() { _ = os.Chmod(recordDir, 0o755) }) // #nosec G302 -- restores the fixture's own mode

	_, err = noteops.RestoreBackup(h.root, archive, backup.RestoreOptions{Wipe: true})
	if err == nil || !strings.Contains(err.Error(), "release the checkpoint") {
		t.Fatalf("RestoreBackup error = %v, want the checkpoint release failure", err)
	}
	if got := h.readFile("Alpha.md"); got != baseFiles["Alpha.md"] {
		t.Fatalf("restored content = %q, want the archived bytes", got)
	}
}
