package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/properties"
)

func TestPropertiesAcceptance_ConcurrentNoteEdits(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	staleHash := noteHash(t, root, "Alpha.md")
	edited := "---\nreviewed: 2026-01-02\nstatus: draft\ntags:\n    - alpha\n    - shared\ntitle: Alpha\n---\nEdited in the editor.\n"
	writeTestFile(t, root, "Alpha.md", edited)

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: staleHash,
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusConflict {
		t.Fatalf("stale immediate edit status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if readVaultFile(t, root, "Alpha.md") != edited {
		t.Fatal("a refused immediate edit must not overwrite the editor's work")
	}

	reread := propertyDetail(t, ts, "property=status&q=Alpha")
	if len(reread.Rows) != 1 {
		t.Fatalf("rows = %v, want just Alpha.md", propertyRowPaths(reread))
	}
	freshHash := reread.Rows[0].Hash
	if freshHash == staleHash {
		t.Fatal("the re-read handed back the hash the editor invalidated")
	}

	okStatus, _ := postPropertyMutation(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: freshHash,
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if okStatus != http.StatusOK {
		t.Fatalf("immediate edit status = %d after re-reading the note", okStatus)
	}
	if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "status: done") {
		t.Fatalf("Alpha.md = %q", got)
	}

	batch := propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md"},
	}
	preview := previewProperty(t, ts.URL, batch)
	writeTestFile(t, root, "Beta.md", "---\nstatus: draft\ntitle: Beta\n---\nEdited in the editor.\n")

	applyStatus, applyRaw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if applyStatus != http.StatusConflict {
		t.Fatalf("stale batch apply status = %d, want %d", applyStatus, http.StatusConflict)
	}
	requireErrorCode(t, applyRaw, string(noteops.CodePreviewStale))
	if !strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("a refused batch must write nothing at all, not even the untouched note")
	}

	second := previewProperty(t, ts.URL, batch)
	if status, _ := applyProperty(t, ts.URL, second.PreviewID); status != http.StatusOK {
		t.Fatalf("re-previewed apply status = %d", status)
	}
	for _, rel := range []string{"Alpha.md", "Beta.md"} {
		if strings.Contains(readVaultFile(t, root, rel), "status:") {
			t.Fatalf("%s still carries the property", rel)
		}
	}
}

// TestPropertiesAcceptance_DetailPageServesWorkbenchAffordances guards the
// markup the keyboard, preview, and history interactions bind to. The behavior
// itself is exercised by the browser test suite; only the server renders these
// hooks, so a template regression would otherwise leave the page inert with
// every script still passing.
func TestPropertiesAcceptance_DetailPageServesWorkbenchAffordances(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig,
		Options{CSRFToken: propertyTestToken})

	resp, err := http.Get(ts.URL + "/properties/status") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET /properties/status: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	for _, want := range []string{
		`data-property-csrf="` + propertyTestToken + `"`,
		"properties-keyboard-hint",
		"data-property-selection-count",
		"data-property-select-all",
		"data-property-clear-selection",
		`data-property-action="set"`,
		`data-property-action="replace_value"`,
		`data-property-action="unset"`,
		`data-property-action="rename_property"`,
		`data-property-action="repair"`,
		"data-property-editor",
		"data-property-dialog-host",
		"data-property-toast-host",
		"data-property-schema-type",
		"data-property-schema-items",
		"data-property-schema-required",
		"data-property-schema-allowed",
		"data-property-schema-save",
		"data-property-schema-delete",
		"data-property-schema-adopt",
		"data-property-config-invalid",
		"data-property-config-restart",
		`id="property-history"`,
		"data-history-panel",
		"data-history-list",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail page missing %q", want)
		}
	}
}
