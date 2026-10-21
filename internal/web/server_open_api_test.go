package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
)

func TestServer_OpenAPI_SuccessWithEditorArgs(t *testing.T) {
	var (
		gotBin  string
		gotArgv []string
		calls   int
	)
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --wait"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			calls++
			gotBin = bin
			gotArgv = append([]string(nil), argv...)
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNoContent, body)
	}
	if calls != 1 {
		t.Fatalf("start editor calls: got %d, want %d", calls, 1)
	}
	if !strings.HasPrefix(filepath.Base(gotBin), "echo") {
		t.Fatalf("bin: got %q, want echo binary", gotBin)
	}
	expectedPath, err := resolveOpenPath(root, filepath.Join(root, "Source.md"))
	if err != nil {
		t.Fatalf("resolve expected source path: %v", err)
	}
	wantArgv := []string{"echo", "--wait", "+1", expectedPath}
	if len(gotArgv) != len(wantArgv) {
		t.Fatalf("argv length: got %d, want %d (%v)", len(gotArgv), len(wantArgv), gotArgv)
	}
	for i := range wantArgv {
		if gotArgv[i] != wantArgv[i] {
			t.Fatalf("argv[%d]: got %q, want %q (full=%v)", i, gotArgv[i], wantArgv[i], gotArgv)
		}
	}
}

func TestServer_OpenAPI_CSRFRequired(t *testing.T) {
	called := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			called = true
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	if called {
		t.Fatal("start editor should not be called without csrf")
	}
}

func TestServer_OpenAPI_AcceptsFormCSRFToken(t *testing.T) {
	called := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			called = true
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "", "csrf-token")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if !called {
		t.Fatal("start editor should be called with valid form csrf token")
	}
}

func TestServer_OpenAPI_BadKeyResponses(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			t.Fatal("start editor should not be called for bad key requests")
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "../source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid key status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	requireOpenErrorCode(t, body, "invalid_key")

	resp = postOpenRequest(t, ts.URL, "missing", "csrf-token", "")
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown key status: got %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	requireOpenErrorCode(t, body, "not_found")
}

// requireOpenErrorCode asserts the open routes answer failures in the shared
// management envelope rather than as plain text, so every panel that reads
// {"ok":false,"code","message"} can show the message as written.
func requireOpenErrorCode(t *testing.T, body, code string) {
	t.Helper()
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != code || payload.Message == "" {
		t.Fatalf("payload = %#v, want code %q with a message (body %q)", payload, code, body)
	}
}

func TestServer_OpenAPI_RejectsEscapingSymlinkPath(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source.md")
	if err := os.WriteFile(sourcePath, []byte("---\ntitle: Source\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write source note: %v", err)
	}
	outsideRoot := t.TempDir()
	outsideNote := filepath.Join(outsideRoot, "Outside.md")
	if err := os.WriteFile(outsideNote, []byte("---\ntitle: Outside\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write outside note: %v", err)
	}

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	cfg.Open.Editor = "echo"
	srv, err := NewServer(cfg, v, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			t.Fatal("start editor should not be called for escaping path")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	now := time.Unix(0, 0)
	srv.state.mu.Lock()
	srv.state.now = func() time.Time { return now }
	srv.state.snapshot = vaultSnapshot{
		graph:    &query.Graph{PathsByTitle: map[string]string{"source": outsideNote}},
		loadedAt: now,
	}
	srv.state.mu.Unlock()

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := postOpenRequest(t, ts.URL, "source", "csrf-token", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestServer_OpenAPI_MissingEditorConfigured(t *testing.T) {
	t.Setenv("EDITOR", "")

	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = ""
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			t.Fatal("start editor should not be called without editor config")
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusPreconditionFailed)
	}
	requireOpenErrorCode(t, body, "invalid_request")
	if !strings.Contains(body, "editor not configured") {
		t.Fatalf("body does not say the editor is missing: %q", body)
	}
}

func TestServer_ConfigOpenAPI_UsesConfiguredEditor(t *testing.T) {
	var (
		gotArgv []string
		calls   int
	)
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":     []byte("---\ntitle: Source\n---\nBody\n"),
		config.FileName: []byte("[open]\neditor = \"echo --wait\"\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --wait"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(_ string, argv []string) error {
			calls++
			gotArgv = append([]string(nil), argv...)
			return nil
		},
	})

	req, err := http.NewRequest(http.MethodPost, ts.URL+configOpenAPIRoutePath, strings.NewReader("token=csrf-token"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeaderName, "csrf-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", configOpenAPIRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNoContent, body)
	}
	if calls != 1 {
		t.Fatalf("start editor calls: got %d, want 1", calls)
	}
	wantPath, err := resolveOpenPath(root, filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	wantArgv := []string{"echo", "--wait", "+1", wantPath}
	if strings.Join(gotArgv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("editor argv = %#v, want %#v", gotArgv, wantArgv)
	}
}

func TestServer_ConfigOpenAPI_RequiresCSRF(t *testing.T) {
	called := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(_ string, _ []string) error {
			called = true
			return nil
		},
	})

	resp, err := http.Post(ts.URL+configOpenAPIRoutePath, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST %s: %v", configOpenAPIRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	requireOpenErrorCode(t, body, "forbidden")
	if called {
		t.Fatal("start editor should not be called without csrf")
	}
}
