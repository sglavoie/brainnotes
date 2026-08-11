package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
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
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid key status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = postOpenRequest(t, ts.URL, "missing", "csrf-token", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown key status: got %d, want %d", resp.StatusCode, http.StatusNotFound)
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
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusPreconditionFailed)
	}
}
