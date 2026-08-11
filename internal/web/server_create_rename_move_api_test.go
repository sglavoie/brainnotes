package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestServer_CreateAPI_BareTargetCreatesStubJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Source.md": []byte("---\ntitle: Source\n---\nBody [[Missing Target]].\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/notes/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/notes/source: %v", err)
	}
	warmBody := readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(warmBody, `/broken?q=missing&#43;target`) {
		t.Fatalf("warm note should show a broken-link route, got %q", warmBody)
	}

	resp := postCreateRequest(t, ts.URL, "notes/source", "Missing Target", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Created note." {
		t.Fatalf("payload = %#v, want create success", payload)
	}
	if payload.Key != "notes/missing target" || payload.Path != "/n/notes/missing%20target" || payload.RelPath != "Notes/Missing Target.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	assertFile := filepath.Join(root, "Notes", "Missing Target.md")
	created := readNoteFile(t, assertFile)
	if created != "---\ntitle: Missing Target\n---\n" {
		t.Fatalf("created stub = %q, want title-only frontmatter", created)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	sourceResp, err := http.Get(ts.URL + "/n/notes/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed source note: %v", err)
	}
	sourceBody := readResponseBody(t, sourceResp)
	if sourceResp.StatusCode != http.StatusOK {
		t.Fatalf("refreshed source status: got %d, want %d", sourceResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(sourceBody, `href="/n/notes/missing%20target"`) {
		t.Fatalf("source note did not resolve created target after refresh, got %q", sourceBody)
	}
	if strings.Contains(sourceBody, `/broken?q=missing&#43;target`) {
		t.Fatalf("source note still renders created target as broken, got %q", sourceBody)
	}
}

func TestServer_CreateAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postCreateRequest(t, ts.URL, "source", "Missing", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Missing.md")); !os.IsNotExist(err) {
		t.Fatalf("created file stat err = %v, want missing", err)
	}
}

func TestServer_CreateAPI_InvalidTargetReturnsBadRequest(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[../Escape]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postCreateRequest(t, ts.URL, "source", "../Escape", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "invalid_request" {
		t.Fatalf("payload = %#v, want invalid_request", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Escape.md")); !os.IsNotExist(err) {
		t.Fatalf("invalid target created file, stat err=%v", err)
	}
}

func TestServer_RenameAPI_RewritesWikilinksJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nSelf [[Source]].\n"),
		"Ref.md":    []byte("---\ntitle: Ref\ncategories:\n  - \"[[Source]]\"\n---\nSee [[Source]] and [[Source|alias]].\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.WikilinkProperties = []string{"categories"}
	}, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}

	resp := postRenameRequest(t, ts.URL, "source", "Renamed", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Renamed note." {
		t.Fatalf("payload = %#v, want rename success", payload)
	}
	if payload.Key != "renamed" || payload.Path != "/n/renamed" || payload.RelPath != "Renamed.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	renamed := readNoteFile(t, filepath.Join(root, "Renamed.md"))
	if !strings.Contains(renamed, "Self [[Renamed]].") {
		t.Fatalf("renamed note self-link not rewritten:\n%s", renamed)
	}
	ref := readNoteFile(t, filepath.Join(root, "Ref.md"))
	if strings.Count(ref, "[[Renamed]]") < 2 {
		t.Fatalf("ref body/frontmatter links not rewritten after rename:\n%s", ref)
	}
	if !strings.Contains(ref, "[[Renamed|alias]]") || strings.Contains(ref, "[[Source]]") {
		t.Fatalf("ref links unexpected after rename:\n%s", ref)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	oldResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET old note: %v", err)
	}
	readResponseBody(t, oldResp)
	if oldResp.StatusCode != http.StatusNotFound {
		t.Fatalf("old route status: got %d, want %d", oldResp.StatusCode, http.StatusNotFound)
	}
	newResp, err := http.Get(ts.URL + "/n/renamed") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET renamed note: %v", err)
	}
	readResponseBody(t, newResp)
	if newResp.StatusCode != http.StatusOK {
		t.Fatalf("new route status: got %d, want %d", newResp.StatusCode, http.StatusOK)
	}
}

func TestServer_RenameAPI_ConflictDoesNotRewrite(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":  []byte("---\ntitle: Source\n---\nBody\n"),
		"Renamed.md": []byte("---\ntitle: Renamed\n---\nExisting\n"),
		"Ref.md":     []byte("---\ntitle: Ref\n---\nSee [[Source]].\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postRenameRequest(t, ts.URL, "source", "Renamed", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Ref.md")); !strings.Contains(got, "[[Source]]") || strings.Contains(got, "[[Renamed]]") {
		t.Fatalf("reference was rewritten despite conflict:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Renamed.md")); !strings.Contains(got, "Existing") {
		t.Fatalf("destination was overwritten despite conflict:\n%s", got)
	}
}

func TestServer_RenameAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postRenameRequest(t, ts.URL, "source", "Renamed", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Source.md")); err != nil {
		t.Fatalf("source should remain: %v", err)
	}
}

func TestServer_MoveAPI_RewritesWikilinksJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Books/Source.md": []byte("---\ntitle: Source\n---\nFolder self [[Books/Source]]. Bare [[Source]].\n"),
		"Ref.md":          []byte("---\ntitle: Ref\ncategories:\n  - \"[[Books/Source]]\"\n---\nSee [[Books/Source]] and bare [[Source]].\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.WikilinkProperties = []string{"categories"}
	}, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/books/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/books/source: %v", err)
	}
	readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}

	resp := postMoveRequest(t, ts.URL, "books/source", "Archive", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Moved note." {
		t.Fatalf("payload = %#v, want move success", payload)
	}
	if payload.Key != "archive/source" || payload.Path != "/n/archive/source" || payload.RelPath != "Archive/Source.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Books", "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	moved := readNoteFile(t, filepath.Join(root, "Archive", "Source.md"))
	if !strings.Contains(moved, "Folder self [[Archive/Source]]. Bare [[Source]].") {
		t.Fatalf("moved note links unexpected:\n%s", moved)
	}
	ref := readNoteFile(t, filepath.Join(root, "Ref.md"))
	if strings.Count(ref, "[[Archive/Source]]") < 2 {
		t.Fatalf("ref body/frontmatter links not rewritten after move:\n%s", ref)
	}
	if !strings.Contains(ref, "bare [[Source]]") || strings.Contains(ref, "[[Books/Source]]") {
		t.Fatalf("ref links unexpected after move:\n%s", ref)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	oldResp, err := http.Get(ts.URL + "/n/books/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET old note: %v", err)
	}
	readResponseBody(t, oldResp)
	if oldResp.StatusCode != http.StatusNotFound {
		t.Fatalf("old route status: got %d, want %d", oldResp.StatusCode, http.StatusNotFound)
	}
	newResp, err := http.Get(ts.URL + "/n/archive/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET moved note: %v", err)
	}
	readResponseBody(t, newResp)
	if newResp.StatusCode != http.StatusOK {
		t.Fatalf("new route status: got %d, want %d", newResp.StatusCode, http.StatusOK)
	}
}

func TestServer_MoveAPI_ConflictDoesNotRewrite(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Books/Source.md":   []byte("---\ntitle: Source\n---\nBody\n"),
		"Archive/Source.md": []byte("---\ntitle: Source\n---\nExisting\n"),
		"Ref.md":            []byte("---\ntitle: Ref\n---\nSee [[Books/Source]].\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postMoveRequest(t, ts.URL, "books/source", "Archive", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Ref.md")); !strings.Contains(got, "[[Books/Source]]") || strings.Contains(got, "[[Archive/Source]]") {
		t.Fatalf("reference was rewritten despite conflict:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Archive", "Source.md")); !strings.Contains(got, "Existing") {
		t.Fatalf("destination was overwritten despite conflict:\n%s", got)
	}
}
