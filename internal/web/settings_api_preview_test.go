package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

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
