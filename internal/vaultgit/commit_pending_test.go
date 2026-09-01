package vaultgit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The vault holds names that break naive shell handling: spaces, non-ASCII,
// commas (which `update-index --cacheinfo`'s comma form truncates), glob
// characters (which a pathspec would expand), and a leading dash.
func TestCommitPendingHandlesAwkwardNames(t *testing.T) {
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

// The invariant the whole package exists for: a note that disappeared without a
// journal record is never committed, never pushed, and latches a durable block.
func TestUnexplainedDeletionIsNeverCommittedOrPushed(t *testing.T) {
	h := newHarness(t)
	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "add", "origin", bare)

	h.write("Inbox/keep.md", "keep\n")
	h.write("Inbox/precious.md", "irreplaceable\n")
	h.commitPending()
	if _, err := h.mgr.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	safe := h.head()

	// Something outside BrainNotes removed the note: a sync client, a bad
	// `rm`, a merge gone wrong.
	h.remove("Inbox/precious.md")
	h.write("Inbox/keep.md", "keep, edited\n")

	_, err := h.mgr.CommitPending(context.Background())
	if !IsUnexplainedDeletion(err) {
		t.Fatalf("CommitPending = %v, want an unexplained-deletion failure", err)
	}
	if !strings.Contains(err.Error(), "Inbox/precious.md") {
		t.Fatalf("the error must name the missing note: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved despite an unexplained deletion")
	}
	if !h.trackedSet()["Inbox/precious.md"] {
		t.Fatal("the note left the history")
	}

	// Nothing new can reach the remote, because nothing new was committed.
	if _, err := h.mgr.Push(context.Background()); err != nil {
		t.Fatalf("Push after a refused snapshot: %v", err)
	}
	if remoteHead := rawGit(t, bare, "rev-parse", "refs/heads/main"); remoteHead != safe {
		t.Fatalf("remote head = %s, want %s", remoteHead, safe)
	}

	record, blocked := h.blockRecord()
	if !blocked {
		t.Fatal("an unexplained deletion must latch the block marker")
	}
	if record.Kind != KindUnexplainedDeletion || len(record.Recovery) == 0 {
		t.Fatalf("block record = %+v", record)
	}
	if !contains(record.Paths, "Inbox/precious.md") {
		t.Fatalf("block record paths = %v", record.Paths)
	}
	// The marker names paths and instructions, never note contents.
	for _, step := range record.Recovery {
		if strings.Contains(step, "irreplaceable") {
			t.Fatalf("the block record leaked note content: %q", step)
		}
	}
	// The refusal happened while the snapshot was being planned, so no intent was
	// ever written; and a marker that did land takes the record's place in any
	// case. Either way nothing is left for recovery to adjudicate.
	if _, present := h.mgr.Interrupted(); present {
		t.Fatal("a latched refusal left an intent record behind")
	}
}

func TestJournalRecordAuthorizesDeletion(t *testing.T) {
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
	if !contains(result.Deleted, "Inbox/gone.md") {
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

// A journal record proves a transition once. Once it is in the history it can no
// longer authorize anything, or deleting a note, restoring it, and deleting it
// again by hand would pass as intended.
func TestCommittedJournalRecordCannotAuthorizeALaterDeletion(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/gone.md", "goodbye\n")
	h.commitPending()

	h.advance(time.Minute)
	h.record("bn rm Inbox/gone.md", []string{"Inbox/gone.md"}, func() { h.remove("Inbox/gone.md") })
	h.commitPending()

	// The same note comes back with the same content and then vanishes again,
	// this time with nothing to explain it.
	h.advance(time.Minute)
	h.write("Inbox/gone.md", "goodbye\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	h.remove("Inbox/gone.md")
	_, err := h.mgr.CommitPending(context.Background())
	if !IsUnexplainedDeletion(err) {
		t.Fatalf("CommitPending = %v, want an unexplained-deletion failure", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved on a deletion proven only by an already-committed record")
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

// A journal record whose pre-image hash is not what the history holds proves
// nothing about the file that is actually being removed. Matching the exact
// content is what makes the proof specific rather than a blanket permission
// slip for that path.
func TestJournalRecordWithWrongHashDoesNotAuthorize(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/gone.md", "version one\n")
	h.commitPending()
	safe := h.head()

	h.record("bn rm Inbox/gone.md", []string{"Inbox/gone.md"}, func() { h.remove("Inbox/gone.md") })

	manifest := soleManifest(t, h)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	files, ok := doc["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("unexpected manifest shape: %v", doc["files"])
	}
	before := files[0].(map[string]any)["before"].(map[string]any)
	before["sha256"] = strings.Repeat("0", 64)
	patched, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(manifest, patched, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err = h.mgr.CommitPending(context.Background())
	if !IsUnexplainedDeletion(err) {
		t.Fatalf("CommitPending = %v, want an unexplained-deletion failure", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved on a deletion the journal does not prove")
	}
	if !h.trackedSet()["Inbox/gone.md"] {
		t.Fatal("the note left the history")
	}
}

// A manifest that does not load — truncated by a crash, half-written — proves
// nothing, so the deletion it would have explained stays unexplained. The
// conservative outcome is the one that keeps the note.
func TestUnreadableJournalRecordDoesNotAuthorize(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/gone.md", "version one\n")
	h.commitPending()

	h.record("bn rm Inbox/gone.md", []string{"Inbox/gone.md"}, func() { h.remove("Inbox/gone.md") })
	if err := os.WriteFile(soleManifest(t, h), []byte(`{"id":"trunc`), 0o644); err != nil {
		t.Fatalf("truncate manifest: %v", err)
	}

	_, err := h.mgr.CommitPending(context.Background())
	if !IsUnexplainedDeletion(err) {
		t.Fatalf("CommitPending = %v, want an unexplained-deletion failure", err)
	}
	if !h.trackedSet()["Inbox/gone.md"] {
		t.Fatal("the note left the history")
	}
}

// --- Unreadable notes --------------------------------------------------------

// A vault may hold a note the user deliberately chmod'd to 0000. Refusing to
// snapshot the whole vault because of one would trade a real safety net for a
// completeness guarantee nobody asked for, so an unreadable *addition* is
// reported and skipped: nothing leaves the history by skipping it.
func TestUnreadableNoteIsSkippedNotFatal(t *testing.T) {
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

	result := h.commitPending()
	if len(result.Skipped) == 0 {
		t.Fatalf("expected the unreadable rename target to be skipped: %+v", result)
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("a deletion was committed while its counterpart was skipped: %+v", result.Deleted)
	}
	if !contains(result.DeferredDeletions, "Inbox/old name.md") {
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

	// Once the file is readable again the rename completes in one pass.
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	h.advance(time.Minute)
	final := h.commitPending()
	if !contains(final.Deleted, "Inbox/old name.md") {
		t.Fatalf("the deferred deletion never landed: %+v", final)
	}
	tracked := h.trackedSet()
	if tracked["Inbox/old name.md"] || !tracked["Zettel/new name.md"] {
		t.Fatalf("the rename did not complete; tracked = %v", h.tracked())
	}
}

// --- Staging failures --------------------------------------------------------
