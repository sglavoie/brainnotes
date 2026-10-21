package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
)

func postPinRequest(t *testing.T, baseURL, route string, fields map[string]string, headerToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	for name, value := range fields {
		form.Set(name, value)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+route, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", route, err)
	}
	return resp
}

// newPinnedNotesServer hands back the server itself, which the concurrency test
// needs so it can install the commit hook.
func newPinnedNotesServer(t *testing.T, files map[string][]byte) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	for rel, data := range files {
		writeTestFile(t, root, rel, string(data))
	}
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

type pinResponse struct {
	status int
	body   string
	err    error
}

// postPinAsync sends a pin request from its own goroutine and reports the
// outcome on a channel, since a goroutine must not call t.Fatalf.
func postPinAsync(baseURL, route string, fields map[string]string) <-chan pinResponse {
	out := make(chan pinResponse, 1)
	go func() {
		form := url.Values{}
		for name, value := range fields {
			form.Set(name, value)
		}
		req, err := http.NewRequest(http.MethodPost, baseURL+route, strings.NewReader(form.Encode()))
		if err != nil {
			out <- pinResponse{err: err}
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(csrfHeaderName, "csrf-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			out <- pinResponse{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		out <- pinResponse{status: resp.StatusCode, body: string(body), err: err}
	}()
	return out
}

func awaitPinnedNotesGate(t *testing.T, gates <-chan chan struct{}) chan struct{} {
	t.Helper()
	select {
	case gate := <-gates:
		return gate
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for a pin request to reach the commit hook")
		return nil
	}
}

func awaitPinResponse(t *testing.T, responses <-chan pinResponse) pinResponse {
	t.Helper()
	select {
	case got := <-responses:
		if got.err != nil {
			t.Fatalf("pin request: %v", got.err)
		}
		return got
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for a pin response")
		return pinResponse{}
	}
}

func countJournalEntries(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, config.DefaultJournalDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read journal dir: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count
}

func TestServer_PinsAPI_SetWritesRankWithoutDeclaration(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md": []byte("---\ntitle: Alpha\n---\nAlpha body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postPinRequest(t, ts.URL, notePinSetAPIRoutePath, map[string]string{"key": "alpha"}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Pinned note." || payload.RelPath != "Alpha.md" {
		t.Fatalf("payload = %#v, want pin success for Alpha.md", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); !strings.Contains(got, "pinned: 1.0\n") ||
		!strings.Contains(got, "Alpha body.") {
		t.Fatalf("pin output unexpected:\n%s", got)
	}

	again := postPinRequest(t, ts.URL, notePinSetAPIRoutePath, map[string]string{"key": "alpha"}, "csrf-token")
	againPayload := decodeManagementResult(t, readResponseBody(t, again))
	if againPayload.Message != "Note already pinned." {
		t.Fatalf("second pin message = %q, want already pinned", againPayload.Message)
	}
	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); !strings.Contains(got, "pinned: 1.0\n") {
		t.Fatalf("second pin rewrote the rank:\n%s", got)
	}
}

func TestServer_PinsAPI_SetRequiresCSRF(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md": []byte("---\ntitle: Alpha\n---\nAlpha body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postPinRequest(t, ts.URL, notePinSetAPIRoutePath, map[string]string{"key": "alpha"}, "wrong-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); strings.Contains(got, "pinned:") {
		t.Fatalf("note pinned without csrf:\n%s", got)
	}
}

func TestServer_PinsAPI_RemoveDropsPinnedKey(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md": []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postPinRequest(t, ts.URL, notePinRemoveAPIRoutePath, map[string]string{"key": "alpha"}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Unpinned note." || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("payload = %#v, want unpin success with journal", payload)
	}
	got := readNoteFile(t, filepath.Join(root, "Alpha.md"))
	if strings.Contains(got, "pinned") || !strings.Contains(got, "title: Alpha") || !strings.Contains(got, "Alpha body.") {
		t.Fatalf("unpin output unexpected:\n%s", got)
	}

	again := postPinRequest(t, ts.URL, notePinRemoveAPIRoutePath, map[string]string{"key": "alpha"}, "csrf-token")
	againPayload := decodeManagementResult(t, readResponseBody(t, again))
	if againPayload.Message != "Note was not pinned." {
		t.Fatalf("second unpin message = %q, want not pinned", againPayload.Message)
	}
}

func TestServer_PinsAPI_ReorderWritesMidpointAndTouchesOneFile(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md":   []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
		"Bravo.md":   []byte("---\npinned: 2.0\ntitle: Bravo\n---\nBravo body.\n"),
		"Charlie.md": []byte("---\npinned: 3.0\ntitle: Charlie\n---\nCharlie body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	alphaBefore := readNoteFile(t, filepath.Join(root, "Alpha.md"))
	bravoBefore := readNoteFile(t, filepath.Join(root, "Bravo.md"))

	resp := postPinRequest(t, ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key":    "charlie",
		"before": "alpha",
		"after":  "bravo",
	}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Reordered pins." || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("payload = %#v, want reorder success with journal", payload)
	}

	if got := readNoteFile(t, filepath.Join(root, "Charlie.md")); !strings.Contains(got, "pinned: 1.5\n") {
		t.Fatalf("reorder did not write the midpoint:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); got != alphaBefore {
		t.Fatalf("Alpha.md changed:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Bravo.md")); got != bravoBefore {
		t.Fatalf("Bravo.md changed:\n%s", got)
	}
}

func TestServer_PinsAPI_ReorderMissingNeighbourIsNotFound(t *testing.T) {
	// A neighbour key naming no note at all fails resolution, which is a
	// different answer from a neighbour that exists but is no longer pinned.
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md": []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
		"Bravo.md": []byte("---\npinned: 2.0\ntitle: Bravo\n---\nBravo body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	bravoBefore := readNoteFile(t, filepath.Join(root, "Bravo.md"))

	resp := postPinRequest(t, ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key":    "bravo",
		"before": "ghost",
	}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "not_found" {
		t.Fatalf("payload = %#v, want not_found", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Bravo.md")); got != bravoBefore {
		t.Fatalf("Bravo.md changed on a refused reorder:\n%s", got)
	}
	if n := countJournalEntries(t, root); n != 0 {
		t.Fatalf("journal entries = %d, want 0", n)
	}
}

func TestServer_PinsAPI_ReorderUnpinnedNeighbourConflicts(t *testing.T) {
	// Charlie is a real note that is not pinned, so planning refuses the move
	// as a conflict rather than as a missing note.
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md":   []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
		"Bravo.md":   []byte("---\npinned: 2.0\ntitle: Bravo\n---\nBravo body.\n"),
		"Charlie.md": []byte("---\ntitle: Charlie\n---\nCharlie body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	bravoBefore := readNoteFile(t, filepath.Join(root, "Bravo.md"))

	resp := postPinRequest(t, ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key":    "bravo",
		"before": "charlie",
	}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	if payload := decodeManagementError(t, body); payload.OK || payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Bravo.md")); got != bravoBefore {
		t.Fatalf("Bravo.md changed on a refused reorder:\n%s", got)
	}
	if n := countJournalEntries(t, root); n != 0 {
		t.Fatalf("journal entries = %d, want 0", n)
	}
}

func TestServer_PinsAPI_ReorderStaleGapConflicts(t *testing.T) {
	// Every note the request names is still pinned, but Bravo sits between
	// Alpha and Charlie, so the pair no longer describes a gap.
	files := map[string][]byte{
		"Alpha.md":   []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
		"Bravo.md":   []byte("---\npinned: 2.0\ntitle: Bravo\n---\nBravo body.\n"),
		"Charlie.md": []byte("---\npinned: 3.0\ntitle: Charlie\n---\nCharlie body.\n"),
		"Delta.md":   []byte("---\npinned: 4.0\ntitle: Delta\n---\nDelta body.\n"),
	}

	for name, fields := range map[string]map[string]string{
		"note between the named pair": {"key": "delta", "before": "alpha", "after": "charlie"},
		"head sentinel below a pin":   {"key": "delta", "after": "bravo"},
		"tail sentinel above a pin":   {"key": "delta", "before": "bravo"},
		"both sentinels":              {"key": "delta"},
	} {
		t.Run(name, func(t *testing.T) {
			ts, root := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: "csrf-token"})
			before := map[string]string{}
			for rel := range files {
				before[rel] = readNoteFile(t, filepath.Join(root, rel))
			}

			resp := postPinRequest(t, ts.URL, notePinReorderAPIRoutePath, fields, "csrf-token")
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
			}
			if payload := decodeManagementError(t, body); payload.OK || payload.Code != "conflict" {
				t.Fatalf("payload = %#v, want conflict", payload)
			}
			for rel, want := range before {
				if got := readNoteFile(t, filepath.Join(root, rel)); got != want {
					t.Fatalf("%s changed on a refused reorder:\n%s", rel, got)
				}
			}
			if n := countJournalEntries(t, root); n != 0 {
				t.Fatalf("journal entries = %d, want 0", n)
			}
		})
	}
}

func TestServer_PinsAPI_ReorderRenormalizeKeepsOrderInOneJournalEntry(t *testing.T) {
	// Bravo and Charlie share a rank, so no float fits between them and the
	// move must renormalize the whole list instead of writing a midpoint.
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Alpha.md":   []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
		"Bravo.md":   []byte("---\npinned: 2.0\ntitle: Bravo\n---\nBravo body.\n"),
		"Charlie.md": []byte("---\npinned: 2.0\ntitle: Charlie\n---\nCharlie body.\n"),
		"Delta.md":   []byte("---\npinned: 5.0\ntitle: Delta\n---\nDelta body.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp := postPinRequest(t, ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key":    "delta",
		"before": "bravo",
		"after":  "charlie",
	}, "csrf-token")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Reordered pins." || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("payload = %#v, want reorder success with journal", payload)
	}

	want := map[string]string{
		"Alpha.md":   "pinned: 1.0\n",
		"Bravo.md":   "pinned: 2.0\n",
		"Delta.md":   "pinned: 3.0\n",
		"Charlie.md": "pinned: 4.0\n",
	}
	for rel, rank := range want {
		if got := readNoteFile(t, filepath.Join(root, rel)); !strings.Contains(got, rank) {
			t.Fatalf("%s: want %q, got:\n%s", rel, rank, got)
		}
	}
	if n := countJournalEntries(t, root); n != 1 {
		t.Fatalf("journal entries = %d, want 1", n)
	}
}

// TestServer_PinsAPI_ConcurrentReordersRejectStaleGap is F-002 end to end. Both
// requests validate the same Bravo/Charlie gap against the same list and plan
// the same 2.5 midpoint, and both are held there. The first one to commit fills
// the gap; the second must then be refused, because its plan describes a list
// that no longer exists — without the pinned-list precondition it would write
// 2.5 too and produce an order neither browser showed.
func TestServer_PinsAPI_ConcurrentReordersRejectStaleGap(t *testing.T) {
	files := map[string][]byte{
		"Alpha.md":   []byte("---\npinned: 1.0\ntitle: Alpha\n---\nAlpha body.\n"),
		"Bravo.md":   []byte("---\npinned: 2.0\ntitle: Bravo\n---\nBravo body.\n"),
		"Charlie.md": []byte("---\npinned: 3.0\ntitle: Charlie\n---\nCharlie body.\n"),
		"Delta.md":   []byte("---\npinned: 4.0\ntitle: Delta\n---\nDelta body.\n"),
	}
	ts, srv, root := newPinnedNotesServer(t, files)

	gates := make(chan chan struct{}, 2)
	srv.beforePinnedNotesCommit = func() {
		gate := make(chan struct{})
		gates <- gate
		<-gate
	}

	unchanged := map[string]string{}
	for _, rel := range []string{"Bravo.md", "Charlie.md", "Delta.md"} {
		unchanged[rel] = readNoteFile(t, filepath.Join(root, rel))
	}

	// Delta is held first so it plans against the list Alpha is about to change.
	loser := postPinAsync(ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key": "delta", "before": "bravo", "after": "charlie",
	})
	loserGate := awaitPinnedNotesGate(t, gates)
	winner := postPinAsync(ts.URL, notePinReorderAPIRoutePath, map[string]string{
		"key": "alpha", "before": "bravo", "after": "charlie",
	})
	winnerGate := awaitPinnedNotesGate(t, gates)

	close(winnerGate)
	first := awaitPinResponse(t, winner)
	close(loserGate)
	second := awaitPinResponse(t, loser)

	if first.status != http.StatusOK {
		t.Fatalf("first reorder status: got %d, want %d; body=%q", first.status, http.StatusOK, first.body)
	}
	if payload := decodeManagementResult(t, first.body); !payload.OK || payload.Message != "Reordered pins." {
		t.Fatalf("first payload = %#v, want reorder success", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); !strings.Contains(got, "pinned: 2.5\n") {
		t.Fatalf("first reorder did not write the midpoint:\n%s", got)
	}

	if second.status != http.StatusConflict {
		t.Fatalf("second reorder status: got %d, want %d; body=%q", second.status, http.StatusConflict, second.body)
	}
	if payload := decodeManagementError(t, second.body); payload.OK || payload.Code != "conflict" {
		t.Fatalf("second payload = %#v, want conflict", payload)
	}
	for rel, want := range unchanged {
		if got := readNoteFile(t, filepath.Join(root, rel)); got != want {
			t.Fatalf("%s changed on the refused reorder:\n%s", rel, got)
		}
	}
	if n := countJournalEntries(t, root); n != 1 {
		t.Fatalf("journal entries = %d, want 1 (only the committed reorder)", n)
	}
}

// TestServer_PinsAPI_ReorderRenormalizesAListPastTheOldWriteCap is F-003 end to
// end: every pin shares a rank, so one drag renumbers the whole list, and the
// list is long enough that the renumber used to be refused by the atomic
// writer's fixed 256-note cap — the page rendered fine and the reorder could
// never complete.
