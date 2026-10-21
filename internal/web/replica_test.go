package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func replicaFixture(t *testing.T, root, state string) *Server {
	t.Helper()
	cfg := &config.Config{Vault: root, Serve: config.ServeConfig{Addr: "127.0.0.1:0"}}
	v, err := vault.OpenReadOnly(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, v, Options{ReadOnly: true, StateDir: state, CSRFToken: "replica-test",
		StartEditor: func(string, []string) error { t.Fatal("editor launched"); return nil },
		RunAgent:    func(context.Context, []string, []byte) ([]byte, error) { t.Fatal("agent launched"); return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestReplicaRequiresExternalState(t *testing.T) {
	root := t.TempDir()
	for _, state := range []string{"", root} {
		if err := validateReplicaOptions(root, Options{ReadOnly: true, StateDir: state}); err == nil {
			t.Fatal("accepted unsafe state")
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateReplicaOptions(root, Options{ReadOnly: true, StateDir: alias}); err == nil {
		t.Fatal("accepted state alias")
	}
}

// TestReplicaStateDirRefusalsNameTheFlag pins each --state-dir refusal under
// bn serve's name: a missing directory is not a bare lstat, and a regular file
// is "not a directory", not "inside the vault".
func TestReplicaStateDirRefusalsNameTheFlag(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	missing := filepath.Join(outside, "nope")
	file := filepath.Join(outside, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "state")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		opts Options
		want string
	}{
		{Options{ReadOnly: true}, "serve: --read-only requires an external --state-dir"},
		{Options{StateDir: missing}, "serve: --state-dir: " + missing + " does not exist; create the directory first"},
		{Options{StateDir: file}, "serve: --state-dir: " + file + " is not a directory"},
		{Options{StateDir: inside}, "serve: --state-dir: " + inside + " must be outside the vault"},
	} {
		err := validateReplicaOptions(root, tc.opts)
		if err == nil || err.Error() != tc.want {
			t.Errorf("validateReplicaOptions(%+v) = %v, want %q", tc.opts, err, tc.want)
		}
	}
	if err := validateReplicaOptions(root, Options{StateDir: outside}); err != nil {
		t.Fatalf("external directory refused: %v", err)
	}
}

func TestReplicaBrowsingAndWriteBarrier(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"A.md": "---\ntitle: Alpha\npinned: true\n---\n[[B]]\n", "B.md": "# Beta\n", "file.txt": "attachment", "brainnotes.toml": "", "brainnotes.local.toml": "", ".secret": "hidden"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0755) })
	srv := replicaFixture(t, root, state)
	for _, route := range []string{"/", "/library", "/n/A", "/pinned", "/properties", "/health", "/broken", "/graph", "/api/ready", "/api/search?q=Alpha", "/api/graph.json", "/api/facets", "/api/views", "/api/properties", "/media/file.txt"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		if rec.Code != 200 {
			t.Errorf("%s: %d %s", route, rec.Code, rec.Body.String())
		}
	}
	for _, route := range []string{"/media/brainnotes.toml", "/media/brainnotes.local.toml", "/media/.secret", "/media/../brainnotes.toml"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		if rec.Code == 200 {
			t.Errorf("private file exposed: %s", route)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatal("vault runtime state created")
	}
}

func TestReplicaForbiddenRoutes(t *testing.T) {
	srv := replicaFixture(t, t.TempDir(), t.TempDir())
	// Every registered non-reading capability and all alternate methods are guarded.
	routes := []string{"/api/open", "/api/config/open", "/api/capture", "/api/notes/trash", "/api/notes/create", "/api/notes/rename", "/api/notes/move", "/api/notes/frontmatter/set", "/api/notes/frontmatter/unset", "/api/pins/set", "/api/pins/remove", "/api/pins/reorder", "/api/notes/bulk/preview", "/api/notes/bulk", "/history", "/api/history", "/api/history/preview", "/api/undo", "/api/brief/insight", "/api/note/insight", "/api/daily-review/agent", "/api/fixes/preview", "/api/fixes/apply", "/api/properties/value", "/api/properties/preview", "/api/properties/apply", "/api/properties/schema/preview", "/api/properties/schema/apply", "/api/views/preview", "/api/views/apply", "/api/synthesis/context", "/api/synthesis/generate", "/api/synthesis/preview", "/api/synthesis/apply", "/api/cockpit/item", "/api/related/item", "/synthesize"}
	for _, route := range routes {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			req := httptest.NewRequest(method, route, strings.NewReader("token=replica-test"))
			req.Header.Set(csrfHeaderName, "replica-test")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s allowed: %d", method, route, rec.Code)
			}
		}
	}
}

func replicaPost(t *testing.T, srv *Server, route string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("POST", route, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result := httptest.NewRecorder()
	srv.Handler().ServeHTTP(result, request)
	return result
}

func TestReplicaBriefPersistsAcrossReplacement(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	writeReplicaNotes(t, root)
	srv := replicaFixture(t, root, state)
	if got := replicaPost(t, srv, "/api/brief/view", url.Values{}); got.Code != 403 {
		t.Fatal("CSRF bypass")
	}
	if got := replicaPost(t, srv, "/api/brief/view", url.Values{"token": {"replica-test"}}); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	card := replicaCard(t, srv)
	openAndReplaceReplicaCard(t, srv, card)
	replacement := t.TempDir()
	if err := os.WriteFile(filepath.Join(replacement, "Renamed.md"), []byte("# Replacement\n"), 0444); err != nil {
		t.Fatal(err)
	}
	restarted := replicaFixture(t, replacement, state)
	activity, err := restarted.briefState.RollingActivity(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if activity.Last7Days.VisitDays != 1 || activity.Last7Days.OpenCount != 1 {
		t.Fatalf("lost activity: %+v", activity)
	}
	rec := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "data-brief-state-unreadable") {
		t.Fatal("deleted targets broke brief")
	}
}

func TestReplicaStaleIsStillReady(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("hello"), 0444); err != nil {
		t.Fatal(err)
	}
	srv := replicaFixture(t, root, state)
	srv.replicaStatusFile = filepath.Join(state, "status.json")
	content := `{"last_successful_sync":"2000-01-01T00:00:00Z"}`
	if err := os.WriteFile(srv.replicaStatusFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/ready", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"stale":true`) {
		t.Fatal(rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Note") || strings.Contains(rec.Body.String(), "hello") {
		t.Fatal("readiness leaked content")
	}
}

// A refused method names the ones the probe accepts, as every other 405 does.
func TestReplicaReadyRefusalNamesAllowedMethods(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("hello"), 0444); err != nil {
		t.Fatal(err)
	}
	srv := replicaFixture(t, root, state)
	rec := httptest.NewRecorder()
	srv.handleReady(rec, httptest.NewRequest(http.MethodPut, "/api/ready", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

func replicaCard(t *testing.T, srv *Server) *brief.Card {
	t.Helper()
	snap, err := srv.state.currentSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := srv.newBriefRuntime(snap)
	if err != nil {
		t.Fatal(err)
	}
	daily, err := runtime.service.Daily(runtime.selection.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var card *brief.Card
	for _, slot := range daily.Brief.Slots {
		if slot.Card != nil {
			card = slot.Card
			break
		}
	}
	if card == nil {
		t.Fatal("no card")
	}

	return card
}

func openAndReplaceReplicaCard(t *testing.T, srv *Server, card *brief.Card) {
	t.Helper()
	got := replicaPost(t, srv, "/api/brief/item", url.Values{"token": {"replica-test"}, "action": {"open"}, "card_id": {string(card.ID)}})
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	var response briefItemResponse
	if err := json.Unmarshal(got.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(response.Destination, "/n/") {
		t.Fatal("missing browser destination")
	}
	got = replicaPost(t, srv, "/api/brief/item", url.Values{"token": {"replica-test"}, "action": {"another"}, "card_id": {string(card.ID)}})
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
}

func writeReplicaNotes(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"Alpha.md", "Beta.md", "Gamma.md", "Delta.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# Shared ideas\n"), 0444); err != nil {
			t.Fatal(err)
		}
	}
}
