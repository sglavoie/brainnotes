//go:build darwin

package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/properties"
)

const fileProviderDomainAttr = "com.apple.file-provider-domain-id"

// markFileProvider makes root look like storage a File Provider extension owns.
// It runs after the fixture is on disk, so only the requests under test face the
// policy.
func markFileProvider(t *testing.T, root string) {
	t.Helper()
	if err := unix.Setxattr(root, fileProviderDomainAttr, []byte("fixture-domain"), 0); err != nil {
		t.Skipf("cannot set %s on %q: %v", fileProviderDomainAttr, root, err)
	}
}

func assertForbiddenResponse(t *testing.T, route string, status int, body string) {
	t.Helper()
	if status != http.StatusForbidden {
		t.Fatalf("POST %s status = %d, want %d; body %q", route, status, http.StatusForbidden, body)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode %s body %q: %v", route, body, err)
	}
	if payload.Code != string(noteops.CodeForbidden) {
		t.Fatalf("POST %s code = %q, want %q", route, payload.Code, noteops.CodeForbidden)
	}
	for _, want := range []string{"File Provider", "there is no override"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("POST %s message = %q, want it to contain %q", route, payload.Message, want)
		}
	}
}

// The typed error reaches HTTP as a 403 rather than a 500: nothing failed, the
// write is simply not allowed to land here.
func TestPropertyApplyIsForbiddenUnderFileProviderRoot(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	// Previewing is a read and must keep working, so the receipt is issued
	// before the root is marked and applied after.
	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionReplaceValue,
		Property: "status",
		Notes:    []string{"Alpha.md"},
		Match:    &properties.TypedValue{Kind: config.KindString, Text: "draft"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	before := readVaultFile(t, root, "Alpha.md")
	markFileProvider(t, root)
	tree := snapshotTree(t, root)

	status, body := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	assertForbiddenResponse(t, propertyApplyAPIRoutePath, status, body)

	if got := readVaultFile(t, root, "Alpha.md"); got != before {
		t.Fatalf("Alpha.md was rewritten under a refused root")
	}
	if _, err := os.Stat(noteops.JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("stat journal root = %v, want it never created by a refused apply", err)
	}
	assertNoNewArtifacts(t, root, tree)
}

// The schema write patches brainnotes.toml, which is the configuration-write
// surface. It goes through the same transaction and the same refusal.
func TestSchemaApplyIsForbiddenUnderFileProviderRoot(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "status",
		Definition: &propertySchemaDefinition{
			Type:    config.KindString,
			Allowed: []string{"draft", "done", "archived"},
		},
	})
	before := readVaultFile(t, root, config.FileName)
	markFileProvider(t, root)
	tree := snapshotTree(t, root)

	status, body := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	assertForbiddenResponse(t, propertySchemaApplyAPIRoutePath, status, body)

	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("brainnotes.toml was rewritten under a refused root")
	}
	assertNoNewArtifacts(t, root, tree)
}

// Reading a synced vault stays fully available. The policy blocks writes, not
// the ability to look at what is there.
func TestReadEndpointsWorkUnderFileProviderRoot(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)
	markFileProvider(t, root)

	for _, route := range []string{
		propertiesAPIRoutePath,
		propertiesDetailAPIRoutePath + "?property=status",
		"/api/history",
	} {
		resp, err := http.Get(ts.URL + route) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body %q", route, resp.StatusCode, body)
		}
	}

	// Preview is a read too, so the workbench can still show what a change would
	// do even where it could not be applied.
	status, _ := postPropertyMutation(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken,
		propertyMutationRequest{
			Action:   propertyActionReplaceValue,
			Property: "status",
			Notes:    []string{"Alpha.md"},
			Match:    &properties.TypedValue{Kind: config.KindString, Text: "draft"},
			Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
		})
	if status != http.StatusOK {
		t.Fatalf("preview status = %d under a File Provider root, want 200", status)
	}
}

// snapshotTree records every path under root. Taken at the moment the root is
// marked, it is the baseline a refused mutation must not add to — an exact
// comparison rather than a list of forbidden names, so a staged temp file, a
// lock file, a journal entry or an empty destination folder are all caught
// without having to be predicted.
func snapshotTree(t *testing.T, root string) map[string]bool {
	t.Helper()
	paths := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths[path] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
	return paths
}

func assertNoNewArtifacts(t *testing.T, root string, before map[string]bool) {
	t.Helper()
	for path := range snapshotTree(t, root) {
		if !before[path] {
			t.Fatalf("%q appeared under a File Provider root; the refused mutation left an artifact behind", path)
		}
	}
}
