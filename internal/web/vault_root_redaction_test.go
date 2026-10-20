package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Three surfaces write an error's text into a body of their own rather than
// through the shared management error writer: a failed source save (whose
// snapshot failure carries git's stderr), a property plan's blockers, and a
// history comparison that could not be read. Each must redact for a hosted
// reader exactly as the shared writer does, and keep the path for a local one.
func TestServer_HostedErrorBodiesNeverNameTheVaultRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   Mode
		redact bool
	}{
		{"local", ModeLocal, false},
		{"hosted", ModeHosted, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// readerFor stands in for requestGuard: it installs the redacting
			// writer exactly when the mode's capabilities ask for one.
			readerFor := func(root string) (*httptest.ResponseRecorder, http.ResponseWriter) {
				recorder := httptest.NewRecorder()
				redactor := vaultRootRedactorFor(capabilitiesFor(tc.mode), root)
				if !redactor.active() {
					return recorder, recorder
				}
				return recorder, &redactingResponseWriter{ResponseWriter: recorder, redactor: redactor}
			}

			_, srv, root := newSourceTestServer(t, Options{Hosted: tc.mode == ModeHosted})
			recorder, w := readerFor(root)
			srv.writeSourceSaveError(w, noteops.SaveSourceRequest{RelPath: "Alpha.md"}, nil,
				noteops.Errorf(noteops.CodeSnapshotFailed, "git add: fatal: unable to index %s", filepath.Join(root, "Alpha.md")))
			payload := decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSnapshotFailed)
			assertVaultRootRedaction(t, "source save error", payload.Message, root, tc.redact)

			_, w = readerFor(root)
			response := propertyMutationResponseForPlan(w, &propertyPlan{Blockers: []propertyBlocker{{
				Kind: propertyBlockerConfigConflict, Detail: "patch " + filepath.Join(root, config.FileName) + ": conflict",
			}}}, ConfigStatus{})
			assertVaultRootRedaction(t, "property blocker", response.Blockers[0].Detail, root, tc.redact)

			// A real recorded change whose manifest is gone fails its comparison
			// read with the manifest's absolute path.
			_, historyRoot, entry := newRestorableVault(t)
			manifest := filepath.Join(noteops.JournalRoot(historyRoot), entry, journal.ManifestFileName)
			if err := os.Remove(manifest); err != nil {
				t.Fatalf("remove manifest: %v", err)
			}
			preview := historyPreviewResponse{EntryID: entry, Changes: []historyPreviewChange{{Path: "Source.md", Class: "note"}}}
			_, w = readerFor(historyRoot)
			(&Server{vault: &vault.Vault{Root: historyRoot}}).addHistoryComparisons(w, &preview, nil)
			assertVaultRootRedaction(t, "history comparison", preview.Changes[0].ComparisonUnavailable, historyRoot, tc.redact)
		})
	}
}

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
			}, nil, tc.opts)
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
