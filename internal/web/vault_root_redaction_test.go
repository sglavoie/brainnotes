package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// An invalid configuration's error names the file that failed, and the file is
// named by its absolute path. A reader on another machine is told which file and
// why, but not where the vault lives on the serving machine's disk; the local
// profile keeps the whole path, because that reader is on that disk.
func TestServer_RemoteReadersNeverSeeTheVaultRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   Options
		redact bool
	}{
		{"local", Options{CSRFToken: "csrf-token"}, false},
		{"hosted", Options{CSRFToken: "csrf-token", Hosted: true}, true},
		{"read-only", Options{CSRFToken: "csrf-token", ReadOnly: true, StateDir: t.TempDir()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
				config.FileName: []byte(""),
				"Source.md":     []byte("---\ntitle: Source\n---\nBody\n"),
				"Broken.md":     []byte("---\ntitle: [unterminated\n---\nBody\n"),
			}, nil, tc.opts)

			assertParseErrorRedaction(t, ts.URL, root, !tc.opts.ReadOnly, tc.redact)

			writeTestFile(t, root, config.FileName, "[[[ not toml\n")

			status := getConfigStatusError(t, ts.URL)
			if status == "" {
				t.Fatalf("an invalid configuration reported no error")
			}
			assertVaultRootRedaction(t, "config status", status, root, tc.redact)

			if tc.opts.ReadOnly {
				return
			}
			// The same text reaches a write refusal through the shared
			// management error writer.
			resp := doFormPost(t, ts.URL+"/api/notes/frontmatter/set", url.Values{
				"key": {"source"}, "property": {"status"}, "value": {"done"},
			}, "csrf-token")
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%q", resp.StatusCode, body)
			}
			assertVaultRootRedaction(t, "write refusal", decodeManagementError(t, body).Message, root, tc.redact)
		})
	}
}

func getConfigStatusError(t *testing.T, baseURL string) string {
	t.Helper()
	resp, err := http.Get(baseURL + configStatusRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET config status: %v", err)
	}
	var payload configStatusResponse
	if err := json.Unmarshal([]byte(readResponseBody(t, resp)), &payload); err != nil {
		t.Fatalf("decode config status: %v", err)
	}
	return payload.Error
}

func assertVaultRootRedaction(t *testing.T, surface, message, root string, redact bool) {
	t.Helper()
	named := strings.Contains(message, root)
	if redact && (named || !strings.Contains(message, "<vault>")) {
		t.Fatalf("%s leaked the vault root to a remote reader: %q", surface, message)
	}
	if !redact && !named {
		t.Fatalf("%s lost the absolute path in local mode: %q", surface, message)
	}
}

func TestVaultRootRedactorPrefersTheLongerSpelling(t *testing.T) {
	redactor := vaultRootRedactor{roots: []string{"/private/var/v", "/var/v"}}
	got := redactor.redact("open /private/var/v/a.md and /var/v/b.md")
	if got != "open <vault>/a.md and <vault>/b.md" {
		t.Fatalf("redact = %q", got)
	}
	if got := (vaultRootRedactor{}).redact("/var/v/a.md"); got != "/var/v/a.md" {
		t.Fatalf("zero redactor changed the message: %q", got)
	}
}

// assertParseErrorRedaction checks the surfaces a snapshot's parse error reaches.
// A note whose frontmatter does not parse is reported by its absolute path, and
// Health lists that error verbatim; a server that writes also returns it as a
// property blocker's detail.
func assertParseErrorRedaction(t *testing.T, baseURL, root string, writes, redact bool) {
	t.Helper()
	health := html.UnescapeString(getBodyOK(t, baseURL+healthRoutePath))
	if !strings.Contains(health, "Broken.md") {
		t.Fatalf("health did not report the broken note")
	}
	assertVaultRootRedaction(t, "health parse error", health, root, redact)
	if !writes {
		return
	}
	status, payload := postPropertyMutation(t, baseURL, propertyPreviewAPIRoutePath, "csrf-token", propertyMutationRequest{
		Action: propertyActionUnset, Property: "title", Notes: []string{"Broken.md"},
	})
	if status != http.StatusOK || len(payload.Blockers) != 1 {
		t.Fatalf("property preview status = %d, blockers = %#v", status, payload.Blockers)
	}
	assertVaultRootRedaction(t, "property blocker", payload.Blockers[0].Detail, root, redact)
}

func getBodyOK(t *testing.T, target string) string {
	t.Helper()
	resp, err := http.Get(target) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", target, resp.StatusCode)
	}
	return body
}
