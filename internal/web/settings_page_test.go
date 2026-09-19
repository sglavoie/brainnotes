package web

import (
	"net/http"
	"strings"
	"testing"
)

func getSettingsPage(t *testing.T, baseURL, path string) (int, string) {
	t.Helper()

	resp, err := http.Get(baseURL + path) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

// hasAttribute reports whether the rendered page carries `name` as a whole
// attribute rather than as the opening of a longer one.
//
// A bare `strings.Contains` cannot answer this on these panels: `data-settings-
// confirm` is a prefix of `data-settings-confirm-block` and `data-templates-
// name` of nothing today but of anything tomorrow, so a presence check would
// pass on a page that renders only the longer attribute and the controller would
// find no element. The delimiter is what makes the assertion mean what it says.
func hasAttribute(body, name string) bool {
	for _, suffix := range []string{name + ">", name + " ", name + "=", name + "\n", name + "\r"} {
		if strings.Contains(body, suffix) {
			return true
		}
	}
	return false
}

func TestSettingsPage_RendersBothPanelsAndTheirControls(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n"),
	})

	status, body := getSettingsPage(t, ts.URL, settingsRoutePath)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	// Every attribute the two controllers query for. readTemplatesElements and
	// readElements return nil when one of the four required ones is missing, so a
	// renamed attribute is a panel that silently never mounts.
	for _, want := range []string{
		"data-settings-panel",
		"data-settings-csrf",
		"data-settings-status",
		"data-settings-list",
		"data-settings-diff",
		"data-settings-confirm",
		"data-settings-cancel",
		"data-settings-refresh",
		"data-templates-panel",
		"data-templates-csrf",
		"data-templates-status",
		"data-templates-notice",
		"data-templates-list",
		"data-templates-name",
		"data-templates-content",
		"data-templates-diff",
		"data-templates-save",
		"data-templates-delete",
		"data-templates-confirm",
		"data-templates-cancel",
		"data-templates-new",
		"data-templates-refresh",
	} {
		if !hasAttribute(body, want) {
			t.Fatalf("settings page missing attribute %q", want)
		}
	}
}

// TestSettingsPage_SeedsTheEndpointsAndTheToken pins the page's whole contract
// with the browser: it carries a token and two endpoint names, and no setting
// or template content at all — both panels fetch their own state, so the page
// and the API cannot report different things.
func TestSettingsPage_SeedsTheEndpointsAndTheToken(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n"),
	})

	_, body := getSettingsPage(t, ts.URL, settingsRoutePath)
	for _, want := range []string{
		`data-settings-endpoint="` + settingsAPIRoutePath + `"`,
		`data-templates-endpoint="` + templatesAPIRoutePath + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}
	if !strings.Contains(body, `data-settings-csrf="`) || strings.Contains(body, `data-settings-csrf=""`) {
		t.Fatalf("settings page did not seed a CSRF token")
	}
	if strings.Contains(body, "data-settings-row") {
		t.Fatalf("settings page rendered a settings row server-side")
	}
	if strings.Contains(body, "data-templates-open") {
		t.Fatalf("settings page rendered a template row server-side")
	}
}

// TestSettingsPage_SaysMachineLocalSettingsAreElsewhere covers the one thing a
// person has to be told before they look for a key that is not in the list.
func TestSettingsPage_SaysMachineLocalSettingsAreElsewhere(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n"),
	})

	_, body := getSettingsPage(t, ts.URL, settingsRoutePath)
	if !strings.Contains(body, "brainnotes.local.toml") {
		t.Fatalf("settings page does not say where machine-local settings live")
	}
	for _, key := range machineLocalKeys {
		if strings.Contains(body, `data-settings-row="`+key+`"`) {
			t.Fatalf("settings page rendered machine-local key %q", key)
		}
	}
}

// TestSettingsPage_OffersTheUndoTheWritesNeed pins the history panel onto this
// page. Both write classes here are journaled and undoable, and "undo it from
// Recent changes" is what the templates controller tells a person after a
// delete — a page without the panel would make that sentence a dead end.
func TestSettingsPage_OffersTheUndoTheWritesNeed(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n"),
	})

	_, body := getSettingsPage(t, ts.URL, settingsRoutePath)
	for _, want := range []string{"data-history-panel", "data-history-list", "data-history-undo-latest"} {
		if !hasAttribute(body, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}
}

func TestSettingsPage_IsExactPathAndGETOnly(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n"),
	})

	status, _ := getSettingsPage(t, ts.URL, settingsRoutePath+"/extra")
	if status != http.StatusNotFound {
		t.Fatalf("subpath status = %d, want %d", status, http.StatusNotFound)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, err := http.NewRequest(method, ts.URL+settingsRoutePath, nil)
		if err != nil {
			t.Fatalf("new %s request: %v", method, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, settingsRoutePath, err)
		}
		_ = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want %d", method, resp.StatusCode, http.StatusMethodNotAllowed)
		}
		if got := resp.Header.Get("Allow"); got != http.MethodGet {
			t.Fatalf("%s Allow = %q, want %q", method, got, http.MethodGet)
		}
	}
}

// TestSettingsPage_ReadOnlyReplicaRefusesIt pins the page onto the same
// allowlist as the routes it calls: /settings, /api/settings and /api/templates
// are all absent from replicaReadPath, so a replica that served the page would
// offer a configuration workspace for writes it refuses.
func TestSettingsPage_ReadOnlyReplicaRefusesIt(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n"),
	}, nil, Options{ReadOnly: true, StateDir: t.TempDir()})

	for _, path := range []string{settingsRoutePath, settingsAPIRoutePath, templatesAPIRoutePath} {
		status, body := getSettingsPage(t, ts.URL, path)
		if status != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want %d", path, status, http.StatusForbidden)
		}
		if !strings.Contains(body, "read-only replica") {
			t.Fatalf("GET %s refusal did not name the replica: %s", path, body)
		}
	}
}

// TestSettingsPage_LinkedFromTheNavOnlyWhereItIsServed is the entry-point half
// of the refusal above: a mode that answers 403 must not render a link to it.
func TestSettingsPage_LinkedFromTheNavOnlyWhereItIsServed(t *testing.T) {
	files := map[string][]byte{"Notes/Alpha.md": []byte("---\ntitle: Alpha\n---\nBody\n")}
	link := `<a href="/settings">Settings</a>`

	writable, _ := newHTTPServerForFiles(t, files)
	for _, path := range []string{"/", libraryRoutePath, healthRoutePath, settingsRoutePath} {
		_, body := getSettingsPage(t, writable.URL, path)
		if !strings.Contains(body, link) {
			t.Fatalf("%s is missing the Settings navigation link", path)
		}
	}

	replica, _ := newHTTPServerForFilesWithOptions(t, files, nil,
		Options{ReadOnly: true, StateDir: t.TempDir()})
	_, body := getSettingsPage(t, replica.URL, "/")
	if strings.Contains(body, link) {
		t.Fatalf("read-only overview links to a settings page it refuses")
	}
}
