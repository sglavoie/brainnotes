package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// TestViewsPreviewApply_CreateWritesConfigurationOnly is the create case the
// client auto-chains: nothing exists to destroy, so Created is true and the diff
// is a receipt rather than a question.
func TestViewsPreviewApply_CreateWritesConfigurationOnly(t *testing.T) {
	ts, srv, root := newViewsFixtureServer(t)

	notesBefore := map[string]string{
		"Alpha.md": readVaultFile(t, root, "Alpha.md"),
		"Beta.md":  readVaultFile(t, root, "Beta.md"),
	}

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done and not folder:Archive", Sort: "-modified", Limit: 20},
	})
	if !preview.Created {
		t.Fatal("a view that does not exist yet must preview as created")
	}
	if len(preview.ConfigDiff) == 0 {
		t.Fatal("a view change must preview a configuration diff")
	}
	if preview.JournalID != "" {
		t.Fatal("a preview must not journal anything")
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a preview must not write the configuration")
	}
	fresh := findViewSummary(t, preview.Views, "Fresh")
	if !fresh.Enabled || fresh.Limit != 20 || fresh.Sort != "-modified" {
		t.Fatalf("previewed view = %#v", fresh)
	}

	generationBefore := srv.configState.Status().Generation
	status, applied := applyView(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("a view change must be journalled")
	}
	if applied.Generation <= generationBefore {
		t.Fatalf("generation = %d, want more than %d", applied.Generation, generationBefore)
	}
	if !strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("the view was not written")
	}
	for rel, want := range notesBefore {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("%s changed during a view apply:\n got %q\nwant %q", rel, got, want)
		}
	}
	findViewSummary(t, getViews(t, ts.URL).Views, "Fresh")

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != viewsUpsertCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, viewsUpsertCommand)
	}
	if history.Entries[0].FileCount != 1 {
		t.Fatalf("file count = %d, want 1", history.Entries[0].FileCount)
	}
}

// TestViewsPreviewApply_OverwriteDropsCommentsInsideTheBlock is the concrete
// reason overwrite must confirm the diff while create may auto-chain: an upsert
// replaces the whole block, so a comment inside it is destroyed.
func TestViewsPreviewApply_OverwriteDropsCommentsInsideTheBlock(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Commented",
		View: &viewDefinition{Query: "tag:shared"},
	})
	if preview.Created {
		t.Fatal("an existing view must not preview as created")
	}
	if status, _ := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	source := readVaultFile(t, root, config.FileName)
	if strings.Contains(source, "# Why this view exists.") {
		t.Fatal("an overwrite is expected to drop the comment inside the block")
	}
	if !strings.Contains(source, `query = "tag:shared"`) {
		t.Fatal("the overwrite did not land")
	}
}

// TestViewsPreviewApply_RenamePreservesCommentsInsideTheBlock is the other half
// of that split: a rename rewrites one line, so the block survives intact.
func TestViewsPreviewApply_RenamePreservesCommentsInsideTheBlock(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:      viewOpRename,
		Name:    "Commented",
		NewName: "Renamed",
	})
	if status, applied := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	} else if applied.NewName != "Renamed" {
		t.Fatalf("new_name = %q", applied.NewName)
	}

	source := readVaultFile(t, root, config.FileName)
	if !strings.Contains(source, "# Why this view exists.") {
		t.Fatal("a rename must preserve the comment inside the block")
	}
	if strings.Contains(source, `name = "Commented"`) {
		t.Fatal("the old name survived the rename")
	}
	findViewSummary(t, getViews(t, ts.URL).Views, "Renamed")

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != viewsRenameCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, viewsRenameCommand)
	}
}

func TestViewsPreviewApply_DeleteRemovesTheBlock(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{Op: viewOpDelete, Name: "Rotted"})
	for _, view := range preview.Views {
		if strings.EqualFold(view.Name, "Rotted") {
			t.Fatal("the preview still lists the deleted view")
		}
	}
	if status, _ := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Rotted"`) {
		t.Fatal("the view survived the delete")
	}

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != viewsDeleteCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, viewsDeleteCommand)
	}
}

func TestViewsApply_UndoRestoresTheConfiguration(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	before := readVaultFile(t, root, config.FileName)
	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	status, applied := applyView(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	undoStatus, raw := postUndo(t, ts.URL, applied.JournalID)
	if undoStatus != http.StatusOK {
		t.Fatalf("undo status = %d, body %q", undoStatus, raw)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("undo did not restore the configuration:\n got %q\nwant %q", got, before)
	}
}

// TestViewsApply_RejectsStaleConfiguration is the three-writer failure mode:
// brainnotes.toml moved under the receipt, so the loser is a 409 that names the
// remedy rather than a generic error.
func TestViewsApply_RejectsStaleConfiguration(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	rewriteConfig(t, root, viewsTestConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if !strings.Contains(raw, "re-check the diff") {
		t.Fatalf("message = %q, want the re-check remedy", raw)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a stale apply must write nothing")
	}
}

// The overlay never assigns `views`, and it still invalidates this receipt:
// what the apply re-checks is the configuration, and the configuration is both
// files.
func TestViewsApply_RejectsAnOverlayThatAppeared(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	writeLocalOverlay(t, root, "[open]\neditor = \"hx\"\n")

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusConflict, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a stale apply must write nothing")
	}
}

// The inverse of the two tests above, and the one an overlay-carrying vault
// depends on every day: an overlay that is present and did not move must not
// block anything. It is worth its own test because the two halves of that
// comparison are computed by different packages from different reads — the pin's
// digest comes from config's Fingerprint, the plan's from vault.HashBytes over a
// fresh read — and the presence flag is bookkeeping neither of them can check.
// Every other overlay test here asserts a refusal, so a drift between those two
// derivations would leave them all green while making a configuration write
// impossible in exactly the vaults the overlay was built for.
func TestViewsApply_SucceedsWithAnUnchangedOverlayPresent(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	// Written before the preview and published through the status poll, so the
	// pin the apply carries names an overlay that really exists.
	writeLocalOverlay(t, root, "[open]\neditor = \"hx\"\n")
	if status, payload := getConfigStatus(t, ts.URL); status != http.StatusOK || !payload.LocalPresent {
		t.Fatalf("config status = %d, local_present = %t, want 200 and true", status, payload.LocalPresent)
	}

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusOK, raw)
	}
	if !strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("the view was not written")
	}
	if got := readVaultFile(t, root, config.LocalFileName); got != "[open]\neditor = \"hx\"\n" {
		t.Fatalf("the overlay was disturbed by a portable write:\n%s", got)
	}
}

// TestViewsApply_RejectsAConcurrentViewWrite is the same race between two view
// writers rather than between a view and a property writer.
func TestViewsApply_RejectsAConcurrentViewWrite(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	first := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	second := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Other",
		View: &viewDefinition{Query: "tag:alpha"},
	})
	if status, _ := applyView(t, ts.URL, first.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d", status)
	}

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: second.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("second apply status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if !strings.Contains(raw, "re-check the diff") {
		t.Fatalf("message = %q, want the re-check remedy", raw)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Other"`) {
		t.Fatal("the losing writer must write nothing")
	}
}

func TestViewsApply_RejectsReusedReceipt(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	if status, _ := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d", status)
	}
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("replay status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

func TestViewsApply_RejectsSchemaReceipt(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("a mismatched receipt must write nothing")
	}
}

func TestPropertySchemaApply_RejectsViewReceipt(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a mismatched receipt must write nothing")
	}
}

func TestViewsApply_RefusedWhileConfigurationInvalid(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	rewriteConfig(t, root, "[frontmatter.properties.status\ntype = ")

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
}
