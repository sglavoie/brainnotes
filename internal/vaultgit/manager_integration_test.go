package vaultgit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- Opening -----------------------------------------------------------------

// A vault without Git is not a broken vault. It reports ErrUnmanaged so callers
// carry on with no content history rather than treating it as damage.
func TestOpenUnmanagedVault(t *testing.T) {
	_, err := Open(context.Background(), t.TempDir(), Options{})
	if !IsUnmanaged(err) {
		t.Fatalf("Open on a non-Git vault = %v, want ErrUnmanaged", err)
	}
}

func TestOpenRejectsGitFile(t *testing.T) {
	requireGit(t)
	isolateHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	_, err := Open(context.Background(), root, Options{})
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Open with a .git file = %v, want a repository-state failure", err)
	}
	if IsUnmanaged(err) {
		t.Fatal("a linked worktree must not be reported as an unmanaged vault")
	}
}

// A `.git` directory that is not a repository is the partially-initialized case:
// the user believes they have history and must be told plainly that they do not.
func TestOpenRejectsMalformedRepository(t *testing.T) {
	requireGit(t)
	isolateHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Open(context.Background(), root, Options{})
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Open on a malformed repository = %v, want a repository-state failure", err)
	}
}

// A vault that is merely a subdirectory of some larger repository — a home
// directory under version control, say — is not managed. Every path the engine
// computed would otherwise be relative to the wrong root, and it would commit
// the vault into a history that is not the vault's.
func TestOpenIgnoresRepositoryAboveTheVault(t *testing.T) {
	h := newHarness(t)
	nested := filepath.Join(h.root, "sub", "vault")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Open(context.Background(), nested, Options{})
	if !IsUnmanaged(err) {
		t.Fatalf("Open below the repository root = %v, want ErrUnmanaged", err)
	}
}

func TestOpenRejectsDetachedHead(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	rawGit(t, h.root, "checkout", "--detach", "-q", "HEAD")

	_, err := Open(context.Background(), h.root, Options{})
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Open on a detached HEAD = %v, want a repository-state failure", err)
	}
}

// --- Additions and modifications ---------------------------------------------

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

// failingStageShim stands in for a git whose `update-index --add` refuses. The
// nth such call is let through, so one test can provoke "every path failed" and
// another "some paths failed" with the same seam; pass 0 to fail every call.
//
// It is the only way to reach this failure from outside. A repository whose
// index cannot be written is what produced it in the field, and arranging that
// by hand means making the engine's own state directory unwritable at the exact
// instant staging runs — which breaks the block marker the assertions need to
// read afterwards.
func failingStageShim(t *testing.T, letThrough int) string {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "stage-calls")
	return gitShim(t, `
if [ "$1" = "update-index" ] && [ "$2" = "--add" ]; then
  count=$(cat `+shellQuote(counter)+` 2>/dev/null || echo 0)
  count=$((count + 1))
  printf '%s' "$count" > `+shellQuote(counter)+`
  if [ "$count" -ne `+shellQuote(strconv.Itoa(letThrough))+` ]; then
    cat > /dev/null
    echo "simulated index write failure" >&2
    exit 128
  fi
fi`)
}

// A repository that will not stage anything is not an empty vault. Turning every
// `update-index` failure into a skip made this exact case — an unwritable index,
// hit on the first call and therefore on every path — look like a snapshot with
// nothing to record: no commit, no error, and a `bn` command that printed
// "updated" and exited 0 while the history stayed where it was.
func TestCommitJournalEntryStagingFailureOfEveryPathLatchesABlock(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	entry := h.record("bn set", []string{"Inbox/note.md"}, func() { h.write("Inbox/note.md", "two\n") })

	broken := h.open(Options{GitExecutable: failingStageShim(t, 0)})
	result, err := broken.CommitJournalEntry(context.Background(), entry.ID, "set")
	if err == nil {
		t.Fatalf("a repository that staged nothing reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitJournalEntry = %v, want a commit failure", err)
	}
	if result.Committed {
		t.Fatalf("a failed snapshot reported a commit: %+v", result)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although nothing could be staged")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "one" {
		t.Fatalf("history holds %q, want the pre-mutation content", got)
	}

	record, blocked := h.blockRecord()
	if !blocked || record.Kind != KindCommitFailed {
		t.Fatalf("a staging failure must latch an explained block: %+v", record)
	}
	// Fail-closed means the refusal outlives the process that saw it.
	restarted := h.open(Options{})
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight after a staging failure = %v, want a blocked failure", err)
	}
}

// Staging some of an exact snapshot's paths is worse than staging none: the tree
// would hold the journal record without the note it describes. Nothing is
// committed, and the vault is blocked rather than left looking healthy.
func TestCommitJournalEntryPartialStagingFailureLatchesABlock(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	entry := h.record("bn set", []string{"Inbox/note.md"}, func() { h.write("Inbox/note.md", "two\n") })

	// Call 1 is the whole-plan batch and call 2 is the first path of the per-path
	// fallback, which is a journal record file: the plan is sorted and `.bnotes/`
	// sorts before `Inbox/`. So part of the record reaches the index and the note
	// it describes never does — the one shape a snapshot must never commit.
	broken := h.open(Options{GitExecutable: failingStageShim(t, 2)})
	result, err := broken.CommitJournalEntry(context.Background(), entry.ID, "set")
	if err == nil {
		t.Fatalf("a partially staged snapshot reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitJournalEntry = %v, want a commit failure", err)
	}
	if !strings.Contains(err.Error(), "could not be staged") {
		t.Fatalf("the failure must say which path stopped it: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved on a partially staged snapshot")
	}
	tracked := h.trackedSet()
	for path := range tracked {
		if strings.HasPrefix(path, ".bnotes/journal/") {
			t.Fatalf("a journal record was committed without the note it describes: %q", path)
		}
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "one" {
		t.Fatalf("history holds %q, want the pre-mutation content", got)
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("a partial staging failure must latch a block")
	}
}

// The external-change path is allowed to skip a note it cannot read. It is not
// allowed to skip a note it can read perfectly well, because a git that refuses
// one of those is failing for a reason that applies to the whole vault.
func TestCommitPendingRepositoryWideStagingFailureIsFatal(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	h.advance(time.Minute)
	h.write("Inbox/second.md", "two\n")

	broken := h.open(Options{GitExecutable: failingStageShim(t, 0)})
	result, err := broken.CommitPending(context.Background())
	if err == nil {
		t.Fatalf("a repository that staged nothing reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPending = %v, want a commit failure", err)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("a repository-wide failure must not be reported as skipped paths: %+v", result.Skipped)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although nothing could be staged")
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("a repository-wide staging failure must latch a block")
	}
}

// A caller that names a path is asking for that path. When the vault will not
// hand its content over, the honest answer is a refusal — not a success that
// omits it. The same unreadable file is a tolerated skip on the external path,
// and TestUnreadableNoteIsSkippedNotFatal is what pins that half down.
func TestCommitPathsFailsClosedOnAnUnreadableRequiredPath(t *testing.T) {
	h := newHarness(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0000 file, so this case cannot be provoked")
	}
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	state := filepath.Join(h.root, ".bnotes", "recall.json")
	h.write(".bnotes/recall.json", "{}\n")
	if err := os.Chmod(state, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0o644) })

	h.advance(time.Minute)
	result, err := h.mgr.CommitPaths(context.Background(), "recall-state", []string{state})
	if err == nil {
		t.Fatalf("an unrecordable named path reported success: %+v", result)
	}
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPaths = %v, want a commit failure", err)
	}
	if !contains(result.Unrecorded(), ".bnotes/recall.json") && !strings.Contains(err.Error(), ".bnotes/recall.json") {
		t.Fatalf("the failure must name the path it could not record: %v", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved although the named path was never staged")
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("an unrecordable named path must latch a block")
	}
}

// --- Preflight ---------------------------------------------------------------

func TestPreflightCleanVault(t *testing.T) {
	h := newHarness(t)
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight on an unborn branch: %v", err)
	}
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	if err := h.mgr.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight on a clean vault: %v", err)
	}
}

// Preflight is the gate a mutating command calls before touching a note, so it
// has to refuse a vault with a pending deletion nothing explains — otherwise
// the next snapshot would make that disappearance permanent.
func TestPreflightRefusesUnexplainedDeletion(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	h.remove("Inbox/note.md")

	err := h.mgr.Preflight(context.Background())
	if !IsUnexplainedDeletion(err) {
		t.Fatalf("Preflight = %v, want an unexplained-deletion failure", err)
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("Preflight must latch the block marker")
	}
}

func TestPreflightRefusesInProgressGitOperation(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	if err := os.WriteFile(filepath.Join(h.root, ".git", "MERGE_HEAD"), []byte(h.head()+"\n"), 0o644); err != nil {
		t.Fatalf("write MERGE_HEAD: %v", err)
	}

	err := h.mgr.Preflight(context.Background())
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Preflight during a merge = %v, want a repository-state failure", err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("an in-progress merge is the user's to finish and must not latch a block")
	}
}

// Two BrainNotes processes on one vault must not interleave inside a snapshot.
// The loser is told it is busy, which is retryable and says nothing about the
// vault's health.
func TestConcurrentManagersSerialize(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	other := h.open(Options{})
	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if err := other.Preflight(context.Background()); !IsBusy(err) {
		release()
		t.Fatalf("Preflight while another process holds the lock = %v, want busy", err)
	}
	if _, err := other.CommitPending(context.Background()); !IsBusy(err) {
		release()
		t.Fatalf("CommitPending while another process holds the lock = %v, want busy", err)
	}
	if Blocks(err) {
		release()
		t.Fatal("a busy vault must not latch a block")
	}

	release()
	if err := other.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight after the lock was released: %v", err)
	}
}

// The mutation lock and the push lock are independent, so a push stuck on an
// unreachable network cannot delay a note save.
func TestPushLockDoesNotBlockSnapshots(t *testing.T) {
	h := newHarness(t)
	release, err := h.mgr.acquire(pushLockName, "test")
	if err != nil {
		t.Fatalf("acquire push lock: %v", err)
	}
	defer release()

	h.write("Inbox/note.md", "one\n")
	if _, err := h.mgr.CommitPending(context.Background()); err != nil {
		t.Fatalf("CommitPending while a push is in flight: %v", err)
	}
}

// --- Journal-entry commits ---------------------------------------------------

func TestCommitJournalEntryStagesOnlyItsOwnPaths(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/tracked.md", "one\n")
	h.commitPending()

	h.advance(time.Minute)
	// The user is editing an unrelated note in another window while `bn` runs.
	h.write("Inbox/unrelated.md", "work in progress\n")
	entry := h.record("bn set", []string{"Inbox/tracked.md"}, func() {
		h.write("Inbox/tracked.md", "two\n")
	})

	result, err := h.mgr.CommitJournalEntry(context.Background(), entry.ID, "set")
	if err != nil {
		t.Fatalf("CommitJournalEntry: %v", err)
	}
	if !result.Committed {
		t.Fatalf("nothing was committed: %+v", result)
	}
	if result.Message != "brainnotes: set ["+entry.ID+"]" {
		t.Fatalf("message = %q", result.Message)
	}
	tracked := h.trackedSet()
	if !tracked["Inbox/tracked.md"] {
		t.Fatal("the mutated note is not tracked")
	}
	if tracked["Inbox/unrelated.md"] {
		t.Fatal("an unrelated in-progress note was swept into the commit")
	}
	if !tracked[".bnotes/journal/"+entry.ID+"/manifest.json"] {
		t.Fatalf("the journal record was not committed; tracked = %v", h.tracked())
	}
}

func TestCommitJournalEntryDeletion(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/gone.md", "goodbye\n")
	h.commitPending()

	h.advance(time.Minute)
	entry := h.record("bn rm", []string{"Inbox/gone.md"}, func() { h.remove("Inbox/gone.md") })

	result, err := h.mgr.CommitJournalEntry(context.Background(), entry.ID, "remove")
	if err != nil {
		t.Fatalf("CommitJournalEntry: %v", err)
	}
	if !contains(result.Deleted, "Inbox/gone.md") {
		t.Fatalf("deleted = %v", result.Deleted)
	}
	if h.trackedSet()["Inbox/gone.md"] {
		t.Fatal("the deleted note is still tracked")
	}
}

// If the world moved between the mutation and the snapshot, the request is
// stale rather than dangerous: nothing is committed, nothing is blocked, and the
// next pending-change scan picks up whatever the vault actually holds.
func TestCommitJournalEntryRefusesStaleRequest(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	entry := h.record("bn set", []string{"Inbox/note.md"}, func() { h.write("Inbox/note.md", "two\n") })
	// Someone edited the note again before the snapshot ran.
	h.write("Inbox/note.md", "three\n")

	_, err := h.mgr.CommitJournalEntry(context.Background(), entry.ID, "set")
	if KindOf(err) != KindStaleRequest {
		t.Fatalf("CommitJournalEntry = %v, want a stale-request failure", err)
	}
	if h.head() != safe {
		t.Fatal("a stale request moved the branch")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a stale request must not latch a block")
	}

	// The ordinary pending-change path still records the current content.
	if !h.commitPending().Committed {
		t.Fatal("the following snapshot recorded nothing")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "three" {
		t.Fatalf("history holds %q, want the current content", got)
	}
}

func TestCommitJournalEntryRejectsBadEntryID(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"", "../escape", "entry/1"} {
		if _, err := h.mgr.CommitJournalEntry(context.Background(), id, "set"); KindOf(err) != KindStaleRequest {
			t.Fatalf("CommitJournalEntry(%q) = %v, want a stale-request failure", id, err)
		}
	}
}

// --- The user's index --------------------------------------------------------

// An engine commit must leave `git status` clean, or the user sees what looks
// like a staged revert and is invited to "fix" it with a checkout that would
// discard the snapshot.
func TestSnapshotLeavesUserStatusClean(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.write("Inbox/other.md", "two\n")
	h.commitPending()
	if out := rawGit(t, h.root, "status", "--porcelain"); out != "" {
		t.Fatalf("status after the first snapshot is not clean:\n%s", out)
	}

	h.advance(time.Minute)
	h.write("Inbox/note.md", "one, edited\n")
	h.commitPending()
	if out := rawGit(t, h.root, "status", "--porcelain"); out != "" {
		t.Fatalf("status after a later snapshot is not clean:\n%s", out)
	}

	h.advance(time.Minute)
	h.record("bn rm", []string{"Inbox/other.md"}, func() { h.remove("Inbox/other.md") })
	h.commitPending()
	if out := rawGit(t, h.root, "status", "--porcelain"); out != "" {
		t.Fatalf("status after an authorized deletion is not clean:\n%s", out)
	}
}

// Whatever the user deliberately staged is theirs. The engine commits the
// working tree through a private index and never touches an entry the user has
// moved away from HEAD.
func TestUserStagedWorkSurvivesASnapshot(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.write("Inbox/staged.md", "v1\n")
	h.commitPending()

	// The user stages a version of their own, then keeps editing on disk.
	h.write("Inbox/staged.md", "v2 staged by hand\n")
	rawGit(t, h.root, "add", "Inbox/staged.md")
	stagedOID := rawGit(t, h.root, "rev-parse", ":Inbox/staged.md")
	h.write("Inbox/staged.md", "v3 on disk\n")

	h.advance(time.Minute)
	h.write("Inbox/note.md", "one, edited\n")
	if !h.commitPending().Committed {
		t.Fatal("the snapshot recorded nothing")
	}

	if got := rawGit(t, h.root, "rev-parse", ":Inbox/staged.md"); got != stagedOID {
		t.Fatalf("the user's staged entry changed: %s -> %s", stagedOID, got)
	}
	if got := rawGit(t, h.root, "cat-file", "blob", stagedOID); got != "v2 staged by hand" {
		t.Fatalf("the user's staged content is %q", got)
	}
	// The engine records what is on disk, which is the newest content.
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/staged.md"); got != "v3 on disk" {
		t.Fatalf("history holds %q, want the working-tree content", got)
	}
}

// --- Concurrency and failure -------------------------------------------------

// Losing a compare-and-swap means another writer committed first. The snapshot
// is rebuilt against the new tip rather than forced onto the old one, so the
// other writer's commit survives.
func TestRefConflictIsRetriedNotForced(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	first := h.head()

	marker := filepath.Join(t.TempDir(), "fired")
	shim := gitShim(t, `
if [ "$1" = "update-ref" ] && [ ! -e `+shellQuote(marker)+` ]; then
  : > `+shellQuote(marker)+`
  tree=$("$REAL_GIT" rev-parse "HEAD^{tree}")
  commit=$(GIT_AUTHOR_NAME=Interloper GIT_AUTHOR_EMAIL=i@example.invalid \
           GIT_COMMITTER_NAME=Interloper GIT_COMMITTER_EMAIL=i@example.invalid \
           "$REAL_GIT" commit-tree "$tree" -p HEAD -m interloper)
  "$REAL_GIT" update-ref refs/heads/main "$commit"
fi`)

	racing := h.open(Options{GitExecutable: shim})
	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")

	result, err := racing.CommitPending(context.Background())
	if err != nil {
		t.Fatalf("CommitPending under a losing race: %v", err)
	}
	if !result.Committed {
		t.Fatalf("nothing was committed: %+v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the test did not actually provoke a conflict")
	}
	subjects := h.subjects()
	if !contains(subjects, "interloper") {
		t.Fatalf("the other writer's commit was discarded: %v", subjects)
	}
	if h.head() == first {
		t.Fatal("the branch did not advance")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "two" {
		t.Fatalf("history holds %q after the retry", got)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a conflict that was retried successfully must not latch a block")
	}
}

// A local commit that cannot be written is the failure this engine cannot
// paper over: the working tree holds content the history does not, so it stops
// and says so durably.
func TestLocalCommitFailureLatchesABlock(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	safe := h.head()

	shim := gitShim(t, `
if [ "$1" = "commit-tree" ]; then
  echo "simulated object write failure" >&2
  exit 128
fi`)
	broken := h.open(Options{GitExecutable: shim})
	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")

	_, err := broken.CommitPending(context.Background())
	if KindOf(err) != KindCommitFailed {
		t.Fatalf("CommitPending = %v, want a commit failure", err)
	}
	if h.head() != safe {
		t.Fatal("the branch moved despite a failed commit")
	}
	record, blocked := h.blockRecord()
	if !blocked || record.Kind != KindCommitFailed || len(record.Recovery) == 0 {
		t.Fatalf("a commit failure must latch an explained block: %+v", record)
	}

	// Restart recovery: a fresh Manager on a healthy git still refuses until a
	// person clears the marker, then works normally.
	restarted := h.open(Options{})
	if _, err := restarted.CommitPending(context.Background()); !IsBlocked(err) {
		t.Fatalf("a restarted manager = %v, want a blocked failure", err)
	}
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight on a blocked vault = %v, want a blocked failure", err)
	}
	if err := restarted.ClearBlock(); err != nil {
		t.Fatalf("ClearBlock: %v", err)
	}
	if !mustCommit(t, restarted).Committed {
		t.Fatal("the vault did not recover after the marker was cleared")
	}
	if got := rawGit(t, h.root, "show", "HEAD:Inbox/note.md"); got != "two" {
		t.Fatalf("history holds %q after recovery", got)
	}
}

// A block marker written by an older or newer build, or truncated by a crash,
// keeps refusing rather than silently re-enabling mutations.
func TestCorruptBlockMarkerRefusesMutations(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	if err := h.mgr.state.ensureDir(); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	if err := os.WriteFile(h.mgr.state.path(blockedFileName), []byte("{ truncated"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	restarted := h.open(Options{})
	h.write("Inbox/note.md", "two\n")
	if _, err := restarted.CommitPending(context.Background()); !IsBlocked(err) {
		t.Fatalf("CommitPending with a corrupt marker = %v, want a blocked failure", err)
	}
	if err := restarted.Preflight(context.Background()); !IsBlocked(err) {
		t.Fatalf("Preflight with a corrupt marker = %v, want a blocked failure", err)
	}
}

func mustCommit(t *testing.T, m *Manager) CommitResult {
	t.Helper()
	result, err := m.CommitPending(context.Background())
	if err != nil {
		t.Fatalf("CommitPending: %v", err)
	}
	return result
}

// --- Push --------------------------------------------------------------------

func TestPushWithoutRemoteIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	result, err := h.mgr.Push(context.Background())
	if err != nil {
		t.Fatalf("Push with no remote: %v", err)
	}
	if result.Attempted || result.Pushed {
		t.Fatalf("a vault with no remote must not attempt a push: %+v", result)
	}
}

func TestPushFailureRecordsRetryStateAndNeverBlocks(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	local := h.head()

	rawGit(t, h.root, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	_, err := h.mgr.Push(context.Background())
	if !IsPushFailure(err) {
		t.Fatalf("Push to a missing remote = %v, want a push failure", err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a push failure must never latch the mutation block")
	}
	state, ok := h.mgr.PushStatus()
	if !ok || state.Attempts != 1 || state.LastError == "" {
		t.Fatalf("push state = %+v (ok=%v)", state, ok)
	}
	if h.mgr.PushDue() {
		t.Fatal("a retry must not be due immediately after a failure")
	}

	// Local work continues while the remote is unreachable: remote availability
	// is not an input to local correctness.
	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")
	if !h.commitPending().Committed {
		t.Fatal("a snapshot failed while the remote was unreachable")
	}
	if h.head() == local {
		t.Fatal("the branch did not advance while the remote was unreachable")
	}

	// Recovery: the remote comes back and the retry state is forgotten.
	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "set-url", "origin", bare)
	h.advance(10 * time.Minute)
	if !h.mgr.PushDue() {
		t.Fatal("a retry must be due once the backoff has elapsed")
	}
	result, err := h.mgr.Push(context.Background())
	if err != nil {
		t.Fatalf("Push after recovery: %v", err)
	}
	if !result.Pushed {
		t.Fatalf("push result = %+v", result)
	}
	if _, ok := h.mgr.PushStatus(); ok {
		t.Fatal("a successful push must forget the failure streak")
	}
	if remoteHead := rawGit(t, bare, "rev-parse", "refs/heads/main"); remoteHead != h.head() {
		t.Fatalf("remote head = %s, want %s", remoteHead, h.head())
	}
}

// A remote that rejects the push — because it holds commits this clone does not
// — must never be resolved by rewriting history.
func TestPushNeverForces(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "add", "origin", bare)
	if _, err := h.mgr.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	shared := h.head()

	// Another clone publishes a commit this vault has never seen.
	other := t.TempDir()
	rawGit(t, other, "clone", "-q", bare, "clone")
	clone := filepath.Join(other, "clone")
	rawGit(t, clone, "config", "user.name", "Other")
	rawGit(t, clone, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.md"), []byte("from another machine\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rawGit(t, clone, "add", "elsewhere.md")
	rawGit(t, clone, "commit", "-q", "-m", "from another machine")
	rawGit(t, clone, "push", "-q", "origin", "main")
	remoteHead := rawGit(t, bare, "rev-parse", "refs/heads/main")

	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")
	h.commitPending()

	_, err := h.mgr.Push(context.Background())
	if !IsPushFailure(err) {
		t.Fatalf("Push to a diverged remote = %v, want a push failure", err)
	}
	if got := rawGit(t, bare, "rev-parse", "refs/heads/main"); got != remoteHead {
		t.Fatalf("the remote was overwritten: %s -> %s", remoteHead, got)
	}
	if !contains(h.subjects(), "brainnotes: autosave 2026-08-28T09:01:00Z") {
		t.Fatal("the local snapshot did not survive the rejected push")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a rejected push must never latch the mutation block")
	}
	_ = shared
}

// --- Environment isolation ---------------------------------------------------

// The engine must not be steerable by the environment it happens to inherit. A
// stray GIT_INDEX_FILE or GIT_DIR in the user's shell would otherwise redirect
// a snapshot into another repository.
func TestStrayGitEnvironmentIsIgnored(t *testing.T) {
	h := newHarness(t)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "hijacked-index"))
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "hijacked.git"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "Impostor")

	h.write("Inbox/note.md", "one\n")
	if !h.commitPending().Committed {
		t.Fatal("the snapshot did not run")
	}
	if !h.trackedSet()["Inbox/note.md"] {
		t.Fatal("the snapshot landed somewhere other than the vault")
	}
	if author := rawGit(t, h.root, "log", "-1", "--format=%an"); author != "BrainNotes" {
		t.Fatalf("commit author = %q, want the engine identity", author)
	}
}

// A NUL byte can never be a legal argument, and rejecting it here means no
// caller can smuggle one into a git invocation.
func TestGitArgumentsRejectNULBytes(t *testing.T) {
	requireGit(t)
	g := &gitExec{exe: requireGit(t), dir: t.TempDir()}
	_, err := g.run(context.Background(), gitCall{args: []string{"status", "bad\x00arg"}})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("run with a NUL argument = %v, want a rejection", err)
	}
}

func TestOpenRequiresAVaultRoot(t *testing.T) {
	if _, err := Open(context.Background(), "  ", Options{}); err == nil {
		t.Fatal("Open with an empty root must fail")
	}
	if _, err := Open(context.Background(), "  ", Options{}); errors.Is(err, ErrUnmanaged) {
		t.Fatal("an empty root is a programming error, not an unmanaged vault")
	}
}
