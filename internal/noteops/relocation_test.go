package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// setRelocationHook installs one relocation window hook for the length of the
// test. Tests using it must not call t.Parallel: the hooks are package state.
func setRelocationHook(t *testing.T, beforeCheck, beforeMove func(newPath string)) {
	t.Helper()
	previous := relocationTestHooks
	relocationTestHooks = relocationHooks{beforeCheck: beforeCheck, beforeMove: beforeMove}
	t.Cleanup(func() { relocationTestHooks = previous })
}

// createOnce returns a hook that writes body at path the first time it runs —
// the editor that saves a note at the destination after bn planned the move.
func createOnce(t *testing.T, path, body string) func(string) {
	t.Helper()
	done := false
	return func(string) {
		if done {
			return
		}
		done = true
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Errorf("create late destination %s: %v", path, err)
		}
	}
}

func openRelocationVault(t *testing.T, root string) (*vault.Vault, *config.Config) {
	t.Helper()
	cfg := &config.Config{Vault: root}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	return v, cfg
}

// assertNoJournalDirectories checks a refused operation left neither a finalized
// entry (which undo would offer) nor an unfinalized directory nothing can list.
func assertNoJournalDirectories(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(JournalRoot(root))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read journal root: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("journal entries left behind: %v", names)
	}
}

const (
	relocationSource = "---\ntitle: Old\n---\nOld body.\n"
	relocationRef    = "---\ntitle: Ref\n---\nSee [[Old]] and [[Inbox/Old]].\n"
	lateDestination  = "written by an editor after planning\n"
)

// TestRenameRefusesDestinationCreatedAfterPlanning covers both windows a rename
// plans across. A note created before the final check is refused before any
// backlink changes; one created while the backlinks are being rewritten is
// refused by the no-replace move, and the rewrites are put back. Either way the
// late note survives byte for byte, the source stays put, and no journal entry
// claims a change that did not happen.
func TestRenameRefusesDestinationCreatedAfterPlanning(t *testing.T) {
	for name, window := range map[string]string{
		"before the final check":     "check",
		"after the backlink rewrite": "move",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFrontmatterTestFile(t, root, "Old.md", relocationSource)
			writeFrontmatterTestFile(t, root, "Ref.md", relocationRef)
			v, cfg := openRelocationVault(t, root)
			dest := filepath.Join(root, "New.md")
			hook := createOnce(t, dest, lateDestination)
			if window == "check" {
				setRelocationHook(t, hook, nil)
			} else {
				setRelocationHook(t, nil, hook)
			}

			_, err := RenameNote(v, cfg, "Old.md", "New")
			if CodeOf(err) != CodeConflict {
				t.Fatalf("RenameNote err = %v (code %q), want %q", err, CodeOf(err), CodeConflict)
			}
			want := "rename: destination " + dest + " already exists (use --force to overwrite)"
			if MessageOf(err) != want {
				t.Fatalf("message = %q, want %q", MessageOf(err), want)
			}
			if got := readFrontmatterTestFile(t, root, "New.md"); got != lateDestination {
				t.Fatalf("late destination overwritten:\n%s", got)
			}
			if got := readFrontmatterTestFile(t, root, "Old.md"); got != relocationSource {
				t.Fatalf("source changed:\n%s", got)
			}
			if got := readFrontmatterTestFile(t, root, "Ref.md"); got != relocationRef {
				t.Fatalf("backlink rewrite was not rolled back:\n%s", got)
			}
			assertNoJournalDirectories(t, root)
		})
	}
}

// TestRenameForceReplacesDestinationCreatedAfterPlanning is the other side of
// the refusal: --force is a decision to overwrite, and it still holds when the
// occupant arrived late.
func TestRenameForceReplacesDestinationCreatedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Old.md", relocationSource)
	writeFrontmatterTestFile(t, root, "Ref.md", relocationRef)
	v, cfg := openRelocationVault(t, root)
	dest := filepath.Join(root, "New.md")
	setRelocationHook(t, nil, createOnce(t, dest, lateDestination))

	if _, err := RenameNote(v, cfg, "Old.md", "New", WithRenameForce(true)); err != nil {
		t.Fatalf("RenameNote --force: %v", err)
	}
	if got := readFrontmatterTestFile(t, root, "New.md"); got != relocationSource {
		t.Fatalf("destination = %q, want the renamed note", got)
	}
	if _, err := os.Stat(filepath.Join(root, "Old.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	if got := readFrontmatterTestFile(t, root, "Ref.md"); !strings.Contains(got, "[[New]]") {
		t.Fatalf("backlink not rewritten:\n%s", got)
	}
}

// TestMoveRefusesDestinationCreatedAfterRewrite is the move counterpart, at the
// narrower window: the destination folder's note appears after the
// folder-qualified links were rewritten.
func TestMoveRefusesDestinationCreatedAfterRewrite(t *testing.T) {
	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Inbox/Old.md", relocationSource)
	writeFrontmatterTestFile(t, root, "Ref.md", relocationRef)
	v, cfg := openRelocationVault(t, root)
	if err := os.MkdirAll(filepath.Join(root, "Archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "Archive", "Old.md")
	setRelocationHook(t, nil, createOnce(t, dest, lateDestination))

	_, err := MoveNote(v, cfg, "Inbox/Old.md", "Archive")
	want := "move: destination " + dest + " already exists (use --force to overwrite)"
	if CodeOf(err) != CodeConflict || MessageOf(err) != want {
		t.Fatalf("MoveNote err = %v (code %q), want %q", err, CodeOf(err), want)
	}
	if got := readFrontmatterTestFile(t, root, "Archive/Old.md"); got != lateDestination {
		t.Fatalf("late destination overwritten:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "Inbox/Old.md"); got != relocationSource {
		t.Fatalf("source changed:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "Ref.md"); got != relocationRef {
		t.Fatalf("backlink rewrite was not rolled back:\n%s", got)
	}
	assertNoJournalDirectories(t, root)
}

// TestBulkMoveRefusedItemKeepsTheBatchEntry checks compensation in a shared
// journal: the refused item puts its own rewrites back, and the batch entry
// still records the item that landed, so undo reverses exactly that.
func TestBulkMoveRefusedItemKeepsTheBatchEntry(t *testing.T) {
	const aBody = "---\ntitle: A\n---\nA.\n"
	const refBody = "---\ntitle: Ref\n---\nSee [[Inbox/A]] and [[Inbox/B]].\n"
	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Inbox/A.md", aBody)
	writeFrontmatterTestFile(t, root, "Inbox/B.md", "---\ntitle: B\n---\nB.\n")
	writeFrontmatterTestFile(t, root, "Ref.md", refBody)
	v, cfg := openRelocationVault(t, root)
	lateBeforeMoving(t, filepath.Join(root, "Archive", "B.md"))

	result, err := BulkMoveNotes(v, cfg,
		[]BulkSource{{Key: "a", RelPath: "Inbox/A.md"}, {Key: "b", RelPath: "Inbox/B.md"}},
		"Archive",
		WithBulkJournalOptions(journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-bulk", nil })),
	)
	if err != nil {
		t.Fatalf("BulkMoveNotes: %v", err)
	}
	if result.Changed != 1 || len(result.Failures) != 1 || result.Failures[0].Key != "b" {
		t.Fatalf("result = changed %d failures %#v, want A moved and B refused", result.Changed, result.Failures)
	}
	if result.Journal == nil || result.Journal.ID != "entry-bulk" {
		t.Fatalf("journal = %#v, want the batch entry", result.Journal)
	}
	assertFileBody(t, root, "Archive/B.md", lateDestination)
	assertFileBody(t, root, "Ref.md", "---\ntitle: Ref\n---\nSee [[Archive/A]] and [[Inbox/B]].\n")

	if _, err := Undo(root, "entry-bulk", UndoOptions{}); err != nil {
		t.Fatalf("undo batch: %v", err)
	}
	assertFileBody(t, root, "Ref.md", refBody)
	assertFileBody(t, root, "Inbox/A.md", aBody)
	assertFileBody(t, root, "Archive/B.md", lateDestination)
}

// lateBeforeMoving creates dest, in its own folder, the first time a relocation
// to dest reaches its move.
func lateBeforeMoving(t *testing.T, dest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	late := createOnce(t, dest, lateDestination)
	setRelocationHook(t, nil, func(newPath string) {
		if newPath == dest {
			late(newPath)
		}
	})
}

func assertFileBody(t *testing.T, root, rel, want string) {
	t.Helper()
	if got := readFrontmatterTestFile(t, root, rel); got != want {
		t.Fatalf("%s = %q, want %q", rel, got, want)
	}
}

// TestRenameFileCaseOnlyStillWorks keeps the pivot path honest now that the
// plain path refuses an existing destination: on a case-insensitive volume the
// new spelling "exists" because it is the note itself.
func TestRenameFileCaseOnlyStillWorks(t *testing.T) {
	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "note.md", relocationSource)
	from := filepath.Join(root, "note.md")
	to := filepath.Join(root, "Note.md")

	if err := RenameFile(root, from, to, RenameFileOptions{}); err != nil {
		t.Fatalf("RenameFile case-only: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 1 || names[0] != "Note.md" {
		t.Fatalf("directory holds %v, want [Note.md]", names)
	}
	if got := readFrontmatterTestFile(t, root, "Note.md"); got != relocationSource {
		t.Fatalf("content changed across the pivot:\n%s", got)
	}
}
