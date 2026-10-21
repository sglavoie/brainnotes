package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

func TestServer_FrontmatterAPI_SetUnsetJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"status": {Type: config.KindString, Allowed: []string{"draft", "done"}},
		}
	}, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	warmBody := readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK || !strings.Contains(warmBody, "draft") {
		t.Fatalf("warm note response unexpected: status=%d body=%q", warmResp.StatusCode, warmBody)
	}

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Updated frontmatter." {
		t.Fatalf("payload = %#v, want frontmatter update", payload)
	}
	if payload.Key != "source" || payload.Path != "/n/source" || payload.RelPath != "Source.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}
	updated := readNoteFile(t, filepath.Join(root, "Source.md"))
	if !strings.Contains(updated, "status: done") || !strings.Contains(updated, "Body marker.") {
		t.Fatalf("set output unexpected:\n%s", updated)
	}

	refreshedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed /n/source: %v", err)
	}
	refreshedBody := readResponseBody(t, refreshedResp)
	if refreshedResp.StatusCode != http.StatusOK ||
		!strings.Contains(refreshedBody, `<span class="frontmatter-text">done</span>`) ||
		strings.Contains(refreshedBody, `<span class="frontmatter-text">draft</span>`) {
		t.Fatalf("state was not refreshed after set: status=%d body=%q", refreshedResp.StatusCode, refreshedBody)
	}

	unsetResp := postFrontmatterUnsetRequest(t, ts.URL, "source", "status", "csrf-token", "")
	unsetBody := readResponseBody(t, unsetResp)
	if unsetResp.StatusCode != http.StatusOK {
		t.Fatalf("unset status: got %d, want %d; body=%q", unsetResp.StatusCode, http.StatusOK, unsetBody)
	}
	unsetPayload := decodeManagementResult(t, unsetBody)
	if !unsetPayload.OK || unsetPayload.Message != "Unset frontmatter." || strings.TrimSpace(unsetPayload.JournalID) == "" {
		t.Fatalf("unset payload = %#v, want unset success with journal", unsetPayload)
	}
	afterUnset := readNoteFile(t, filepath.Join(root, "Source.md"))
	if strings.Contains(afterUnset, "status:") || !strings.Contains(afterUnset, "title: Source") || !strings.Contains(afterUnset, "Body marker.") {
		t.Fatalf("unset output unexpected:\n%s", afterUnset)
	}
}

func TestServer_FrontmatterAPI_ListAndWikilinkMutations(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nparent: \"[[Old]]\"\ntags:\n  - alpha\n---\nBody marker.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"tags":   {Type: config.KindList, Items: config.KindString},
			"parent": {Type: config.KindWikilink},
		}
	}, Options{
		CSRFToken: "csrf-token",
	})

	for _, step := range []struct {
		prop  string
		op    string
		value string
	}{
		{prop: "tags", op: "+=", value: "beta"},
		{prop: "tags", op: "-=", value: "alpha"},
		{prop: "parent", op: "=", value: "New Parent"},
	} {
		resp := postFrontmatterSetRequest(t, ts.URL, "source", step.prop, step.op, step.value, "csrf-token", "")
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("set %s %s: got %d, want %d; body=%q", step.prop, step.op, resp.StatusCode, http.StatusOK, body)
		}
		payload := decodeManagementResult(t, body)
		if !payload.OK || strings.TrimSpace(payload.JournalID) == "" {
			t.Fatalf("payload = %#v, want success with journal", payload)
		}
	}

	got := readNoteFile(t, filepath.Join(root, "Source.md"))
	if !strings.Contains(got, "- beta") || strings.Contains(got, "- alpha") {
		t.Fatalf("list mutation output unexpected:\n%s", got)
	}
	if !strings.Contains(got, "[[New Parent]]") || strings.Contains(got, "[[Old]]") {
		t.Fatalf("wikilink mutation output unexpected:\n%s", got)
	}
	if !strings.Contains(got, "Body marker.") {
		t.Fatalf("body changed unexpectedly:\n%s", got)
	}
}

func TestServer_FrontmatterAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
		t.Fatalf("note changed without csrf:\n%s", got)
	}
}
