package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const reloadableConfigTOML = `[frontmatter.properties.status]
type = "string"
allowed = ["draft", "done"]
`

// newReloadableServer builds a server whose configuration really lives on disk,
// so the reload path has a file to hash and re-decode.
func newReloadableServer(t *testing.T, source string, opts Options) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody marker.\n")
	configPath := filepath.Join(root, config.FileName)
	if err := os.WriteFile(configPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Decode(root, config.VaultOriginFlag, []byte(source))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	disabled := false
	cfg.Serve.Addr = "127.0.0.1:0"
	cfg.Serve.OpenBrowser = &disabled
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

func rewriteConfig(t *testing.T, root, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(source), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
}

func getConfigStatus(t *testing.T, baseURL string) (int, configStatusResponse) {
	t.Helper()
	resp, err := http.Get(baseURL + configStatusRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", configStatusRoutePath, err)
	}
	body := readResponseBody(t, resp)
	var payload configStatusResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("decode status body %q: %v", body, err)
		}
	}
	return resp.StatusCode, payload
}

func TestConfigStatus_ReportsBootGeneration(t *testing.T) {
	ts, _, _ := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	status, payload := getConfigStatus(t, ts.URL)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !payload.OK || payload.Status != ReloadOK || !payload.Writable {
		t.Fatalf("boot status unexpected: %#v", payload)
	}
	if payload.Generation != 1 {
		t.Fatalf("boot generation = %d, want 1", payload.Generation)
	}
	if payload.SourceHash == "" {
		t.Fatal("boot status should carry a source hash")
	}
	if len(payload.RestartRequired) != 0 {
		t.Fatalf("nothing should need a restart at boot: %v", payload.RestartRequired)
	}
}

func TestConfigStatus_RejectsNonGET(t *testing.T) {
	ts, _, _ := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	resp, err := http.Post(ts.URL+configStatusRoutePath, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST %s: %v", configStatusRoutePath, err)
	}
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestConfigStatus_ValidExternalEditReloadsAndInvalidatesCache(t *testing.T) {
	ts, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	if _, payload := getConfigStatus(t, ts.URL); payload.Generation != 1 {
		t.Fatalf("boot generation = %d", payload.Generation)
	}
	if _, err := srv.state.currentSnapshot(); err != nil {
		t.Fatalf("warm snapshot: %v", err)
	}

	rewriteConfig(t, root, reloadableConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")

	_, payload := getConfigStatus(t, ts.URL)
	if payload.Status != ReloadOK || !payload.Writable {
		t.Fatalf("valid edit should stay ok: %#v", payload)
	}
	if payload.Generation != 2 {
		t.Fatalf("generation = %d, want 2", payload.Generation)
	}
	if def, ok := srv.config().Frontmatter.Properties["area"]; !ok || def.Type != config.KindWikilink {
		t.Fatalf("reloaded config missing the new property: %#v", srv.config().Frontmatter.Properties)
	}
	if def, ok := srv.vault.Frontmatter().Properties["area"]; !ok || def.Type != config.KindWikilink {
		t.Fatalf("vault should see the reloaded config: %#v", srv.vault.Frontmatter().Properties)
	}
	if srv.state.snapshot.graph != nil {
		t.Fatal("a valid reload must invalidate the cached vault snapshot")
	}

	// An unchanged file must not bump the generation again.
	if _, again := getConfigStatus(t, ts.URL); again.Generation != 2 {
		t.Fatalf("unchanged file should not republish, generation = %d", again.Generation)
	}
}

func TestConfigStatus_InvalidEditKeepsLastValidConfigAndDisablesPropertyWrites(t *testing.T) {
	ts, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	rewriteConfig(t, root, "[frontmatter.properties.status]\ntype = \"nonsense\"\n")

	_, payload := getConfigStatus(t, ts.URL)
	if payload.Status != ReloadInvalid {
		t.Fatalf("invalid edit should be reported: %#v", payload)
	}
	if payload.Writable {
		t.Fatal("an invalid configuration must disable property writes")
	}
	if payload.Error == "" {
		t.Fatal("invalid status should carry the validation error")
	}
	if payload.Generation != 1 {
		t.Fatalf("invalid edit must not publish a new generation, got %d", payload.Generation)
	}
	if def, ok := srv.config().Frontmatter.Properties["status"]; !ok || def.Type != config.KindString {
		t.Fatalf("last valid configuration should stay published: %#v", srv.config().Frontmatter.Properties)
	}

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("property write status = %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	if !strings.Contains(body, "property writes are disabled") {
		t.Fatalf("refusal should explain itself, got %q", body)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
		t.Fatalf("note must be untouched, got:\n%s", got)
	}

	// Fixing the file re-enables writes without a restart.
	rewriteConfig(t, root, reloadableConfigTOML)
	_, fixed := getConfigStatus(t, ts.URL)
	if fixed.Status != ReloadOK || !fixed.Writable {
		t.Fatalf("repaired config should recover: %#v", fixed)
	}
	if fixed.Generation != 2 {
		t.Fatalf("repaired config should publish a new generation, got %d", fixed.Generation)
	}
	resp = postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("property write after repair: got %d, want 200; body=%q", resp.StatusCode, body)
	}
}

func TestConfigStatus_PropertyWriteSeesAnEditMadeSinceTheLastPoll(t *testing.T) {
	ts, _, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	// No status poll happens between this edit and the write. The new definition
	// makes `tags` a list, which is what decides whether the value is split on
	// commas, so the write path itself must reload before applying.
	rewriteConfig(t, root, reloadableConfigTOML+"\n[frontmatter.properties.tags]\ntype = \"list\"\nitems = \"string\"\n")

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "tags", "=", "alpha,beta", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write status = %d, want 200; body=%q", resp.StatusCode, body)
	}
	got := readNoteFile(t, filepath.Join(root, "Source.md"))
	if !strings.Contains(got, "- alpha") || !strings.Contains(got, "- beta") {
		t.Fatalf("write should have used the reloaded list definition, got:\n%s", got)
	}
}

func TestConfigStatus_StartupOnlyChangesReportRestartRequired(t *testing.T) {
	ts, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})
	bootAddr := srv.config().Serve.Addr

	rewriteConfig(t, root, reloadableConfigTOML+"\n[serve]\naddr = \"127.0.0.1:9999\"\nopen_browser = true\n")

	_, payload := getConfigStatus(t, ts.URL)
	if payload.Status != ReloadOK {
		t.Fatalf("startup-only change is still a valid config: %#v", payload)
	}
	want := map[string]bool{"serve.addr": true, "serve.open_browser": true}
	if len(payload.RestartRequired) != len(want) {
		t.Fatalf("restart_required = %v, want %v", payload.RestartRequired, want)
	}
	for _, field := range payload.RestartRequired {
		if !want[field] {
			t.Fatalf("unexpected restart_required entry %q", field)
		}
	}
	if srv.config().Serve.Addr == bootAddr {
		t.Fatal("the reloaded snapshot should carry the new address even though the listener keeps the old one")
	}
}

func TestConfigStatus_MissingConfigFileIsNotWatched(t *testing.T) {
	// The web tests that build a config in memory have no brainnotes.toml on
	// disk; reload must be a no-op for them rather than a permanent error state.
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})
	status, payload := getConfigStatus(t, ts.URL)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if payload.Status != ReloadOK || !payload.Writable {
		t.Fatalf("unwatched config should stay ok: %#v", payload)
	}
	if payload.SourceHash != "" {
		t.Fatalf("unwatched config has no source hash, got %q", payload.SourceHash)
	}
}

func TestConfigStatus_DeletedConfigBecomesInvalid(t *testing.T) {
	ts, _, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	if err := os.Remove(filepath.Join(root, config.FileName)); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	_, payload := getConfigStatus(t, ts.URL)
	if payload.Status != ReloadInvalid || payload.Writable {
		t.Fatalf("a config that disappears after boot is an error state: %#v", payload)
	}
}

func TestConfigManager_ConcurrentRefreshAndSnapshot(t *testing.T) {
	_, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	var wg sync.WaitGroup
	const readers = 8
	wg.Add(readers + 1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			rewriteConfig(t, root, reloadableConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")
			if err := srv.reloadConfig(); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
			rewriteConfig(t, root, reloadableConfigTOML)
			if err := srv.reloadConfig(); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
		}
	}()
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				cfg := srv.config()
				if cfg == nil {
					t.Error("snapshot must never be nil")
					return
				}
				_ = cfg.Frontmatter.Properties["status"]
				_ = srv.configState.Status()
			}
		}()
	}
	wg.Wait()

	if srv.configState.Status().Status != ReloadOK {
		t.Fatalf("final status = %#v", srv.configState.Status())
	}
}

func TestConfigManager_UndoOfConfigFileRepublishes(t *testing.T) {
	_, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})
	boot := srv.configState.Status()

	rewriteConfig(t, root, reloadableConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")
	if err := srv.reloadConfig(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := srv.config().Frontmatter.Properties["area"]; !ok {
		t.Fatal("edit should be published")
	}

	// Simulate undo restoring the previous brainnotes.toml bytes.
	rewriteConfig(t, root, reloadableConfigTOML)
	if err := srv.reloadConfig(); err != nil {
		t.Fatalf("reload after restore: %v", err)
	}
	restored := srv.configState.Status()
	if restored.SourceHash != boot.SourceHash {
		t.Fatalf("restored source hash = %q, want %q", restored.SourceHash, boot.SourceHash)
	}
	if _, ok := srv.config().Frontmatter.Properties["area"]; ok {
		t.Fatal("restored config should no longer declare the reverted property")
	}
	if restored.Generation <= boot.Generation {
		t.Fatalf("each republication bumps the generation, got %d after %d", restored.Generation, boot.Generation)
	}
}
