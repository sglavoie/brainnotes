package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// queryAPIVaultFiles is the fixture both endpoints read: a few tags, a folder,
// a two-word title to link at, and a declared property with values.
var queryAPIVaultFiles = map[string][]byte{
	"Deep Work.md":     []byte("---\ntags: [focus, wip]\nstatus: wip\nrating: 5\n---\nA note about focus.\n"),
	"Shallow Work.md":  []byte("---\ntags: [wip]\nstatus: done\nrating: 3\n---\nLinks to [[Deep Work]].\n"),
	"Archive/Old.md":   []byte("---\ntags: [focus]\nstatus: done\n---\nArchived.\n"),
	"Areas/Café Au.md": []byte("---\ntags: [café]\n---\nAccented title.\n"),
	"Notes/Review.md":  []byte("---\nstatus: in review\n---\nA status value with a space in it.\n"),
}

func newQueryAPIServer(t *testing.T) *httpTestServer {
	t.Helper()
	ts, root := newHTTPServerForFilesWithOptions(t, queryAPIVaultFiles, func(cfg *config.Config) {
		cfg.Frontmatter = config.FrontmatterConfig{
			Properties: map[string]config.PropertyDefinition{
				"tags":     {Type: config.KindList, Items: config.KindString},
				"status":   {Type: config.KindString},
				"rating":   {Type: config.KindInt},
				"reviewed": {Type: config.KindDate},
			},
		}
	}, Options{})
	return &httpTestServer{URL: ts.URL, Root: root, t: t}
}

type httpTestServer struct {
	URL  string
	Root string
	t    *testing.T
}

func (s *httpTestServer) validate(params url.Values) queryValidateResponse {
	s.t.Helper()
	var payload queryValidateResponse
	s.getJSON(queryValidateAPIRoutePath, params, http.StatusOK, &payload)
	return payload
}

func (s *httpTestServer) completions(params url.Values) queryCompletionsResponse {
	s.t.Helper()
	var payload queryCompletionsResponse
	s.getJSON(queryCompletionsAPIRoutePath, params, http.StatusOK, &payload)
	return payload
}

func (s *httpTestServer) getJSON(path string, params url.Values, wantStatus int, out any) {
	s.t.Helper()
	target := s.URL + path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	resp, err := http.Get(target) // #nosec G107 -- test-only local URL
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != wantStatus {
		s.t.Fatalf("GET %s status = %d, want %d; body=%q", path, resp.StatusCode, wantStatus, string(body))
	}
	if got, want := resp.Header.Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		s.t.Fatalf("content type = %q, want %q", got, want)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			s.t.Fatalf("decode %s: %v; body=%q", path, err, string(body))
		}
	}
}

func TestQueryValidateAPI_AcceptsAValidExpression(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.validate(url.Values{"expr": {"tag:focus and not folder:Archive"}})
	if !payload.OK {
		t.Fatalf("ok = false, want true; payload=%#v", payload)
	}
	if payload.Error != nil {
		t.Fatalf("error = %#v, want nil", payload.Error)
	}
	if got, want := payload.Normalized, "tag:focus and not folder:Archive"; got != want {
		t.Errorf("normalized = %q, want %q", got, want)
	}
	if got, want := strings.Join(payload.FieldsUsed, ","), "folder,tag"; got != want {
		t.Errorf("fields_used = %q, want %q", got, want)
	}
	if payload.NeedsGraph {
		t.Errorf("needs_graph = true, want false for a tag/folder query")
	}
}

func TestQueryValidateAPI_ReportsFieldsUsedAndGraphNeed(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.validate(url.Values{"expr": {"near:[[Deep Work]]<=2 or orphan:true"}})
	if !payload.OK {
		t.Fatalf("ok = false, want true; error=%#v", payload.Error)
	}
	if got, want := strings.Join(payload.FieldsUsed, ","), "near,orphan"; got != want {
		t.Errorf("fields_used = %q, want %q", got, want)
	}
	if !payload.NeedsGraph {
		t.Errorf("needs_graph = false, want true")
	}
}

func TestQueryValidateAPI_EmptyExpressionIsValid(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.validate(nil)
	if !payload.OK {
		t.Fatalf("ok = false for an absent expr; payload=%#v", payload)
	}
	if payload.FieldsUsed == nil {
		t.Errorf("fields_used = null, want an empty array")
	}
	if len(payload.FieldsUsed) != 0 {
		t.Errorf("fields_used = %#v, want empty", payload.FieldsUsed)
	}
}

func TestQueryValidateAPI_ReportsUnknownFieldWithSuggestions(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.validate(url.Values{"expr": {"tag:focus and statuz:wip"}})
	if payload.OK {
		t.Fatalf("ok = true, want false; payload=%#v", payload)
	}
	if payload.Error == nil {
		t.Fatal("error = nil, want an unknown-field error")
	}
	if !strings.Contains(payload.Error.Message, "statuz") {
		t.Errorf("message = %q, want it to name the unknown field", payload.Error.Message)
	}
	if !slices.Contains(payload.Error.Suggestions, "status") {
		t.Errorf("suggestions = %#v, want them to include status", payload.Error.Suggestions)
	}
	if !payload.Error.Positioned {
		t.Errorf("positioned = false, want true for a spanned error")
	}
	if got, want := payload.Error.Start, len("tag:focus and "); got != want {
		t.Errorf("start = %d, want %d", got, want)
	}
	if got, want := payload.Error.End, len("tag:focus and statuz"); got != want {
		t.Errorf("end = %d, want %d", got, want)
	}
	if got, want := payload.Error.Line, 1; got != want {
		t.Errorf("line = %d, want %d", got, want)
	}
	// fields_used survives a compile failure: the query parsed, so the fields
	// it did name are still worth reporting.
	if got, want := strings.Join(payload.FieldsUsed, ","), "statuz,tag"; got != want {
		t.Errorf("fields_used = %q, want %q", got, want)
	}
}

// TestQueryValidateAPI_ConvertsOffsetsToUTF16CodeUnits is the contract test for
// the offset conversion. The fixture puts one astral rune (4 bytes, 1 Go rune,
// *2* UTF-16 code units) before the offending field, so the three candidate
// answers are all different and only the UTF-16 one passes.
func TestQueryValidateAPI_ConvertsOffsetsToUTF16CodeUnits(t *testing.T) {
	ts := newQueryAPIServer(t)

	const expr = "tag:\U0001F3A7 statuz:wip"
	const prefix = "tag:\U0001F3A7 "

	// Pin the fixture's own arithmetic, so a later edit cannot quietly make the
	// three encodings agree and turn this into a vacuous test.
	if got, want := len(prefix), 9; got != want {
		t.Fatalf("fixture prefix is %d bytes, want %d", got, want)
	}
	if got, want := len([]rune(prefix)), 6; got != want {
		t.Fatalf("fixture prefix is %d runes, want %d", got, want)
	}

	payload := ts.validate(url.Values{"expr": {expr}})
	if payload.OK || payload.Error == nil {
		t.Fatalf("ok = %v with error %#v, want a rejection", payload.OK, payload.Error)
	}

	// 4 ASCII bytes + 2 surrogate halves + 1 space = 7 UTF-16 code units.
	if got, want := payload.Error.Start, 7; got != want {
		t.Errorf("start = %d, want %d (byte offset is 9, rune offset is 6)", got, want)
	}
	if got, want := payload.Error.End, 7+len("statuz"); got != want {
		t.Errorf("end = %d, want %d", got, want)
	}
	if got, want := payload.Error.Column, 8; got != want {
		t.Errorf("column = %d, want %d (byte column is 10, rune column is 7)", got, want)
	}
	if got, want := payload.Error.Line, 1; got != want {
		t.Errorf("line = %d, want %d", got, want)
	}
}

func TestQueryValidateAPI_RejectsUnresolvableGraphTarget(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.validate(url.Values{"expr": {"links-to:[[Deep Wrok]]"}})
	if payload.OK {
		t.Fatalf("ok = true, want false for an unresolvable target")
	}
	if payload.Error == nil {
		t.Fatal("error = nil, want a target error")
	}
	if !slices.Contains(payload.Error.Suggestions, "Deep Work") {
		t.Errorf("suggestions = %#v, want them to include the real title", payload.Error.Suggestions)
	}
}

func TestQueryValidateAPI_MethodAndPathGuards(t *testing.T) {
	ts := newQueryAPIServer(t)
	assertReadOnlyRouteGuards(t, ts.URL, queryValidateAPIRoutePath)
}

func TestQueryCompletionsAPI_MethodAndPathGuards(t *testing.T) {
	ts := newQueryAPIServer(t)
	assertReadOnlyRouteGuards(t, ts.URL, queryCompletionsAPIRoutePath)
}

// assertReadOnlyRouteGuards pins the read-only handler shape both routes share
// with /api/facets: GET only, and an exact path (Go's mux would otherwise treat
// a trailing-slash pattern as a subtree).
func assertReadOnlyRouteGuards(t *testing.T, baseURL, routePath string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, baseURL+routePath, nil)
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", routePath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST %s status = %d, want %d", routePath, resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Errorf("POST %s Allow = %q, want %q", routePath, got, http.MethodGet)
	}

	sub, err := http.Get(baseURL + routePath + "/anything") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s/anything: %v", routePath, err)
	}
	defer sub.Body.Close()
	if sub.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s/anything status = %d, want %d", routePath, sub.StatusCode, http.StatusNotFound)
	}
}
