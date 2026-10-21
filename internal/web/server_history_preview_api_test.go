package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// newRestorableVault builds a vault with one journalled frontmatter change and
// returns the base URL, the vault root and that entry's identifier — the
// smallest thing a restore can be previewed against.
func newRestorableVault(t *testing.T) (string, string, string) {
	t.Helper()

	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	return ts.URL, root, decodeManagementResult(t, body).JournalID
}

// driftSourceNote rewrites the note out from under the recorded entry, so the
// restore has newer work to replace and therefore needs a forced, previewed
// confirmation.
func driftSourceNote(t *testing.T, root, status string) {
	t.Helper()

	path := filepath.Join(root, "Source.md")
	content := "---\nstatus: " + status + "\ntitle: Source\n---\nBody marker.\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func historyEntryManifestPath(root, entryID string) string {
	return filepath.Join(noteops.JournalRoot(root), entryID, journal.ManifestFileName)
}

// A receipt authorizes one restore. Replaying it would let a single "yes, replace
// that newer work" be spent twice — the second time against a vault nobody looked
// at, which is exactly the silent replacement this surface exists to prevent.
func TestServer_HistoryPreviewAPI_ReceiptIsOneUse(t *testing.T) {
	baseURL, root, entryID := newRestorableVault(t)
	driftSourceNote(t, root, "blocked")

	preview := previewHistoryRestore(t, baseURL, entryID, "csrf-token")
	first := postUndoRequestWithPreview(t, baseURL, entryID, "csrf-token", "", preview.PreviewID, true, true)
	firstBody := readResponseBody(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first force status: got %d, want %d; body=%q", first.StatusCode, http.StatusOK, firstBody)
	}

	// Drift it again so the replay has something to overwrite, and prove the
	// refusal is the spent receipt rather than a clean restore.
	driftSourceNote(t, root, "blocked-again")
	second := postUndoRequestWithPreview(t, baseURL, entryID, "csrf-token", "", preview.PreviewID, true, true)
	secondBody := readResponseBody(t, second)
	if got := decodeManagementError(t, secondBody).Code; got != "preview_required" {
		t.Fatalf("replayed receipt code = %q, want preview_required; body=%q", got, secondBody)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: blocked-again") {
		t.Fatalf("replayed receipt changed the note:\n%s", got)
	}
}

// A receipt names the entry it was shown for. Without that check, previewing a
// harmless restore and confirming a destructive one would be one request apart.
func TestServer_HistoryPreviewAPI_ReceiptIsBoundToItsEntry(t *testing.T) {
	baseURL, root, firstEntry := newRestorableVault(t)

	secondResp := postFrontmatterSetRequest(t, baseURL, "source", "status", "=", "review", "csrf-token", "")
	secondBody := readResponseBody(t, secondResp)
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("second mutation status: got %d, want %d; body=%q", secondResp.StatusCode, http.StatusOK, secondBody)
	}
	secondEntry := decodeManagementResult(t, secondBody).JournalID
	driftSourceNote(t, root, "blocked")

	preview := previewHistoryRestore(t, baseURL, firstEntry, "csrf-token")
	if preview.EntryID != firstEntry {
		t.Fatalf("preview entry = %q, want %q", preview.EntryID, firstEntry)
	}

	resp := postUndoRequestWithPreview(t, baseURL, secondEntry, "csrf-token", "", preview.PreviewID, true, true)
	body := readResponseBody(t, resp)
	if got := decodeManagementError(t, body).Code; got != "preview_required" {
		t.Fatalf("mismatched receipt code = %q, want preview_required; body=%q", got, body)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: blocked") {
		t.Fatalf("mismatched receipt changed the note:\n%s", got)
	}
}

// The person agreed to lose particular bytes. Bytes that arrived after they
// looked were never shown to them, so the receipt no longer describes the choice
// they made.
func TestServer_HistoryPreviewAPI_RefusesWhenTheContestedFileMovedAgain(t *testing.T) {
	baseURL, root, entryID := newRestorableVault(t)
	driftSourceNote(t, root, "blocked")

	preview := previewHistoryRestore(t, baseURL, entryID, "csrf-token")
	driftSourceNote(t, root, "blocked-later")

	resp := postUndoRequestWithPreview(t, baseURL, entryID, "csrf-token", "", preview.PreviewID, true, true)
	body := readResponseBody(t, resp)
	if got := decodeManagementError(t, body).Code; got != "preview_stale" {
		t.Fatalf("stale file code = %q, want preview_stale; body=%q", got, body)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: blocked-later") {
		t.Fatalf("stale confirmation changed the note:\n%s", got)
	}
}

// The digest pins and the replan are not the same check, and this is the case
// that separates them: the manifest's recorded "after" state is rewritten, so the
// bytes on disk are untouched — every pinned digest still matches — while the
// restore silently acquires a conflict nobody was shown. Only re-planning catches
// it.
func TestServer_HistoryPreviewAPI_RefusesWhenTheConflictSetChanged(t *testing.T) {
	baseURL, root, entryID := newRestorableVault(t)

	preview := previewHistoryRestore(t, baseURL, entryID, "csrf-token")
	if preview.RequiresForce || len(preview.Conflicts) != 0 {
		t.Fatalf("preview = %#v, want a clean restore", preview)
	}

	manifestPath := historyEntryManifestPath(root, entryID)
	raw, err := os.ReadFile(manifestPath) // #nosec G304 -- test-only path
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var entry journal.Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(entry.Files) != 1 {
		t.Fatalf("manifest files = %#v, want one", entry.Files)
	}
	// A recorded "after" the file no longer matches is precisely how planRestore
	// decides a path has moved on.
	entry.Files[0].After.SHA256 = strings.Repeat("a", 64)
	rewritten, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, rewritten, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	resp := postUndoRequestWithPreview(t, baseURL, entryID, "csrf-token", "", preview.PreviewID, false, false)
	body := readResponseBody(t, resp)
	if got := decodeManagementError(t, body).Code; got != "preview_stale" {
		t.Fatalf("changed conflict set code = %q, want preview_stale; body=%q", got, body)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: done") {
		t.Fatalf("stale confirmation changed the note:\n%s", got)
	}
}

// The receipt's pins are not just checked here and discarded — they become
// preconditions of the transaction, which is the only place a writer that lands
// after the check can still be caught. historyRestoreRequirements is the
// translation, and every state a pin can hold has to survive it.
func TestHistoryRestoreRequirements_TranslateEveryPinnedState(t *testing.T) {
	digest := strings.Repeat("a", 64)
	require, err := historyRestoreRequirements([]propertyPreviewFileState{
		{RelPath: "Gone.md"},
		{RelPath: "Notes/Here.md", Digest: "sha256:" + digest},
	})
	if err != nil {
		t.Fatalf("requirements: %v", err)
	}
	want := map[string]vault.FileExpectation{
		"Gone.md":       vault.ExpectMissing(),
		"Notes/Here.md": vault.ExpectHash(digest),
	}
	if !reflect.DeepEqual(require, want) {
		t.Fatalf("requirements = %#v, want %#v", require, want)
	}
}

// A note that has become a directory can still be *previewed* — that is why
// historyRestoreDigest describes a non-regular file instead of failing on it —
// but a FileExpectation can say only "absent" or "these bytes", so there is no
// honest way to pin it. The confirmation is refused by name rather than pinned
// to something untrue.
func TestServer_HistoryPreviewAPI_RefusesToAuthorizeANonRegularFile(t *testing.T) {
	baseURL, root, entryID := newRestorableVault(t)

	notePath := filepath.Join(root, "Source.md")
	if err := os.Remove(notePath); err != nil {
		t.Fatalf("remove note: %v", err)
	}
	if err := os.MkdirAll(notePath, 0o750); err != nil {
		t.Fatalf("mkdir over note: %v", err)
	}

	// Minted with the directory already in place, so every check ahead of the
	// translation agrees: the digests match, and the conflict set has not moved.
	preview := previewHistoryRestore(t, baseURL, entryID, "csrf-token")
	if !preview.RequiresForce {
		t.Fatalf("preview = %#v, want a restore that needs forcing", preview)
	}

	resp := postUndoRequestWithPreview(t, baseURL, entryID, "csrf-token", "", preview.PreviewID, true, true)
	body := readResponseBody(t, resp)
	if got := decodeManagementError(t, body).Code; got != "conflict" {
		t.Fatalf("non-regular destination code = %q, want conflict; body=%q", got, body)
	}
	if !strings.Contains(body, "Source.md") {
		t.Fatalf("refusal should name the path it cannot authorize; body=%q", body)
	}
	if info, err := os.Lstat(notePath); err != nil || !info.IsDir() {
		t.Fatalf("lstat after refusal = %v, %v; want the directory left alone", info, err)
	}
}

// A preview is a read, but it mints the authority to overwrite, so it is guarded
// exactly as the write is.
func TestServer_HistoryPreviewAPI_RefusesUnauthenticatedAndNonPost(t *testing.T) {
	baseURL, _, entryID := newRestorableVault(t)

	noToken := postHistoryPreviewRequest(t, baseURL, entryID, "", "")
	noTokenBody := readResponseBody(t, noToken)
	if noToken.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf-less status: got %d, want %d; body=%q", noToken.StatusCode, http.StatusForbidden, noTokenBody)
	}

	get, err := http.Get(baseURL + historyPreviewAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", historyPreviewAPIRoutePath, err)
	}
	getBody := readResponseBody(t, get)
	if get.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status: got %d, want %d; body=%q", get.StatusCode, http.StatusMethodNotAllowed, getBody)
	}
	if allow := get.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodPost)
	}
}

// "That entry is gone" and "the history is broken" are different answers, and a
// person looking for a version they remember needs to be able to tell them apart.
func TestServer_HistoryPreviewAPI_MissingEntryIsNotFound(t *testing.T) {
	baseURL, _, _ := newRestorableVault(t)

	resp := postHistoryPreviewRequest(t, baseURL, "entry-does-not-exist", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing entry status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
	if got := decodeManagementError(t, body).Code; got != "not_found" {
		t.Fatalf("missing entry code = %q, want not_found; body=%q", got, body)
	}
}

// Previewing with no identifier resolves the newest entry and answers with the
// identifier it resolved, so the confirmation sends that back rather than the
// shorthand — otherwise a mutation landing between the two would move the target.
func TestServer_HistoryPreviewAPI_ResolvesTheLatestEntryByName(t *testing.T) {
	baseURL, _, entryID := newRestorableVault(t)

	preview := previewHistoryRestore(t, baseURL, "", "csrf-token")
	if preview.EntryID != entryID {
		t.Fatalf("preview entry = %q, want the latest %q", preview.EntryID, entryID)
	}
	if preview.Origin != string(historyOriginBrowser) || preview.Command != "bn web frontmatter set" {
		t.Fatalf("preview origin = %q/%q, want a browser save", preview.Origin, preview.Command)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Path != "Source.md" {
		t.Fatalf("preview changes = %#v, want Source.md", preview.Changes)
	}
	if preview.RequiresForce || !strings.Contains(preview.Message, "replaces no newer work") {
		t.Fatalf("preview message = %#v, want a clean restore", preview)
	}
}

// An empty journal has nothing to preview, and says so as a 404 rather than
// answering with an empty plan somebody could confirm.
func TestServer_HistoryPreviewAPI_EmptyJournalIsNotFound(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postHistoryPreviewRequest(t, ts.URL, "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("empty journal status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
}

// The pins are the whole point of the receipt, and this is the only test that
// watches them leave this layer.
//
// Every other refusal in this file is decided by checkHistoryRestorePreview's own
// comparisons, which run before the restore starts — so deleting the handoff into
// noteops.UndoOptions breaks nothing a test can see from outside. The window the
// pins exist to close is the one *after* those comparisons pass: the path locks
// and BeginOperation's Git preflight are still ahead, and bn-vault-sync landing
// inside it rewrites the bytes without changing which paths are in conflict, so
// under force the transaction's own re-reading would authorize whatever arrived.
func TestServer_HistoryUndo_HandsThePreviewPinsToTheRestore(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody marker.\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	entryID := decodeManagementResult(t, body).JournalID
	driftSourceNote(t, root, "blocked")

	preview := previewHistoryRestore(t, ts.URL, entryID, "csrf-token")

	form := url.Values{
		"id":            {entryID},
		"force":         {"1"},
		"confirm_force": {"1"},
		"preview_id":    {preview.PreviewID},
	}
	r := httptest.NewRequest(http.MethodPost, "/api/undo", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	req, err := srv.parseUndoRequest(r)
	if err != nil {
		t.Fatalf("parseUndoRequest: %v", err)
	}

	opts := undoOptionsFor(req)
	if len(opts.Require) == 0 {
		t.Fatal("the receipt's pins never reached the restore, so a forced undo is unpinned again")
	}
	// The bytes somebody was shown — the drifted ones — and not the state the
	// entry recorded. Pinning the recorded state would authorize the very
	// overwrite the force was asked about.
	want := vault.ExpectHash(sha256OfFile(t, filepath.Join(root, "Source.md")))
	if !reflect.DeepEqual(opts.Require["Source.md"], want) {
		t.Fatalf("Require[Source.md] = %#v, want the digest of what the preview showed (%#v)",
			opts.Require["Source.md"], want)
	}
}

func sha256OfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-only path under t.TempDir
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// A leftover temp file is not a failed restore, and neither message may read as
// one. The content is back, journaled and snapshotted; the only action anybody
// can take is deleting the named file, so the only requirement is that it is
// named rather than swallowed.
func TestUndoSuccessMessage_NamesALeftoverWithoutCallingItAFailure(t *testing.T) {
	plan := &journal.RestorePlan{Changes: []journal.RestoreChange{{Path: "Source.md"}}}
	if got := undoSuccessMessage(plan); got != "Undo restored 1 path." {
		t.Fatalf("clean message = %q", got)
	}

	plan.Cleanup = &vault.TxnCleanupError{
		Path:         "Source.md",
		LeftoverPath: "Source.md.tmp",
		Err:          errors.New("permission denied"),
	}
	got := undoSuccessMessage(plan)
	if !strings.HasPrefix(got, "Undo restored 1 path.") {
		t.Fatalf("message stopped reporting the restore: %q", got)
	}
	if !strings.Contains(got, "Source.md.tmp") {
		t.Fatalf("message never names the file it asks somebody to delete: %q", got)
	}
}

// If a cleanup failure ever does come back as an error, the restore did *not*
// finish: journal.Restore carries the housekeeping case out on the plan so the
// operation still reaches Finalize, and the only way to arrive here is that it
// did not — meaning the writes landed with no manifest authorizing them, which is
// what vaultgit refuses the next mutation over. The message must not say the
// restore finished.
func TestUndoRestoreError_DoesNotCallAnUnfinalizedRestoreFinished(t *testing.T) {
	err := undoRestoreError(&vault.TxnCleanupError{
		Path:         "Source.md",
		LeftoverPath: "Source.md.tmp",
		Err:          errors.New("permission denied"),
	})
	var opErr *noteops.Error
	if !errors.As(err, &opErr) {
		t.Fatalf("undoRestoreError returned %T, want a *noteops.Error", err)
	}
	if opErr.Code != noteops.CodeInternal {
		t.Fatalf("code = %q, want %q", opErr.Code, noteops.CodeInternal)
	}
	if strings.Contains(opErr.Error(), "finished") {
		t.Fatalf("the message claims the restore finished: %q", opErr.Error())
	}
	if !strings.Contains(opErr.Error(), "not recorded") {
		t.Fatalf("the message never says the restore was not recorded: %q", opErr.Error())
	}
}

// A restore that could not put displaced content back has one actionable fact:
// the file that content now lives in. The answer the client sees must carry it,
// not only the error chain behind it.
func TestUndoRestoreError_NamesTheRecoveryPath(t *testing.T) {
	recovery := &vault.TxnRecoveryError{
		Path:         "Source.md",
		RecoveryPath: ".txn-custody-1.tmp",
		Err:          &vault.TxnConflictError{Path: "Source.md", Expected: "missing", Actual: "file"},
	}
	for name, cause := range map[string]error{
		"recovery": recovery,
		"rollback": &vault.TxnRollbackError{
			Path:         "Source.md",
			Err:          recovery,
			Failed:       []string{"Other.md"},
			RollbackErrs: []error{errors.New("permission denied")},
		},
	} {
		err := undoRestoreError(cause)
		if got := noteops.CodeOf(err); got != noteops.CodeInternal {
			t.Fatalf("%s: code = %q, want %q", name, got, noteops.CodeInternal)
		}
		if message := noteops.MessageOf(err); !strings.Contains(message, recovery.RecoveryPath) {
			t.Fatalf("%s: message hides the recovery path: %q", name, message)
		}
	}
}

// A cache the server could not refresh must not be reported as a failed restore:
// the content is back on disk, and answering with the reload's error would both
// say otherwise and throw away the one mention a leftover temp file ever gets.
func TestUndoResponseWithReloadNotice_KeepsTheRestoreAsTheAnswer(t *testing.T) {
	plan := &journal.RestorePlan{
		Changes: []journal.RestoreChange{{Path: "Source.md"}},
		Cleanup: &vault.TxnCleanupError{
			Path:         "Source.md",
			LeftoverPath: "Source.md.tmp",
			Err:          errors.New("permission denied"),
		},
	}
	base := undoResponseForPlan(plan)

	if got := undoResponseWithReloadNotice(base, nil); got.Message != base.Message {
		t.Fatalf("a successful reload changed the message: %q", got.Message)
	}

	got := undoResponseWithReloadNotice(base, errors.New("config: reload failed"))
	if !got.OK {
		t.Fatal("a stale cache was reported as a failed restore")
	}
	if !strings.HasPrefix(got.Message, base.Message) {
		t.Fatalf("the restore's own outcome was replaced: %q", got.Message)
	}
	if !strings.Contains(got.Message, "Source.md.tmp") {
		t.Fatalf("the leftover file was dropped along the way: %q", got.Message)
	}
	if !strings.Contains(got.Message, "config: reload failed") {
		t.Fatalf("the reload failure went unmentioned: %q", got.Message)
	}
}
