package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// settingsTestConfigTOML is a portable file that already assigns a few editable
// keys, so a test can tell "the file says this" from "this is the default".
const settingsTestConfigTOML = `[frontmatter.properties.status]
type = "string"

[capture]
inbox_note = "Inbox.md"
heading = "## Captures"

[cockpit]
daily_limit = 5
`

// machineLocalKeys is every key ownership.go assigns to brainnotes.local.toml.
// It is restated here rather than imported because this file is asserting that
// the web surface never names one of them, and a list derived from the table
// under test would agree with it by construction.
var machineLocalKeys = []string{
	"fzf.args",
	"open.editor",
	"serve.addr",
	"serve.open_browser",
	"agent.command",
	"agent.timeout_seconds",
	"cockpit.agent_command",
	"cockpit.agent_timeout_seconds",
}

func newSettingsServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	return newPropertyServer(t, settingsTestConfigTOML, propertyTestNotes())
}

func getSettings(t *testing.T, baseURL string) (int, string) {
	t.Helper()

	resp, err := http.Get(baseURL + settingsAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", settingsAPIRoutePath, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

func decodeSettingsResponse(t *testing.T, raw string) settingsResponse {
	t.Helper()

	var payload settingsResponse
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode settings body %q: %v", raw, err)
	}
	return payload
}

func postSettings(t *testing.T, baseURL, route, token string, body any) (int, settingsResponse, string) {
	t.Helper()

	status, raw := postProperty(t, baseURL, route, token, body)
	var payload settingsResponse
	if status == http.StatusOK {
		payload = decodeSettingsResponse(t, raw)
	}
	return status, payload, raw
}

// previewSetting runs a preview that is expected to succeed and issue a receipt.
func previewSetting(t *testing.T, baseURL string, req settingMutationRequest) settingsResponse {
	t.Helper()

	status, payload, raw := postSettings(t, baseURL, settingsPreviewAPIRoutePath, propertyTestToken, req)
	if status != http.StatusOK {
		t.Fatalf("settings preview status = %d, body %q", status, raw)
	}
	if payload.PreviewID == "" {
		t.Fatalf("settings preview issued no receipt: %q", raw)
	}
	return payload
}

func applySetting(t *testing.T, baseURL, previewID string) (int, settingsResponse, string) {
	t.Helper()
	return postSettings(t, baseURL, settingsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: previewID})
}

func settingByKey(t *testing.T, payload settingsResponse, key string) settingSummary {
	t.Helper()

	for _, summary := range payload.Settings {
		if summary.Key == key {
			return summary
		}
	}
	t.Fatalf("settings response does not carry %q", key)
	return settingSummary{}
}

func setStringSetting(key, text string) settingMutationRequest {
	return settingMutationRequest{
		Op:    settingOpSet,
		Key:   key,
		Value: &settingValue{Kind: string(config.SettingString), Text: text},
	}
}

// TestSettingsList_NamesEveryEditableKeyAndNoOtherKind is the field-ownership
// check at the transport boundary: the list is exactly the editable table, in
// its order, and nothing else.
func TestSettingsList_NamesEveryEditableKeyAndNoOtherKind(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, raw := getSettings(t, ts.URL)
	if status != http.StatusOK {
		t.Fatalf("GET settings status = %d, body %q", status, raw)
	}
	payload := decodeSettingsResponse(t, raw)

	specs := config.EditableSettings()
	if len(payload.Settings) != len(specs) {
		t.Fatalf("listed %d settings, want %d", len(payload.Settings), len(specs))
	}
	for i, spec := range specs {
		got := payload.Settings[i]
		if got.Key != spec.Key {
			t.Fatalf("settings[%d].key = %q, want %q", i, got.Key, spec.Key)
		}
		if got.Kind != string(spec.Kind) {
			t.Fatalf("%s kind = %q, want %q", spec.Key, got.Kind, spec.Kind)
		}
		if got.Summary == "" {
			t.Fatalf("%s has no summary", spec.Key)
		}
	}
	if payload.Action != "list" {
		t.Fatalf("action = %q, want list", payload.Action)
	}
	if !payload.Config.Writable {
		t.Fatalf("config reported as not writable: %#v", payload.Config)
	}
}

// TestSettingsList_NeverMentionsAMachineLocalKey is the privacy gate, and it is
// two assertions rather than one.
//
// The settings table is checked for machine-local *keys*: naming one there would
// be this surface offering to edit it. The whole body is checked for the
// overlay's *values*, because a value can escape through any field — a summary, a
// diff, a message — and that is what "without exposing machine-local settings"
// is about. The config block is deliberately not held to the first rule:
// restart_required is ConfigStatus's own pre-existing field, published
// identically by /api/config/status, and it names a key whose effect needs a
// restart without disclosing anything it was set to.
func TestSettingsList_NeverMentionsAMachineLocalKey(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	local := "[open]\neditor = \"secret-editor\"\n\n[serve]\naddr = \"10.0.0.7:9999\"\n"
	if err := os.WriteFile(filepath.Join(root, config.LocalFileName), []byte(local), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	// A mutation reloads, so the overlay really is part of the published
	// configuration by the time the list is served.
	status, _, raw := postSettings(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("capture.heading", "## Later"))
	if status != http.StatusOK {
		t.Fatalf("preview status = %d, body %q", status, raw)
	}

	status, body := getSettings(t, ts.URL)
	if status != http.StatusOK {
		t.Fatalf("GET settings status = %d, body %q", status, body)
	}
	if !decodeSettingsResponse(t, body).Config.LocalPresent {
		t.Fatalf("the overlay was not published, so this proves nothing: %s", body)
	}
	table, err := json.Marshal(decodeSettingsResponse(t, body).Settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	for _, key := range machineLocalKeys {
		if strings.Contains(string(table), key) {
			t.Fatalf("the settings table names machine-local key %q: %s", key, table)
		}
	}
	for _, secret := range []string{"secret-editor", "10.0.0.7"} {
		if strings.Contains(body, secret) {
			t.Fatalf("settings body leaked overlay value %q: %s", secret, body)
		}
	}
}

// TestSettingsList_ReportsWhatTheFileAssigns separates an assignment from a
// default, and names the layer that made it.
func TestSettingsList_ReportsWhatTheFileAssigns(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	_, raw := getSettings(t, ts.URL)
	payload := decodeSettingsResponse(t, raw)

	heading := settingByKey(t, payload, "capture.heading")
	if !heading.Assigned || heading.Layer != string(config.LayerPortable) {
		t.Fatalf("capture.heading = %#v, want assigned by the portable layer", heading)
	}
	if heading.Value.Text != "## Captures" {
		t.Fatalf("capture.heading value = %q, want %q", heading.Value.Text, "## Captures")
	}
	if heading.Shadowed {
		t.Fatalf("capture.heading reported as shadowed with no overlay")
	}

	limit := settingByKey(t, payload, "cockpit.daily_limit")
	if limit.Value.Number != 5 {
		t.Fatalf("cockpit.daily_limit = %d, want 5", limit.Value.Number)
	}

	unassigned := settingByKey(t, payload, "brief.exclude_types")
	if unassigned.Assigned || unassigned.Layer != "" {
		t.Fatalf("brief.exclude_types = %#v, want unassigned", unassigned)
	}
	if unassigned.Value.Items == nil {
		t.Fatalf("a list setting serialized its items as null rather than []")
	}
}

func TestSettingsList_RefusesAnythingButGET(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, raw := postProperty(t, ts.URL, settingsAPIRoutePath, propertyTestToken, map[string]string{})
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("POST %s status = %d, want 405 (body %q)", settingsAPIRoutePath, status, raw)
	}
}

func TestSettingsPreview_RequiresTheCSRFToken(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, "",
		setStringSetting("capture.heading", "## Later"))
	if status != http.StatusForbidden {
		t.Fatalf("untokened preview status = %d, want 403 (body %q)", status, raw)
	}
}

func TestSettingsPreview_RefusesAGET(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	resp, err := http.Get(ts.URL + settingsPreviewAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET preview: %v", err)
	}
	raw := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET preview status = %d, want 405 (body %q)", resp.StatusCode, raw)
	}
}

// TestSettingsPreview_AnswersTheSameForALocalKeyAndAnUnknownOne is the second
// half of the privacy gate: a request cannot use the refusal to learn that
// serve.addr is a real key this vault assigns.
func TestSettingsPreview_AnswersTheSameForALocalKeyAndAnUnknownOne(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	local := "[open]\neditor = \"secret-editor\"\n"
	if err := os.WriteFile(filepath.Join(root, config.LocalFileName), []byte(local), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	unknownStatus, unknownBody := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("not.a.key", "x"))
	if unknownStatus != http.StatusNotFound {
		t.Fatalf("unknown key status = %d, want 404 (body %q)", unknownStatus, unknownBody)
	}
	requireErrorCode(t, unknownBody, string(noteops.CodeNotFound))

	for _, key := range machineLocalKeys {
		status, body := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
			setStringSetting(key, "x"))
		if status != unknownStatus {
			t.Fatalf("%s status = %d, want %d (body %q)", key, status, unknownStatus, body)
		}
		requireErrorCode(t, body, string(noteops.CodeNotFound))
	}

	for _, key := range []string{"frontmatter.properties", "views", "new.routes"} {
		status, body := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
			setStringSetting(key, "x"))
		if status != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (body %q)", key, status, body)
		}
	}
}

func TestSettingsPreview_RefusesAValueOfTheWrongKind(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("cockpit.daily_limit", "9"))
	if status != http.StatusBadRequest {
		t.Fatalf("kind mismatch status = %d, want 400 (body %q)", status, raw)
	}
	if !strings.Contains(raw, "int") {
		t.Fatalf("refusal does not name the expected kind: %q", raw)
	}
}

func TestSettingsPreview_RefusesAnUnsupportedOpAndAMissingValue(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		settingMutationRequest{Op: "delete", Key: "capture.heading"})
	if status != http.StatusBadRequest {
		t.Fatalf("bad op status = %d, want 400 (body %q)", status, raw)
	}

	status, raw = postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		settingMutationRequest{Op: settingOpSet, Key: "capture.heading"})
	if status != http.StatusBadRequest {
		t.Fatalf("valueless set status = %d, want 400 (body %q)", status, raw)
	}
}

// TestSettingsPreview_RefusesAValueTheLoaderWouldReject is why the plan
// re-decodes the patched bytes instead of trusting the patcher: the value is a
// well-formed string of the right kind and the loader still refuses it, so the
// refusal has to come from the loader or the surface would write a file this
// vault cannot start from.
func TestSettingsPreview_RefusesAValueTheLoaderWouldReject(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	before := readVaultFile(t, root, config.FileName)
	status, raw := postProperty(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("capture.heading", "Later"))
	if status != http.StatusBadRequest {
		t.Fatalf("invalid-value preview status = %d, want 400 (body %q)", status, raw)
	}
	if !strings.Contains(raw, "ATX heading") {
		t.Fatalf("the refusal does not carry the loader's reason: %q", raw)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("a refused preview changed the configuration:\n%s", got)
	}
}

// TestSettingsPreview_ShowsTheDiffAndWritesNothing is the preview half of the
// write class: a plan renders, and the file on disk is untouched until apply.
func TestSettingsPreview_ShowsTheDiffAndWritesNothing(t *testing.T) {
	ts, _, root := newSettingsServer(t)

	before := readVaultFile(t, root, config.FileName)
	payload := previewSetting(t, ts.URL, setStringSetting("capture.heading", "## Later"))

	if len(payload.ConfigDiff) == 0 {
		t.Fatalf("preview rendered no diff: %#v", payload)
	}
	if settingByKey(t, payload, "capture.heading").Value.Text != "## Later" {
		t.Fatalf("preview does not project the new value: %#v", payload.Settings)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("preview wrote the configuration:\n%s", got)
	}
}

// TestSettingsPreview_IssuesNoReceiptForANoOp keeps an apply that would write
// identical bytes from ever existing.
func TestSettingsPreview_IssuesNoReceiptForANoOp(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	status, payload, raw := postSettings(t, ts.URL, settingsPreviewAPIRoutePath, propertyTestToken,
		setStringSetting("capture.heading", "## Captures"))
	if status != http.StatusOK {
		t.Fatalf("no-op preview status = %d, body %q", status, raw)
	}
	if payload.PreviewID != "" {
		t.Fatalf("a no-op issued a receipt: %q", payload.PreviewID)
	}
	if len(payload.ConfigDiff) != 0 {
		t.Fatalf("a no-op rendered a diff: %#v", payload.ConfigDiff)
	}
}

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
