package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
)

// These tests are journeys, not handler checks: each one walks the whole path a
// person takes in the workbench — read the catalog or the detail view, preview,
// apply, then read again — so a regression that only shows up between endpoints
// is caught. Per-handler behavior lives in the other property test files.

// propertyDetail reads the endpoint the detail page renders from, so a journey
// can assert what the user would see next instead of only what landed on disk.
func propertyDetail(t *testing.T, ts *httptest.Server, query string) propertyDetailResponse {
	t.Helper()

	var payload propertyDetailResponse
	resp := getJSON(t, ts, propertiesDetailAPIRoutePath+"?"+query, &payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail %q status = %d", query, resp.StatusCode)
	}
	return payload
}

func propertyCatalog(t *testing.T, ts *httptest.Server) propertiesResponse {
	t.Helper()

	var payload propertiesResponse
	resp := getJSON(t, ts, propertiesAPIRoutePath, &payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET catalog status = %d", resp.StatusCode)
	}
	return payload
}

func propertySummaryFor(catalog propertiesResponse, name string) *propertySummary {
	for i := range catalog.Properties {
		if catalog.Properties[i].Name == name {
			return &catalog.Properties[i]
		}
	}
	return nil
}

func propertyValueNotes(detail propertyDetailResponse, text string) int {
	for _, value := range detail.Values {
		if value.Value.Text == text {
			return value.Notes
		}
	}
	return 0
}

func propertyRowPaths(detail propertyDetailResponse) []string {
	out := make([]string, 0, len(detail.Rows))
	for _, row := range detail.Rows {
		out = append(out, row.RelPath)
	}
	return out
}

func propertyBlockerKinds(blockers []propertyBlocker) map[string]bool {
	kinds := make(map[string]bool, len(blockers))
	for _, blocker := range blockers {
		kinds[blocker.Kind] = true
	}
	return kinds
}

const propertyAcceptanceStatusConfig = `[frontmatter.properties.status]
type = "string"
allowed = ["wip", "doing", "done"]
`

const propertyAcceptanceRequiredConfig = `[frontmatter.properties.reviewed]
type = "date"
required = true
`

// TestPropertiesAcceptance_MergeValueThenUndo is the headline journey: a stray
// value is merged into the one the user keeps, the merge is visible immediately
// in the aggregate statistics, and undo puts the vault back exactly.
func TestPropertiesAcceptance_MergeValueThenUndo(t *testing.T) {
	files := map[string]string{
		"Alpha.md": "---\nstatus: wip\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\nstatus: wip\ntitle: Beta\n---\nBeta body.\n",
		"Gamma.md": "---\nstatus: done\ntitle: Gamma\n---\nGamma body.\n",
	}
	ts, _, root := newPropertyServer(t, propertyAcceptanceStatusConfig, files)

	before := make(map[string]string, len(files))
	for rel := range files {
		before[rel] = readVaultFile(t, root, rel)
	}

	opened := propertyDetail(t, ts, "property=status")
	if propertyValueNotes(opened, "wip") != 2 {
		t.Fatalf("values = %+v, want wip in 2 notes", opened.Values)
	}

	req := propertyMutationRequest{
		Action:   propertyActionReplaceValue,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md", "Gamma.md"},
		Match:    &properties.TypedValue{Kind: config.KindString, Text: "wip"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "doing"},
	}
	preview := previewProperty(t, ts.URL, req)
	if preview.Selected != 3 || preview.Affected != 2 {
		t.Fatalf("counts = selected %d affected %d, want 3 and 2", preview.Selected, preview.Affected)
	}

	status, applied := applyProperty(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("a merge must be journalled so it can be undone")
	}

	merged := propertyDetail(t, ts, "property=status")
	if propertyValueNotes(merged, "wip") != 0 {
		t.Fatalf("wip survived the merge: %+v", merged.Values)
	}
	if propertyValueNotes(merged, "doing") != 2 {
		t.Fatalf("doing = %+v, want it in 2 notes", merged.Values)
	}
	if got := readVaultFile(t, root, "Gamma.md"); got != before["Gamma.md"] {
		t.Fatalf("a selected note that did not hold the value was rewritten: %q", got)
	}

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].ID != applied.JournalID {
		t.Fatalf("history = %+v, want the merge on top", history.Entries)
	}
	if history.Entries[0].Command != propertyReplaceCommand {
		t.Fatalf("command = %q, want %q", history.Entries[0].Command, propertyReplaceCommand)
	}
	if history.Entries[0].FileCount != 2 {
		t.Fatalf("file count = %d, want 2", history.Entries[0].FileCount)
	}

	undoStatus, raw := postUndo(t, ts.URL, applied.JournalID)
	if undoStatus != http.StatusOK {
		t.Fatalf("undo status = %d, body %q", undoStatus, raw)
	}
	for rel, want := range before {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("undo did not restore %s:\n got %q\nwant %q", rel, got, want)
		}
	}
	restored := propertyDetail(t, ts, "property=status")
	if propertyValueNotes(restored, "wip") != 2 {
		t.Fatalf("the workbench still shows the merged value after undo: %+v", restored.Values)
	}
}

// TestPropertiesAcceptance_AddDefinitionThenRepairSeparately pins the deliberate
// two-step shape of adopting a definition: writing the definition never touches
// a note, and the notes it now flags are fixed by a second action with its own
// preview and its own receipt.
func TestPropertiesAcceptance_AddDefinitionThenRepairSeparately(t *testing.T) {
	files := map[string]string{
		"Alpha.md": "---\ncategories:\n    - Reference\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\ncategories:\n    - Project\ntitle: Beta\n---\nBeta body.\n",
	}
	ts, _, root := newPropertyServer(t, propertyTestConfigTOML, files)

	catalog := propertyCatalog(t, ts)
	summary := propertySummaryFor(catalog, "categories")
	if summary == nil {
		t.Fatalf("catalog = %+v, want the undeclared categories property listed", catalog.Properties)
	}
	if summary.Configured {
		t.Fatal("categories must start out undeclared")
	}
	if summary.Proposal == nil {
		t.Fatal("a unanimous undeclared property must come with an inferred definition")
	}

	schemaPreview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "categories",
		Definition: &propertySchemaDefinition{
			Type:    config.KindList,
			Items:   config.KindString,
			Allowed: []string{"reference", "project"},
		},
	})
	if len(schemaPreview.ConfigDiff) == 0 {
		t.Fatal("a definition change must preview its configuration diff")
	}
	if len(schemaPreview.Previews) != 0 {
		t.Fatalf("a definition change must not plan note rewrites: %+v", schemaPreview.Previews)
	}
	if schemaPreview.Affected != 2 {
		t.Fatalf("affected = %d, want the 2 notes the definition would govern", schemaPreview.Affected)
	}
	if schemaPreview.Projected.After.Value != 2 {
		t.Fatalf("projection = %+v, want the allow-list to flag both notes", schemaPreview.Projected)
	}

	status, appliedSchema := applyPropertySchema(t, ts.URL, schemaPreview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("schema apply status = %d", status)
	}
	if appliedSchema.Generation <= schemaPreview.Generation {
		t.Fatalf("generation = %d, want it past the preview's %d",
			appliedSchema.Generation, schemaPreview.Generation)
	}
	if got := readVaultFile(t, root, config.FileName); !strings.Contains(got, "[frontmatter.properties.categories]") {
		t.Fatalf("config = %q", got)
	}
	for rel, want := range files {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("a definition change rewrote %s:\n got %q\nwant %q", rel, got, want)
		}
	}

	flagged := propertyDetail(t, ts, "property=categories&filter=violations")
	if len(flagged.Rows) != 2 {
		t.Fatalf("violating rows = %v, want both notes", propertyRowPaths(flagged))
	}

	repair := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionRepair,
		Property: "categories",
		Notes:    []string{"Alpha.md", "Beta.md"},
	})
	if repair.PreviewID == schemaPreview.PreviewID {
		t.Fatal("the repair must carry its own receipt")
	}
	if repair.Affected != 2 {
		t.Fatalf("repair affected = %d, want 2", repair.Affected)
	}

	if status, _ := applyProperty(t, ts.URL, repair.PreviewID); status != http.StatusOK {
		t.Fatalf("repair apply status = %d", status)
	}
	if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "- reference") {
		t.Fatalf("Alpha.md = %q", got)
	}
	if got := readVaultFile(t, root, "Beta.md"); !strings.Contains(got, "- project") {
		t.Fatalf("Beta.md = %q", got)
	}
	clean := propertyDetail(t, ts, "property=categories&filter=violations")
	if len(clean.Rows) != 0 {
		t.Fatalf("violations remain after the repair: %v", propertyRowPaths(clean))
	}
}

// TestPropertiesAcceptance_FillsMissingRequiredValues covers the coverage gap
// workflow: find the notes that are missing a required property, set it on all
// of them at once, and watch the projection promise what the re-read confirms.
func TestPropertiesAcceptance_FillsMissingRequiredValues(t *testing.T) {
	files := map[string]string{
		"Alpha.md": "---\nreviewed: 2026-01-02\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\ntitle: Beta\n---\nBeta body.\n",
		"Gamma.md": "---\ntitle: Gamma\n---\nGamma body.\n",
	}
	ts, _, root := newPropertyServer(t, propertyAcceptanceRequiredConfig, files)

	missing := propertyDetail(t, ts, "property=reviewed&filter=missing")
	if got := propertyRowPaths(missing); len(got) != 2 || got[0] != "Beta.md" || got[1] != "Gamma.md" {
		t.Fatalf("missing rows = %v, want Beta.md and Gamma.md", got)
	}
	for _, row := range missing.Rows {
		if row.Present {
			t.Fatalf("row %+v is in the missing filter but claims a value", row)
		}
	}
	if missing.Property.MissingRequired != 2 {
		t.Fatalf("missing_required = %d, want 2", missing.Property.MissingRequired)
	}

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "reviewed",
		Notes:    []string{"Beta.md", "Gamma.md"},
		Value:    &properties.TypedValue{Kind: config.KindDate, Text: "2026-02-01"},
	})
	if preview.Projected.Before.Missing != 2 {
		t.Fatalf("projection before = %+v, want 2 missing", preview.Projected.Before)
	}
	if preview.Projected.After.Missing != 0 {
		t.Fatalf("projection after = %+v, want the gap closed", preview.Projected.After)
	}

	if status, _ := applyProperty(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	for _, rel := range []string{"Beta.md", "Gamma.md"} {
		if got := readVaultFile(t, root, rel); !strings.Contains(got, "reviewed: 2026-02-01") {
			t.Fatalf("%s = %q", rel, got)
		}
	}
	if got := readVaultFile(t, root, "Alpha.md"); got != files["Alpha.md"] {
		t.Fatalf("a note that already had the value was rewritten: %q", got)
	}

	filled := propertyDetail(t, ts, "property=reviewed&filter=missing")
	if len(filled.Rows) != 0 {
		t.Fatalf("missing rows remain: %v", propertyRowPaths(filled))
	}
	if filled.Property.MissingRequired != 0 {
		t.Fatalf("missing_required = %d, want 0", filled.Property.MissingRequired)
	}
	if filled.Property.Coverage != 1 {
		t.Fatalf("coverage = %v, want 1", filled.Property.Coverage)
	}
}

// TestPropertiesAcceptance_RenameCollisionRefusedThenSucceeds walks the recovery
// path a collision forces: the blocked plan writes nothing and hands out no
// receipt, and the same rename under a free name goes through untouched.
func TestPropertiesAcceptance_RenameCollisionRefusedThenSucceeds(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	beforeAlpha := readVaultFile(t, root, "Alpha.md")
	beforeConfig := readVaultFile(t, root, config.FileName)

	status, collision := postPropertyMutation(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken,
		propertyMutationRequest{
			Action:   propertyActionRename,
			Property: "status",
			NewName:  "tags",
			Notes:    []string{"Alpha.md", "Beta.md"},
		})
	if status != http.StatusOK {
		t.Fatalf("preview status = %d", status)
	}
	if collision.PreviewID != "" {
		t.Fatalf("a blocked plan must not issue a receipt: %q", collision.PreviewID)
	}
	kinds := propertyBlockerKinds(collision.Blockers)
	if !kinds[propertyBlockerKeyConflict] || !kinds[propertyBlockerConfigConflict] {
		t.Fatalf("blockers = %#v, want both the note and configuration conflicts", collision.Blockers)
	}

	applyStatus, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken, propertyApplyRequest{})
	if applyStatus != http.StatusConflict {
		t.Fatalf("apply status = %d, want %d", applyStatus, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if readVaultFile(t, root, "Alpha.md") != beforeAlpha {
		t.Fatal("a refused rename must leave the notes alone")
	}
	if readVaultFile(t, root, config.FileName) != beforeConfig {
		t.Fatal("a refused rename must leave the configuration alone")
	}

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionRename,
		Property: "status",
		NewName:  "state",
		Notes:    []string{"Alpha.md", "Beta.md"},
	})
	if len(preview.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want a clean plan under a free name", preview.Blockers)
	}
	if status, _ := applyProperty(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	catalog := propertyCatalog(t, ts)
	if propertySummaryFor(catalog, "status") != nil {
		t.Fatalf("the old name survived the rename: %+v", catalog.Properties)
	}
	renamed := propertySummaryFor(catalog, "state")
	if renamed == nil || renamed.NoteCount != 2 || !renamed.Configured {
		t.Fatalf("renamed summary = %+v, want a declared property on 2 notes", renamed)
	}
}

// TestPropertiesAcceptance_ExternalConfigReload covers editing brainnotes.toml
// outside the browser while the page is open: the status endpoint reports the
// new generation, outstanding receipts are refused, an unreadable file blocks
// every write, and fixing the file restores writes without a restart.
func TestPropertiesAcceptance_ExternalConfigReload(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	req := propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	}
	stale := previewProperty(t, ts.URL, req)

	rewriteConfig(t, root, propertyTestConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")
	statusCode, reloaded := getConfigStatus(t, ts.URL)
	if statusCode != http.StatusOK {
		t.Fatalf("config status = %d", statusCode)
	}
	if reloaded.Status != ReloadOK || !reloaded.Writable {
		t.Fatalf("status = %#v, want a valid writable configuration", reloaded)
	}
	if reloaded.Generation <= stale.Generation {
		t.Fatalf("generation = %d, want it past the preview's %d", reloaded.Generation, stale.Generation)
	}

	applyStatus, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: stale.PreviewID})
	if applyStatus != http.StatusConflict {
		t.Fatalf("stale apply status = %d, want %d", applyStatus, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("a receipt from the previous generation must not write")
	}

	fresh := previewProperty(t, ts.URL, req)
	if fresh.Generation != reloaded.Generation {
		t.Fatalf("preview generation = %d, want the reloaded %d", fresh.Generation, reloaded.Generation)
	}
	if status, _ := applyProperty(t, ts.URL, fresh.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if !strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("the re-previewed edit did not land")
	}

	rewriteConfig(t, root, "[frontmatter.properties.status\ntype = ")
	_, broken := getConfigStatus(t, ts.URL)
	if broken.Status != ReloadInvalid || broken.Writable {
		t.Fatalf("status = %#v, want an invalid unwritable configuration", broken)
	}
	if broken.Error == "" {
		t.Fatal("an invalid configuration must report why")
	}
	refusedStatus, refusedRaw := postProperty(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken,
		propertyMutationRequest{
			Action:   propertyActionUnset,
			Property: "status",
			Notes:    []string{"Beta.md"},
		})
	if refusedStatus != http.StatusConflict {
		t.Fatalf("refused status = %d, want %d", refusedStatus, http.StatusConflict)
	}
	requireErrorCode(t, refusedRaw, string(noteops.CodeConflict))

	rewriteConfig(t, root, propertyTestConfigTOML)
	_, healed := getConfigStatus(t, ts.URL)
	if healed.Status != ReloadOK || !healed.Writable {
		t.Fatalf("status = %#v, want writes restored without a restart", healed)
	}
	recovered := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Beta.md"},
	})
	if status, _ := applyProperty(t, ts.URL, recovered.PreviewID); status != http.StatusOK {
		t.Fatalf("recovered apply status = %d", status)
	}
	if strings.Contains(readVaultFile(t, root, "Beta.md"), "status:") {
		t.Fatal("the write after the configuration was fixed did not land")
	}
}

// TestPropertiesAcceptance_ConcurrentNoteEdits covers a note changing in an
// editor while the workbench holds a stale view of it. Both write paths refuse:
// the immediate edit on its note hash, the batch on its receipt. Re-reading is
// the whole recovery.
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
