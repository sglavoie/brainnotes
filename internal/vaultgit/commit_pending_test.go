package vaultgit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The vault holds names that break naive shell handling: spaces, non-ASCII,
// commas (which `update-index --cacheinfo`'s comma form truncates), glob
// characters (which a pathspec would expand), and a leading dash.
func TestCommitPendingHandlesAwkwardNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	names := []string{
		"Inbox/plain.md",
		"Inbox/with spaces.md",
		"Zettel/Ünïcodé — nöte.md",
		"Inbox/co,mma,file.md",
		"Inbox/Draft [2].md",
		"Inbox/TODO*.md",
		"Inbox/-leading-dash.md",
		"Inbox/quote'and\"quote.md",
		"attachments/diagram.png",
	}
	for i, name := range names {
		h.write(name, "body "+string(rune('a'+i))+"\n")
	}

	result := h.commitPending()
	if !result.Committed {
		t.Fatalf("nothing was committed: %+v", result)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", result.Skipped)
	}
	tracked := h.trackedSet()
	for _, name := range names {
		if !tracked[name] {
			t.Fatalf("%q is not in the history; tracked = %v", name, h.tracked())
		}
	}
	if result.Message != "brainnotes: autosave 2026-08-28T09:00:00Z" {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestCommitPendingRecordsModifications(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	first := h.head()

	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")
	result := h.commitPending()
	if !result.Committed || result.Parent != first {
		t.Fatalf("modification was not committed on top of the first commit: %+v", result)
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "two" {
		t.Fatalf("history holds %q, want the new content", got)
	}
}

// A snapshot with nothing to record is a success, not a failure, and must not
// add an empty commit to a history a person has to read.
func TestCommitPendingNoChangesIsNoCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	before := h.head()

	result := h.commitPending()
	if result.Committed {
		t.Fatalf("a clean vault produced a commit: %+v", result)
	}
	if h.head() != before {
		t.Fatal("a no-op snapshot moved the branch")
	}
}

// The tracked scope narrows what the engine adds on its own initiative. Local
// backups, regenerable caches, and in-flight transaction scratch never enter
// the history.
func TestCommitPendingHonoursScope(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.write("brainnotes.toml", "vault = \".\"\n")
	h.write(".bnotes/recall.json", "{}\n")
	h.write(".bnotes/index/notes.json", "{}\n")
	h.write("backups/2026-08-01/note.md", "old\n")
	h.write(".codegraph/graph.db", "binary\n")
	h.write("Inbox/.note-abc123.tmp", "half written\n")

	h.commitPending()
	tracked := h.trackedSet()
	for _, want := range []string{"Inbox/note.md", "brainnotes.toml", ".bnotes/recall.json"} {
		if !tracked[want] {
			t.Fatalf("%q should be tracked; tracked = %v", want, h.tracked())
		}
	}
	for _, unwanted := range []string{".bnotes/index/notes.json", "backups/2026-08-01/note.md", ".codegraph/graph.db", "Inbox/.note-abc123.tmp"} {
		if tracked[unwanted] {
			t.Fatalf("%q must not be tracked; tracked = %v", unwanted, h.tracked())
		}
	}
}

// --- Deletions ---------------------------------------------------------------

// External deletion is recorded and published while the parent keeps the bytes.
func TestExternalDeletionIsCommittedAndRecoverable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "add", "origin", bare)
	h.write("Inbox/keep.md", "keep")
	h.write("Inbox/precious.md", "irreplaceable")
	h.commitPending()
	parent := h.head()
	h.remove("Inbox/precious.md")
	h.write("Inbox/keep.md", "edited")
	result := h.commitPending()
	if !result.Committed || !slices.Contains(result.Deleted, "Inbox/precious.md") || h.trackedSet()["Inbox/precious.md"] {
		t.Fatalf("deletion not recorded: %+v", result)
	}
	if got := rawGit(t, h.root, "show", parent+":Inbox/precious.md"); got != "irreplaceable" {
		t.Fatal(got)
	}
	if _, err := h.mgr.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rawGit(t, bare, "rev-parse", "refs/heads/main"); got != h.head() {
		t.Fatal("deletion was not published")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("external deletion blocked the vault")
	}
}

func TestJournalRecordAuthorizesDeletion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/keep.md", "keep\n")
	h.write("Inbox/gone.md", "goodbye\n")
	h.commitPending()

	h.advance(time.Minute)
	h.record("bn rm Inbox/gone.md", []string{"Inbox/gone.md"}, func() { h.remove("Inbox/gone.md") })

	result := h.commitPending()
	if !result.Committed {
		t.Fatalf("an authorized deletion was not committed: %+v", result)
	}
	if !slices.Contains(result.Deleted, "Inbox/gone.md") {
		t.Fatalf("deleted = %v", result.Deleted)
	}
	tracked := h.trackedSet()
	if tracked["Inbox/gone.md"] {
		t.Fatal("the deleted note is still tracked")
	}
	if !tracked["Inbox/keep.md"] {
		t.Fatal("an unrelated note was dropped")
	}
	// The proof must land in the same commit as the deletion it explains, so the
	// history always contains its own justification.
	var manifests int
	for path := range tracked {
		if strings.HasPrefix(path, ".bnotes/journal/") && strings.HasSuffix(path, "/manifest.json") {
			manifests++
		}
	}
	if manifests != 1 {
		t.Fatalf("expected exactly one committed journal manifest, tracked = %v", h.tracked())
	}
}

func TestJournalRecordAuthorizesRename(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/old name.md", "stable content\n")
	h.commitPending()

	h.advance(time.Minute)
	h.record("bn rename", []string{"Inbox/old name.md", "Zettel/new näme.md"}, func() {
		h.write("Zettel/new näme.md", "stable content\n")
		h.remove("Inbox/old name.md")
	})

	result := h.commitPending()
	if !result.Committed {
		t.Fatalf("a rename was not committed: %+v", result)
	}
	tracked := h.trackedSet()
	if tracked["Inbox/old name.md"] {
		t.Fatal("the rename source is still tracked")
	}
	if !tracked["Zettel/new näme.md"] {
		t.Fatalf("the rename target is not tracked; tracked = %v", h.tracked())
	}
}

func TestRepeatedExternalDeletionNeedsNoNewJournal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("gone.md", "goodbye")
	h.commitPending()
	h.record("bn rm", []string{"gone.md"}, func() { h.remove("gone.md") })
	h.commitPending()
	h.write("gone.md", "goodbye")
	h.commitPending()
	h.remove("gone.md")
	if result := h.commitPending(); !result.Committed || !slices.Contains(result.Deleted, "gone.md") {
		t.Fatal(result)
	}
}

// soleManifest returns the path of the vault's only journal manifest.
func soleManifest(t *testing.T, h *testHarness) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(h.journalRoot(), "*", "manifest.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one manifest, got %v (%v)", matches, err)
	}
	return matches[0]
}

// External changes do not depend on the health or contents of an undo manifest.
func TestExternalDeletionDoesNotRequireJournalProof(t *testing.T) {
	t.Parallel()
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			h := newHarness(t)
			h.write("gone.md", "committed version")
			h.commitPending()
			h.write("gone.md", "edited since the commit")
			h.record("bn rm", []string{"gone.md"}, func() { h.remove("gone.md") })
			if malformed {
				if err := os.WriteFile(soleManifest(t, h), []byte(`{"id":"trunc`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if result := h.commitPending(); !result.Committed || !slices.Contains(result.Deleted, "gone.md") {
				t.Fatal(result)
			}
		})
	}
}

// --- Unreadable notes --------------------------------------------------------

// A vault may hold a note the user deliberately chmod'd to 0000. Refusing to
// snapshot the whole vault because of one would trade a real safety net for a
// completeness guarantee nobody asked for, so an unreadable *addition* is
// reported and skipped: nothing leaves the history by skipping it.
func TestUnreadableNoteIsSkippedNotFatal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0000 file, so this case cannot be provoked")
	}
	h.write("Inbox/readable.md", "fine\n")
	h.write("Inbox/locked.md", "unreadable\n")
	if err := os.Chmod(filepath.Join(h.root, "Inbox", "locked.md"), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(h.root, "Inbox", "locked.md"), 0o644) })

	result := h.commitPending()
	if !result.Committed {
		t.Fatalf("one unreadable note stopped the whole snapshot: %+v", result)
	}
	if !h.trackedSet()["Inbox/readable.md"] {
		t.Fatal("the readable note was not committed")
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Path != "Inbox/locked.md" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
	if result.Skipped[0].Reason == "" {
		t.Fatal("a skip must say why")
	}
}

// A skip suppresses every deletion in the same pass. A rename's new side is
// exactly the kind of file that can be unreadable, and committing the delete
// side of a pair whose add side was skipped would drop the content from the tip.
func TestSkippedPathDefersProvenDeletions(t *testing.T) {
	h := newHarness(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0000 file, so this case cannot be provoked")
	}
	h.write("Inbox/old name.md", "stable content\n")
	h.commitPending()

	h.advance(time.Minute)
	h.record("bn rename", []string{"Inbox/old name.md", "Zettel/new name.md"}, func() {
		h.write("Zettel/new name.md", "stable content\n")
		h.remove("Inbox/old name.md")
	})
	locked := filepath.Join(h.root, "Zettel", "new name.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	assertDeletionDeferredBySkip(t, h, h.commitPending())

	// Once the file is readable again the rename completes in one pass.
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	h.advance(time.Minute)
	final := h.commitPending()
	if !slices.Contains(final.Deleted, "Inbox/old name.md") {
		t.Fatalf("the deferred deletion never landed: %+v", final)
	}
	tracked := h.trackedSet()
	if tracked["Inbox/old name.md"] || !tracked["Zettel/new name.md"] {
		t.Fatalf("the rename did not complete; tracked = %v", h.tracked())
	}
}

// assertDeletionDeferredBySkip checks a pass that skipped the rename target
// held back the source's deletion, its proof, and any block.
func assertDeletionDeferredBySkip(t *testing.T, h *testHarness, result CommitResult) {
	t.Helper()
	if len(result.Skipped) == 0 {
		t.Fatalf("expected the unreadable rename target to be skipped: %+v", result)
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("a deletion was committed while its counterpart was skipped: %+v", result.Deleted)
	}
	if !slices.Contains(result.DeferredDeletions, "Inbox/old name.md") {
		t.Fatalf("deferred deletions = %v", result.DeferredDeletions)
	}
	if !h.trackedSet()["Inbox/old name.md"] {
		t.Fatal("the rename source left the history while its target was unreadable")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a deferred deletion is a proven one and must not latch a block")
	}
	// The proof waits with the deletion. Committing the record now would leave
	// the next pass with a deletion nothing explains, and the engine would block
	// the vault over a removal it had already approved.
	for _, path := range h.tracked() {
		if strings.HasPrefix(path, ".bnotes/journal/") {
			t.Fatalf("the proving journal record was committed without the deletion it proves: %q", path)
		}
	}
}

// --- Staging failures --------------------------------------------------------
