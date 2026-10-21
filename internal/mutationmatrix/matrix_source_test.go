package mutationmatrix

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/web"
)

// A raw-Markdown save replaces a whole note, so the fixture content below is a
// complete note rather than a fragment. The frontmatter is deliberately left in
// the order a person would type it: a save writes the submitted bytes verbatim,
// and a row that happened to submit alphabetically sorted keys could not tell a
// verbatim write from one that round-tripped through frontmatter.Marshal.
const (
	sourceSaveRel     = "Alpha.md"
	sourceSaveKey     = "alpha"
	sourceSaveContent = "---\ntitle: Alpha\nstatus: done\n---\n\nAlpha body, rewritten whole.\n"

	// The create lands in a directory the fixture does not have, so a refused
	// create is shown to have made no folder either.
	sourceCreateRel     = "Drafts/Delta.md"
	sourceCreateContent = "---\ntitle: Delta\nstatus: draft\n---\n\nDelta body.\n"

	// sourceSaveLedgerDir duplicates the constant internal/noteops keeps private.
	// The duplication is the point: this package asserts from the outside that the
	// idempotency record stays out of the content history, so it has to name the
	// place independently of the code that chose it.
	sourceSaveLedgerDir = ".bnotes/source-saves"

	// The attachment fixture. sourceUploadRel is named independently of the code
	// for the same reason the ledger directory is: this package asserts where the
	// bytes end up, so it must not ask the code that decided.
	sourceUploadOwner = "matrix-draft"
	sourceUploadName  = "matrix.png"
	sourceUploadRel   = "attachments/matrix.png"
	sourceUploadDir   = ".bnotes/uploads"

	sourceSaveContentWithAttachment = "---\ntitle: Alpha\nstatus: done\n---\n\nAlpha body with ![[attachments/matrix.png]].\n"
)

// sourceUploadPayload is binary and not valid UTF-8, so a byte damaged anywhere
// between the request body and the vault shows up as a digest that does not
// match rather than as text that still reads.
var sourceUploadPayload = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff}

// sourceState is what a save has to be planned against: the revision the edit
// was made from, and the bytes that revision stands for, which is what a no-op
// resubmits.
type sourceState struct {
	revision string
	content  string
}

// sourceMutations are the whole-note Markdown writers, reached directly and over
// HTTP. They are their own group because both rows carry an idempotency record
// into the same transaction as the note, and that record is the one path in the
// table that is written by a journaled operation and still must not reach the
// snapshot — which is exactly what the "exactly its paths" assertion measures.
func sourceMutations() []mutation {
	return []mutation{
		{
			name: "noteops/source-save",
			prepare: func(t *testing.T, h *harness) any {
				return readSourceState(t, h, sourceSaveRel)
			},
			apply: func(t *testing.T, h *harness, state any) error {
				return saveSource(h, sourceSaveRel, sourceSaveContent, state.(sourceState).revision, "")
			},
			touches:   []string{sourceSaveRel},
			journaled: true,
			noop: func(t *testing.T, h *harness, state any) error {
				// The note's own bytes, submitted back against its own revision: the
				// same code path with nothing to write.
				current := state.(sourceState)
				return saveSource(h, sourceSaveRel, current.content, current.revision, "")
			},
		},
		{
			name: "noteops/source-create",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.SaveSource(h.root, h.cfg, noteops.SaveSourceRequest{
					RelPath: sourceCreateRel,
					Content: sourceCreateContent,
					SaveID:  "matrix-source-create",
					Create:  true,
				}, noteops.WithSourceCommand("bn source create "+sourceCreateRel))
				return err
			},
			touches:   []string{sourceCreateRel},
			journaled: true,
			// The no-op create — the same save id submitted twice — cannot live here,
			// because this row's apply is what makes the record it would replay.
			// TestSourceSaveRecordsIdempotencyOutsideTheHistory covers it.
		},
		{
			// A save carrying an attachment is a distinct write class: two paths in
			// one transaction, one of them copied out of private staging rather than
			// submitted in the request. The row is here to hold it to the same bar as
			// every other mutation — one commit, exactly these paths — because the
			// failure it guards against is a note and its picture landing in two
			// commits, or the picture landing and the note not.
			name: "web/source-save-with-attachment",
			prepare: func(t *testing.T, h *harness) any {
				return sourceSaveWithUpload{
					source: getSource(t, h, sourceSaveKey),
					upload: uploadAttachment(t, h, sourceUploadOwner, sourceUploadName, sourceUploadPayload),
				}
			},
			apply: func(t *testing.T, h *harness, state any) error {
				prepared := state.(sourceSaveWithUpload)
				return postSourceSave(t, h, sourceSaveKey,
					sourceSaveContentWithAttachment, prepared.source.revision, prepared.upload)
			},
			touches:   []string{sourceSaveRel, sourceUploadRel},
			journaled: true,
			overHTTP:  true,
			// No no-op row: the save consumes the upload, so resubmitting it is a
			// different request rather than the same one with nothing to do.
			// TestAttachmentUploadStaysOutOfTheHistory covers the staging half.
		},
		{
			name: "web/source-save",
			prepare: func(t *testing.T, h *harness) any {
				// Read over the route rather than in process, so the row proves the
				// revision the API publishes is the one its own save accepts.
				return getSource(t, h, sourceSaveKey)
			},
			apply: func(t *testing.T, h *harness, state any) error {
				return postSourceSave(t, h, sourceSaveKey, sourceSaveContent, state.(sourceState).revision)
			},
			touches:   []string{sourceSaveRel},
			journaled: true,
			overHTTP:  true,
			noop: func(t *testing.T, h *harness, state any) error {
				current := state.(sourceState)
				return postSourceSave(t, h, sourceSaveKey, current.content, current.revision)
			},
		},
	}
}

func readSourceState(t *testing.T, h *harness, rel string) sourceState {
	t.Helper()
	doc, err := noteops.ReadSource(h.root, h.cfg, rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if !doc.Exists {
		t.Fatalf("%s does not exist in the fixture", rel)
	}
	return sourceState{revision: doc.Revision, content: doc.Content}
}

func saveSource(h *harness, rel, content, base, saveID string) error {
	_, err := noteops.SaveSource(h.root, h.cfg, noteops.SaveSourceRequest{
		RelPath:      rel,
		Content:      content,
		BaseRevision: base,
		SaveID:       saveID,
	}, noteops.WithSourceCommand("bn source save "+rel))
	return err
}

// sourceServer starts a real server over the fixture vault, the way postCapture
// does: nothing about the wiring is stubbed, so a refusal in the guard is a
// refusal this test sees.
func sourceServer(t *testing.T, h *harness) (*httptest.Server, error) {
	t.Helper()
	srv, err := web.NewServer(h.cfg, h.v, web.Options{CSRFToken: matrixCSRFToken})
	if err != nil {
		return nil, err
	}
	return httptest.NewServer(srv.Handler()), nil
}

// getSource reads a note's source over the HTTP route and returns the revision
// and bytes it published.
func getSource(t *testing.T, h *harness, key string) sourceState {
	t.Helper()
	ts, err := sourceServer(t, h)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/source?key=" + url.QueryEscape(key))
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	defer resp.Body.Close()

	var payload struct {
		OK       bool   `json:"ok"`
		Revision string `json:"revision"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode source response: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("GET /api/source answered %d (%+v)", resp.StatusCode, payload)
	}
	return sourceState{revision: payload.Revision, content: payload.Content}
}

// postSourceSave drives the web save route and translates the response back into
// the operation error the rest of the matrix reasons about.
//
// The translation is the same assertion postCapture makes, and it is a separate
// claim on this route: a source save answers with its own error envelope, which
// carries the note's current bytes on a stale refusal and the new revision on a
// snapshot failure. An envelope of its own is an envelope that can disagree with
// the shared status mapping, so this checks the status against the code in the
// body on every failure the table provokes.
func postSourceSave(t *testing.T, h *harness, key, content, revision string, uploads ...uploadState) error {
	t.Helper()
	ts, err := sourceServer(t, h)
	if err != nil {
		return err
	}
	defer ts.Close()

	body := strings.NewReader(sourceSaveForm(key, content, revision, uploads...).Encode())
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/source/save", body)
	if err != nil {
		t.Fatalf("build source save request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-BN-CSRF", matrixCSRFToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post source save: %v", err)
	}
	defer resp.Body.Close()

	var payload sourceSaveAnswer
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode source save response: %v", err)
	}
	return sourceSaveError(t, resp.StatusCode, payload)
}

// sourceSaveForm is the request body of a save, with the attachment fields the
// editor sends: one owner for the draft, one `attachment` per upload.
func sourceSaveForm(key, content, revision string, uploads ...uploadState) url.Values {
	form := url.Values{}
	form.Set("key", key)
	form.Set("content", content)
	form.Set("revision", revision)
	for _, upload := range uploads {
		form.Set("owner", upload.owner)
		form.Add("attachment", upload.id)
	}
	return form
}

// sourceSaveAnswer is the part of the save payload the matrix judges: whether it
// succeeded, and, when it did not, the claim the client has to act on.
type sourceSaveAnswer struct {
	OK       bool   `json:"ok"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Revision string `json:"revision"`
	Changed  bool   `json:"changed"`
}

// sourceSaveError rebuilds the noteops error a direct caller would have got, so
// an HTTP row is judged by the same assertions as its in-process twin.
func sourceSaveError(t *testing.T, status int, payload sourceSaveAnswer) error {
	t.Helper()
	if status == http.StatusOK {
		return nil
	}
	// The snapshot-failure row asserts through the returned error that the bytes
	// landed and the history does not cover them. Over HTTP the error is rebuilt
	// from the payload, so the two fields that carry that same claim to a client
	// are checked here rather than dropped: a 500 with no revision would make the
	// editor resubmit against a revision its own save replaced.
	if noteops.ErrorCode(payload.Code) == noteops.CodeSnapshotFailed {
		if !payload.Changed || payload.Revision == "" {
			t.Fatalf("a snapshot failure answered changed=%v revision=%q, want the landed save named",
				payload.Changed, payload.Revision)
		}
	}
	opErr := &noteops.Error{Code: noteops.ErrorCode(payload.Code), Message: payload.Message}
	if want := noteops.HTTPStatusForError(opErr); status != want {
		t.Fatalf("POST /api/source/save answered %d for code %q, want %d", status, payload.Code, want)
	}
	return opErr
}

// TestSourceSaveRecordsIdempotencyOutsideTheHistory is the other half of the
// "exactly its paths" assertion for a keyed save.
//
// A keyed save writes two files in one transaction: the note, and a record of
// what that save id did. The record has to be in the same transaction — its
// existence is what proves the bytes landed, which is what lets a resubmission
// after a lost response be answered instead of re-applied — and it must not be
// in the snapshot, because it holds digests rather than content and committing
// it would put a second path in the history of every save.
//
// The row in the table proves the commit holds only the note. On its own that
// would also pass if the record were never written at all, so this proves the
// record exists, that it is out of the history, and that a second submission of
// the same save id is answered from it with no further commit.
func TestSourceSaveRecordsIdempotencyOutsideTheHistory(t *testing.T) {
	const saveID = "matrix-source-idempotency"
	h := newHarness(t, nil)
	base := readSourceState(t, h, sourceSaveRel)

	first := keyedSourceSave(t, h, saveID, base.revision, "keyed save")
	if !first.Changed {
		t.Fatal("the first keyed save reported changing nothing")
	}
	assertSourceRecordOutsideTheHistory(t, h)

	// The same request again. The note has moved on, so the base revision it
	// carries is stale — and the record is what makes that answerable rather than
	// a conflict over an edit already on disk.
	head := h.head()
	second := keyedSourceSave(t, h, saveID, base.revision, "resubmitting the same save id")
	if !second.Replayed || second.Changed {
		t.Fatalf("the resubmission reported replayed=%v changed=%v, want replayed=true changed=false",
			second.Replayed, second.Changed)
	}
	if second.Revision != first.Revision {
		t.Fatalf("the replay named revision %q, want the one the save landed at %q",
			second.Revision, first.Revision)
	}
	if got := h.head(); got != head {
		t.Fatalf("a replayed save committed %q", h.subject("HEAD"))
	}
}

// keyedSourceSave submits the fixture's save once under saveID.
//
// Callers submit twice, and both submissions are byte-identical by construction
// — the same content and the same base revision — because that is the only thing
// a lost response can look like from the server's side: the client that never
// heard the answer has nothing newer to send.
func keyedSourceSave(t *testing.T, h *harness, saveID, baseRevision, what string) *noteops.SaveSourceResult {
	t.Helper()
	result, err := noteops.SaveSource(h.root, h.cfg, noteops.SaveSourceRequest{
		RelPath:      sourceSaveRel,
		Content:      sourceSaveContent,
		BaseRevision: baseRevision,
		SaveID:       saveID,
	}, noteops.WithSourceCommand("bn source save "+sourceSaveRel))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return result
}

// assertSourceRecordOutsideTheHistory is the two halves of the record's claim:
// it is on disk, and it is not in the commit.
func assertSourceRecordOutsideTheHistory(t *testing.T, h *harness) {
	t.Helper()
	if records := sourceSaveRecords(t, h); len(records) != 1 {
		t.Fatalf("the vault holds %d idempotency records, want exactly 1: %v", len(records), records)
	}
	for _, p := range h.changedPaths("HEAD") {
		if strings.HasPrefix(p, sourceSaveLedgerDir+"/") {
			t.Fatalf("the snapshot committed the idempotency record %s", p)
		}
	}
}

// sourceSaveRecords lists the idempotency records on disk, by name only.
func sourceSaveRecords(t *testing.T, h *harness) []string {
	t.Helper()
	dir := filepath.Join(h.root, filepath.FromSlash(sourceSaveLedgerDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", sourceSaveLedgerDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type()&fs.ModeDir == 0 {
			names = append(names, entry.Name())
		}
	}
	return names
}
