package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestPropertiesPreview_RenameCollisionBlocks(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, payload := postPropertyMutation(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken, propertyMutationRequest{
		Action:   propertyActionRename,
		Property: "status",
		NewName:  "tags",
		Notes:    []string{"Alpha.md"},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if payload.PreviewID != "" {
		t.Fatalf("a blocked plan must not issue a receipt: %q", payload.PreviewID)
	}
	if len(payload.Blockers) == 0 {
		t.Fatal("a collision must be reported as a blocker")
	}
	kinds := map[string]bool{}
	for _, blocker := range payload.Blockers {
		kinds[blocker.Kind] = true
	}
	if !kinds[propertyBlockerKeyConflict] {
		t.Fatalf("blockers = %#v, want a note key conflict", payload.Blockers)
	}
	if !kinds[propertyBlockerConfigConflict] {
		t.Fatalf("blockers = %#v, want a configuration conflict", payload.Blockers)
	}
	if len(payload.Previews) != 0 {
		t.Fatalf("a collision must not plan a rewrite: %#v", payload.Previews)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.tags]\ntype = \"string\"") {
		t.Fatal("the configuration must be untouched")
	}
}

func TestPropertiesPreviewApply_RenameUpdatesConfigAndNotes(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	req := propertyMutationRequest{
		Action:   propertyActionRename,
		Property: "status",
		NewName:  "state",
		Notes:    []string{"Alpha.md", "Beta.md"},
	}
	preview := previewProperty(t, ts.URL, req)
	if len(preview.ConfigDiff) == 0 {
		t.Fatal("a rename must preview its configuration diff")
	}
	if preview.NewName != "state" {
		t.Fatalf("new_name = %q", preview.NewName)
	}
	if preview.Affected != 2 {
		t.Fatalf("affected = %d, want 2", preview.Affected)
	}

	status, applied := applyProperty(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("a rename must be journalled")
	}
	for _, rel := range []string{"Alpha.md", "Beta.md"} {
		got := readVaultFile(t, root, rel)
		if !strings.Contains(got, "state: draft") || strings.Contains(got, "status:") {
			t.Fatalf("%s = %q", rel, got)
		}
	}
	cfgSource := readVaultFile(t, root, config.FileName)
	if !strings.Contains(cfgSource, "[frontmatter.properties.state]") {
		t.Fatalf("config = %q", cfgSource)
	}
	if strings.Contains(cfgSource, "[frontmatter.properties.status]") {
		t.Fatalf("the old definition survived: %q", cfgSource)
	}
}

func TestPropertiesPreview_ProjectsViolations(t *testing.T) {
	source := `[frontmatter.properties.status]
type = "string"
required = true
allowed = ["draft", "done"]
`
	ts, _, _ := newPropertyServer(t, source, propertyTestNotes())

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	if preview.Projected.Before.Missing != 0 {
		t.Fatalf("before missing = %d, want 0", preview.Projected.Before.Missing)
	}
	if preview.Projected.After.Missing != 1 {
		t.Fatalf("after missing = %d, want 1", preview.Projected.After.Missing)
	}
	if preview.Projected.After.Notes != 1 {
		t.Fatalf("after notes = %d, want 1", preview.Projected.After.Notes)
	}
}

func TestPropertiesPreview_RepairCanonicalizesAndReportsSkips(t *testing.T) {
	files := map[string]string{
		"Alpha.md": "---\nstatus: Draft\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\nstatus: shipped\ntitle: Beta\n---\nBeta body.\n",
	}
	ts, _, root := newPropertyServer(t, propertyTestConfigTOML, files)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionRepair,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md"},
	})
	if preview.Affected != 1 {
		t.Fatalf("affected = %d, want 1", preview.Affected)
	}
	if len(preview.Blockers) != 1 || preview.Blockers[0].Kind != propertyBlockerRepairSkip {
		t.Fatalf("blockers = %#v, want one advisory skip", preview.Blockers)
	}
	if preview.Blockers[0].RelPath != "Beta.md" {
		t.Fatalf("skip rel_path = %q", preview.Blockers[0].RelPath)
	}

	status, _ := applyProperty(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "status: draft") {
		t.Fatalf("Alpha.md = %q", got)
	}
	if got := readVaultFile(t, root, "Beta.md"); !strings.Contains(got, "status: shipped") {
		t.Fatalf("a skipped note must be left alone: %q", got)
	}
}

func TestPropertiesPreviewApply_ReplaceValueInList(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionReplaceValue,
		Property: "tags",
		Notes:    []string{"Alpha.md", "Beta.md"},
		Match:    &properties.TypedValue{Kind: config.KindString, Text: "shared"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "merged"},
	})
	if preview.Selected != 2 {
		t.Fatalf("selected = %d, want 2", preview.Selected)
	}
	if preview.Affected != 1 {
		t.Fatalf("affected = %d, want 1 (only Alpha.md holds the value)", preview.Affected)
	}

	if status, _ := applyProperty(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	got := readVaultFile(t, root, "Alpha.md")
	if !strings.Contains(got, "- merged") || strings.Contains(got, "- shared") {
		t.Fatalf("Alpha.md = %q", got)
	}
	if !strings.Contains(got, "- alpha") {
		t.Fatalf("the untouched list item vanished: %q", got)
	}
}

func TestPropertiesPreview_RejectsUnknownAction(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken, propertyMutationRequest{
		Action:   propertyActionKind("delete_everything"),
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertiesPreview_RejectsEscapingNotePath(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"../Outside.md", "/etc/passwd"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertiesPreview_BlocksNoteOutsideSnapshot(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, payload := postPropertyMutation(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md", "Missing.md"},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if payload.PreviewID != "" {
		t.Fatal("an unknown note must block the whole selection")
	}
	if len(payload.Blockers) != 1 || payload.Blockers[0].Kind != propertyBlockerNotFound {
		t.Fatalf("blockers = %#v", payload.Blockers)
	}
}

// TestPropertiesApply_RollsBackWhenOneWriteFails makes the second staged write
// impossible and asserts the first note is restored, because the workbench
// promises multi-note actions are all-or-nothing.
func TestPropertiesApply_RollsBackWhenOneWriteFails(t *testing.T) {
	files := map[string]string{
		"Alpha.md":         propertyTestNotes()["Alpha.md"],
		"locked/Beta.md":   propertyTestNotes()["Beta.md"],
		"locked/Keep.md":   "---\ntitle: Keep\n---\nKeep body.\n",
		"Untouched.md":     "---\ntitle: Untouched\n---\nUntouched body.\n",
		"locked/README.md": "---\ntitle: Readme\n---\nReadme body.\n",
	}
	ts, _, root := newPropertyServer(t, propertyTestConfigTOML, files)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md", "locked/Beta.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	before := readVaultFile(t, root, "Alpha.md")

	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status == http.StatusOK {
		t.Fatalf("apply should have failed, body %q", raw)
	}
	if got := readVaultFile(t, root, "Alpha.md"); got != before {
		t.Fatalf("Alpha.md was not rolled back:\n got %q\nwant %q", got, before)
	}
}

func TestProperties_HistoryAndUndoRestoreTheWrite(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	before := readVaultFile(t, root, "Alpha.md")
	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	status, applied := applyProperty(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 {
		t.Fatal("history is empty after a property write")
	}
	entry := history.Entries[0]
	if entry.ID != applied.JournalID {
		t.Fatalf("history id = %q, want %q", entry.ID, applied.JournalID)
	}
	if entry.Command != propertySetCommand {
		t.Fatalf("command = %q, want %q", entry.Command, propertySetCommand)
	}
	if entry.FileCount != 2 {
		t.Fatalf("file count = %d, want 2", entry.FileCount)
	}

	undoStatus, raw := postUndo(t, ts.URL, entry.ID)
	if undoStatus != http.StatusOK {
		t.Fatalf("undo status = %d, body %q", undoStatus, raw)
	}
	if got := readVaultFile(t, root, "Alpha.md"); got != before {
		t.Fatalf("undo did not restore Alpha.md:\n got %q\nwant %q", got, before)
	}
}

// A commit that could not put a displaced note back names the destination and
// the file holding its content. The API must forward that guidance instead of
// answering with a retryable conflict or a generic write error, because the
// recovery file is the only place the note still exists.
func TestPropertyTxnClassificationPlacementFailureSurfacesRecovery(t *testing.T) {
	recovery := &vault.TxnRecoveryError{
		Path:         "/vault/Alpha.md",
		RecoveryPath: "/vault/.txn-custody-123.tmp",
		Displaced:    true,
		Err:          &vault.TxnConflictError{Path: "/vault/Alpha.md", Expected: "missing", Actual: "file"},
	}

	for name, err := range map[string]error{
		"placement failure": recovery,
		"placement failure during rollback": &vault.TxnRollbackError{
			Path:         "/vault/Alpha.md",
			Err:          recovery,
			Failed:       []string{"/vault/Beta.md"},
			RollbackErrs: []error{errors.New("boom")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			classified := noteops.ClassifyTxnError("properties: write", err)
			if got := noteops.CodeOf(classified); got != noteops.CodeInternal {
				t.Fatalf("code = %q, want %q", got, noteops.CodeInternal)
			}
			if got := noteops.HTTPStatusForError(classified); got != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", got, http.StatusInternalServerError)
			}
			message := noteops.MessageOf(classified)
			if !strings.Contains(message, recovery.Path) {
				t.Fatalf("message must name the destination, got %q", message)
			}
			if !strings.Contains(message, recovery.RecoveryPath) {
				t.Fatalf("message must name the recovery path, got %q", message)
			}
			if message == "properties: write" {
				t.Fatalf("the generic message must not replace the recovery guidance, got %q", message)
			}
		})
	}
}

func TestPropertyTxnClassificationTreatsFailedRollbackOverConflictAsInternal(t *testing.T) {
	err := &vault.TxnRollbackError{
		Path: "/vault/Alpha.md",
		Err: &vault.TxnConflictError{
			Path:     "/vault/Alpha.md",
			Expected: "old hash",
			Actual:   "new hash",
		},
		Failed:       []string{"/vault/Beta.md"},
		RollbackErrs: []error{errors.New("permission denied")},
	}

	classified := noteops.ClassifyTxnError("properties: write", err)
	if got := noteops.CodeOf(classified); got != noteops.CodeInternal {
		t.Fatalf("code = %q, want %q", got, noteops.CodeInternal)
	}
	if got := noteops.HTTPStatusForError(classified); got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", got, http.StatusInternalServerError)
	}
	if message := noteops.MessageOf(classified); !strings.Contains(message, "inspect 'git diff' to recover") {
		t.Fatalf("message must preserve rollback recovery guidance, got %q", message)
	}
}

// A lock wait that timed out changed nothing, so the property surface answers it
// as the retryable conflict the shared classifier gives every other writer,
// keeping the "retry" sentence instead of a generic write failure.
func TestPropertyTxnClassificationTreatsLockBusyAsRetryableConflict(t *testing.T) {
	err := &vault.TxnLockBusyError{Path: "/vault/Alpha.md"}

	classified := noteops.ClassifyTxnError("properties: write", err)
	if got := noteops.CodeOf(classified); got != noteops.CodeConflict {
		t.Fatalf("code = %q, want %q", got, noteops.CodeConflict)
	}
	if got := noteops.HTTPStatusForError(classified); got != http.StatusConflict {
		t.Fatalf("status = %d, want %d", got, http.StatusConflict)
	}
	if message := noteops.MessageOf(classified); !strings.Contains(message, "retry") {
		t.Fatalf("message must keep the retry advice, got %q", message)
	}
}
