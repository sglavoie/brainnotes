package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
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
