package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func buildWebTestVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	note := filepath.Join(root, "Note.md")
	if err := os.WriteFile(note, []byte("---\ntitle: Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	return root
}

func testVaultOpen(t *testing.T, root string, addr string) (*config.Config, *vault.Vault) {
	t.Helper()
	disabled := false
	cfg := &config.Config{
		Vault: root,
		Serve: config.ServeConfig{
			Addr:        addr,
			OpenBrowser: &disabled,
		},
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	return cfg, v
}

func awaitGET(t *testing.T, rawURL string, timeout time.Duration) (*http.Response, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(rawURL) // #nosec G107 -- test-only local listener URL
		if err == nil {
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newHTTPServerForFiles(t *testing.T, files map[string][]byte) (*httptest.Server, string) {
	return newHTTPServerForFilesWithOptions(t, files, nil, Options{})
}

func newHTTPServerForFilesWithOptions(
	t *testing.T,
	files map[string][]byte,
	configure func(cfg *config.Config),
	opts Options,
) (*httptest.Server, string) {
	t.Helper()

	root := t.TempDir()
	for rel, data := range files {
		writeTestFile(t, root, rel, string(data))
	}

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	if configure != nil {
		configure(cfg)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, root
}

// newBlockedLoaderServer builds a running test server whose note loader blocks
// forever on blockedRel (a vault-relative slash path), so that note times out on
// the first bounded load and is quarantined for the life of the server; every
// other note loads normally. It injects the loader before the handler goes live
// (there is no Options hook for it) and shrinks the load timeout so the one
// bounded wait is a few tens of milliseconds instead of two seconds. It returns
// the running server, the vault root, and the callCount probe so a test can
// prove the stalled note is read at most once no matter how many endpoints run.
func newBlockedLoaderServer(
	t *testing.T,
	files map[string][]byte,
	blockedRel string,
	configure func(cfg *config.Config),
	opts Options,
) (*httptest.Server, string, func(string) int) {
	t.Helper()

	root := t.TempDir()
	for rel, data := range files {
		writeTestFile(t, root, rel, string(data))
	}
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	if configure != nil {
		configure(cfg)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	blockedPath := filepath.Join(root, filepath.FromSlash(blockedRel))
	load, callCount := newBlockingLoader(t, blockedPath)
	srv.state.load = load
	srv.state.loadTimeout = 50 * time.Millisecond

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, root, callCount
}

func writeTestFile(t *testing.T, root, rel, body string) {
	t.Helper()

	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func readNoteFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func readResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func denseGraphVaultFixture() map[string][]byte {
	return map[string][]byte{
		"Hub.md":    []byte("---\ntitle: Hub\n---\n[[SpokeA]] [[SpokeB]] [[SpokeC]] [[SpokeD]] [[SpokeE]]\n"),
		"SpokeA.md": []byte("---\ntitle: Spoke A\n---\n[[Hub]] [[SpokeB]] [[SpokeC]]\n"),
		"SpokeB.md": []byte("---\ntitle: Spoke B\n---\n[[Hub]] [[SpokeC]] [[SpokeD]]\n"),
		"SpokeC.md": []byte("---\ntitle: Spoke C\n---\n[[Hub]] [[SpokeD]] [[SpokeE]]\n"),
		"SpokeD.md": []byte("---\ntitle: Spoke D\n---\n[[Hub]] [[SpokeE]] [[SpokeA]]\n"),
		"SpokeE.md": []byte("---\ntitle: Spoke E\n---\n[[Hub]] [[SpokeA]] [[SpokeB]]\n"),
		"Leaf.md":   []byte("---\ntitle: Leaf\n---\n[[Hub]]\n"),
		"Island.md": []byte("---\ntitle: Island\n---\nNo links.\n"),
	}
}
