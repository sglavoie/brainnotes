package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// TestSettingsApply_WritesTheValueAndBumpsTheGeneration is the whole round trip
// for each of the four kinds.
func TestSettingsApply_WritesTheValueAndBumpsTheGeneration(t *testing.T) {
	cases := []struct {
		name  string
		req   settingMutationRequest
		wants string
	}{
		{
			name:  "string",
			req:   setStringSetting("capture.heading", "## Later"),
			wants: `heading = "## Later"`,
		},
		{
			name: "int",
			req: settingMutationRequest{Op: settingOpSet, Key: "cockpit.daily_limit",
				Value: &settingValue{Kind: string(config.SettingInt), Number: 12}},
			wants: "daily_limit = 12",
		},
		{
			name: "bool",
			req: settingMutationRequest{Op: settingOpSet, Key: "cockpit.sensitive_scan",
				Value: &settingValue{Kind: string(config.SettingBool), Flag: true}},
			wants: "sensitive_scan = true",
		},
		{
			name: "string_list",
			req: settingMutationRequest{Op: settingOpSet, Key: "brief.exclude_folders",
				Value: &settingValue{Kind: string(config.SettingStringList), Items: []string{"Archive", "Daily"}}},
			wants: `exclude_folders = ["Archive", "Daily"]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, _, root := newSettingsServer(t)

			preview := previewSetting(t, ts.URL, tc.req)
			status, payload, raw := applySetting(t, ts.URL, preview.PreviewID)
			if status != http.StatusOK {
				t.Fatalf("apply status = %d, body %q", status, raw)
			}
			if payload.JournalID == "" {
				t.Fatalf("apply recorded no journal entry: %q", raw)
			}
			if payload.Generation <= preview.Generation {
				t.Fatalf("generation = %d, want greater than %d", payload.Generation, preview.Generation)
			}
			if got := readVaultFile(t, root, config.FileName); !strings.Contains(got, tc.wants) {
				t.Fatalf("configuration does not hold %q:\n%s", tc.wants, got)
			}

			_, listed := getSettings(t, ts.URL)
			summary := settingByKey(t, decodeSettingsResponse(t, listed), tc.req.Key)
			if !summary.Assigned || summary.Layer != string(config.LayerPortable) {
				t.Fatalf("%s = %#v, want assigned by the portable layer", tc.req.Key, summary)
			}
		})
	}
}

// TestSettingsApply_UnsetRemovesTheAssignment proves the value goes back to its
// default rather than to a written-out zero.
func TestSettingsApply_UnsetRemovesTheAssignment(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	preview := previewSetting(t, ts.URL,
		settingMutationRequest{Op: settingOpUnset, Key: "capture.heading"})
	status, payload, raw := applySetting(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("unset apply status = %d, body %q", status, raw)
	}
	if got := readVaultFile(t, root, config.FileName); strings.Contains(got, "heading =") {
		t.Fatalf("unset left the assignment in place:\n%s", got)
	}
	if summary := settingByKey(t, payload, "capture.heading"); summary.Assigned {
		t.Fatalf("capture.heading still reported as assigned: %#v", summary)
	}
}

// TestSettingsApply_IsUndoable is half of "edited, undone": the journal entry
// the apply reports restores the previous configuration byte for byte.
func TestSettingsApply_IsUndoable(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	before := readVaultFile(t, root, config.FileName)
	preview := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))
	status, payload, raw := applySetting(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body %q", status, raw)
	}

	undoStatus, undoBody := postUndo(t, ts.URL, payload.JournalID)
	if undoStatus != http.StatusOK {
		t.Fatalf("undo status = %d, body %q", undoStatus, undoBody)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("undo did not restore the configuration:\nwant %q\ngot  %q", before, got)
	}
}

func TestSettingsApply_RequiresAReceipt(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, raw := postProperty(t, ts.URL, settingsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{})
	if status != http.StatusConflict && status != http.StatusBadRequest {
		t.Fatalf("receiptless apply status = %d, body %q", status, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))

	status, raw = postProperty(t, ts.URL, settingsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: "0123456789abcdef"})
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if status == http.StatusOK {
		t.Fatalf("an invented receipt was accepted")
	}
}

// TestSettingsApply_RefusesAReplay proves the receipt is one-use.
func TestSettingsApply_RefusesAReplay(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	preview := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))
	if status, _, raw := applySetting(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d, body %q", status, raw)
	}
	status, _, raw := applySetting(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("the same receipt applied twice")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

// TestSettingsApply_RefusesAReceiptFromAnotherFamily proves the preview kinds
// cannot be crossed even by a caller holding a valid identifier.
func TestSettingsApply_RefusesAReceiptFromAnotherFamily(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	preview := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status == http.StatusOK {
		t.Fatalf("a settings receipt was redeemed at the views endpoint")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

// TestSettingsApply_RefusesAPreviewTheFileMovedPast is the stale-configuration
// path: brainnotes.toml has four independent writers and the loser is refused
// rather than overwriting what it never saw.
func TestSettingsApply_RefusesAPreviewTheFileMovedPast(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	preview := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))
	external := settingsTestConfigTOML + "\n[attachments]\ndir = \"Files\"\n"
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(external), 0o644); err != nil {
		t.Fatalf("external edit: %v", err)
	}

	status, _, raw := applySetting(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("apply wrote over an external edit")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if got := readVaultFile(t, root, config.FileName); got != external {
		t.Fatalf("the external edit was not preserved:\n%s", got)
	}
}

// TestSettingsApply_NamesTheBusyVaultRatherThanTheStaleDiff bounds the one
// rewriting settingsWriteError does. A write refused while another process holds
// the Git safety lock is a conflict too, and it is not this file changing: the
// remedy is to wait, the receipt is still good, and a sentence telling the person
// to re-read a diff that did not move would send them looking for a change
// nobody made.
func TestSettingsApply_NamesTheBusyVaultRatherThanTheStaleDiff(t *testing.T) {
	ts, _, root := newSettingsServer(t)
	startContentHistory(t, root)

	before := readVaultFile(t, root, config.FileName)
	preview := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))
	holdSafetyLock(t, root)

	status, _, raw := applySetting(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("apply ran while the safety lock was held: %q", raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if !strings.Contains(raw, busyVaultRemedyWord) {
		t.Fatalf("the refusal does not say the vault is busy: %q", raw)
	}
	if strings.Contains(raw, "re-check the diff") {
		t.Fatalf("a busy vault was reported as a changed configuration: %q", raw)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("a refused apply changed the configuration:\n%s", got)
	}
}

// TestConfigWriteErrors_LeaveAFailedRollbackSayingSo is the other half of that
// boundary, and the one no request can provoke: a transaction whose precondition
// failed partway through the commit and whose compensating rollback failed too
// unwraps to that same conflict, so a predicate that asked only IsTxnConflict
// would answer a vault in a mixed state with "re-check the diff" — a sentence
// that sends a person to look at a diff instead of at the file they have to
// repair. Both surfaces are asserted together because they share the rule.
func TestConfigWriteErrors_LeaveAFailedRollbackSayingSo(t *testing.T) {
	mixed := &vault.TxnRollbackError{
		Path:         config.FileName,
		Err:          &vault.TxnConflictError{Path: config.FileName, Expected: "sha256:a", Actual: "sha256:b"},
		Failed:       []string{config.FileName},
		RollbackErrs: []error{errors.New("restore the previous bytes: permission denied")},
	}
	// This is how the commit boundary classifies it: coded a conflict, carrying
	// the transaction's own account of what is where.
	classified := noteops.WrapError(noteops.CodeConflict, mixed.Error(), mixed)

	for surface, got := range map[string]error{
		"settings":  settingsWriteError(classified),
		"templates": templatesWriteError(classified),
	} {
		if strings.Contains(got.Error(), "re-check the diff") {
			t.Fatalf("%s reported a failed rollback as a stale diff: %q", surface, got)
		}
		if got.Error() != classified.Error() {
			t.Fatalf("%s rewrote the mixed-state message:\nwant %q\ngot  %q", surface, classified, got)
		}
		if !vault.IsTxnRollbackFailure(got) {
			t.Fatalf("%s dropped the rollback failure from the chain: %v", surface, got)
		}
	}
}

// TestConfigWriteErrors_KeepTheRecoveryPath covers the third shape that
// unwraps to a conflict: a compare-and-swap rejected another writer's bytes and
// could not put them back, so they live only in a recovery file. The classifier
// codes it internal, but the rewrite must also leave a conflict-coded copy alone,
// because "re-check the diff" would replace the one sentence naming that file.
func TestConfigWriteErrors_KeepTheRecoveryPath(t *testing.T) {
	recovery := &vault.TxnRecoveryError{
		Path:         config.FileName,
		RecoveryPath: ".txn-custody-1.tmp",
		Foreign:      true,
		Err:          &vault.TxnConflictError{Path: config.FileName, Expected: "sha256:a", Actual: "sha256:b"},
	}
	for name, err := range map[string]error{
		"classified": noteops.ClassifyTxnError("settings: write", recovery),
		"conflict":   noteops.WrapError(noteops.CodeConflict, recovery.Error(), recovery),
	} {
		for surface, got := range map[string]error{
			"settings":  settingsWriteError(err),
			"templates": templatesWriteError(err),
		} {
			if noteops.CodeOf(got) == noteops.CodePreviewStale {
				t.Fatalf("%s/%s reported a needed recovery as a stale diff: %q", surface, name, got)
			}
			if !strings.Contains(noteops.MessageOf(got), "preserved at "+recovery.RecoveryPath) {
				t.Fatalf("%s/%s dropped the recovery path: %q", surface, name, got)
			}
		}
	}
}

// TestSettings_RefusesAKeyTheOverlayShadows: writing the portable file would
// report a save for a value the vault never adopts.
func TestSettings_RefusesAKeyTheOverlayShadows(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	local := "[capture]\nheading = \"## Overlay\"\n"
	if err := os.WriteFile(filepath.Join(root, config.LocalFileName), []byte(local), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	before := readVaultFile(t, root, config.FileName)
	status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("capture.heading", "## Later"))
	if status != http.StatusConflict {
		t.Fatalf("shadowed preview status = %d, want 409 (body %q)", status, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if !strings.Contains(raw, config.LocalFileName) {
		t.Fatalf("the refusal does not name the overlay: %q", raw)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("a refused preview changed the configuration:\n%s", got)
	}

	// The list still reports the key, marked as shadowed, so a person can see why.
	_, listed := getSettings(t, ts.URL)
	summary := settingByKey(t, decodeSettingsResponse(t, listed), "capture.heading")
	if !summary.Shadowed || summary.Layer != string(config.LayerLocal) {
		t.Fatalf("capture.heading = %#v, want shadowed by the overlay", summary)
	}
}

// TestSettingsApply_RefusesAnInvalidConfiguration: a file that no longer decodes
// disables the write rather than patching bytes nobody can load.
func TestSettingsApply_RefusesAnInvalidConfiguration(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte("this is not = = toml\n"), 0o644); err != nil {
		t.Fatalf("break config: %v", err)
	}
	status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("capture.heading", "## Later"))
	if status != http.StatusConflict {
		t.Fatalf("invalid-config preview status = %d, want 409 (body %q)", status, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
}

// TestSettingsApply_RecoversAfterMalformedConfigurationIsRepaired proves the
// refusal is a state rather than a latch.
func TestSettingsApply_RecoversAfterMalformedConfigurationIsRepaired(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte("broken = = \n"), 0o644); err != nil {
		t.Fatalf("break config: %v", err)
	}
	if status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("capture.heading", "## Later")); status != http.StatusConflict {
		t.Fatalf("invalid-config preview status = %d, body %q", status, raw)
	}

	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(settingsTestConfigTOML), 0o644); err != nil {
		t.Fatalf("repair config: %v", err)
	}
	preview := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))
	if status, _, raw := applySetting(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply after repair status = %d, body %q", status, raw)
	}
	if got := readVaultFile(t, root, config.FileName); !strings.Contains(got, `heading = "## Later"`) {
		t.Fatalf("the repaired configuration was not written:\n%s", got)
	}
}
