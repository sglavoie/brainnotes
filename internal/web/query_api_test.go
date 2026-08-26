package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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
	if !containsString(payload.Error.Suggestions, "status") {
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
	if !containsString(payload.Error.Suggestions, "Deep Work") {
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

func TestQueryCompletionsAPI_CompletesFieldNames(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.completions(url.Values{"expr": {"ta"}, "pos": {"2"}})
	if !payload.OK {
		t.Fatalf("ok = false; payload=%#v", payload)
	}
	if got, want := payload.Kind, "field"; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if got, want := payload.Prefix, "ta"; got != want {
		t.Errorf("prefix = %q, want %q", got, want)
	}
	if payload.Replace != (queryReplaceRange{Start: 0, End: 2}) {
		t.Errorf("replace = %+v, want {0 2}", payload.Replace)
	}

	tag := findCompletion(payload.Completions, "tag")
	if tag == nil {
		t.Fatalf("completions = %#v, want one for tag", payload.Completions)
	}
	if got, want := tag.Insert, "tag:"; got != want {
		t.Errorf("tag insert = %q, want %q", got, want)
	}
	if got, want := tag.Kind, "field"; got != want {
		t.Errorf("tag kind = %q, want %q", got, want)
	}
	if strings.TrimSpace(tag.Detail) == "" {
		t.Errorf("tag detail is empty, want the field hint")
	}
	// A declared property is spelled bare, so it belongs in the same list.
	if rating := findCompletion(payload.Completions, "rating"); rating != nil {
		t.Errorf("rating matched the prefix %q unexpectedly: %#v", payload.Prefix, rating)
	}
	all := ts.completions(url.Values{"expr": {""}})
	if rating := findCompletion(all.Completions, "rating"); rating == nil {
		t.Errorf("completions = %#v, want the declared property rating", all.Completions)
	}
}

func TestQueryCompletionsAPI_CompletesFacetValues(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.completions(url.Values{"expr": {"tag:"}})
	if got, want := payload.Kind, "value"; got != want {
		t.Fatalf("kind = %q, want %q; payload=%#v", got, want, payload)
	}
	if got, want := payload.Field, "tag"; got != want {
		t.Fatalf("field = %q, want %q", got, want)
	}
	if payload.Replace != (queryReplaceRange{Start: 4, End: 4}) {
		t.Errorf("replace = %+v, want {4 4}", payload.Replace)
	}

	// wip is on two notes and focus on two; both come from the facet counts.
	wip := findCompletion(payload.Completions, "wip")
	if wip == nil {
		t.Fatalf("completions = %#v, want one for wip", payload.Completions)
	}
	if wip.Count != 2 {
		t.Errorf("wip count = %d, want 2", wip.Count)
	}
	if got, want := wip.Kind, "tag"; got != want {
		t.Errorf("wip kind = %q, want %q", got, want)
	}

	folders := ts.completions(url.Values{"expr": {"folder:"}})
	if findCompletion(folders.Completions, "Archive") == nil {
		t.Errorf("folder completions = %#v, want Archive", folders.Completions)
	}

	statuses := ts.completions(url.Values{"expr": {"status:d"}})
	if findCompletion(statuses.Completions, "done") == nil {
		t.Errorf("status completions = %#v, want done", statuses.Completions)
	}
	if findCompletion(statuses.Completions, "wip") != nil {
		t.Errorf("status completions = %#v, want the prefix d to exclude wip", statuses.Completions)
	}
}

func TestQueryCompletionsAPI_CompletesPropertyNamesAndValues(t *testing.T) {
	ts := newQueryAPIServer(t)

	names := ts.completions(url.Values{"expr": {"has:rat"}})
	rating := findCompletion(names.Completions, "rating")
	if rating == nil {
		t.Fatalf("has: completions = %#v, want rating", names.Completions)
	}
	if got, want := rating.Kind, "property"; got != want {
		t.Errorf("rating kind = %q, want %q", got, want)
	}

	values := ts.completions(url.Values{"expr": {"prop:rating="}})
	if got, want := values.Field, "prop"; got != want {
		t.Fatalf("field = %q, want %q; payload=%#v", got, want, values)
	}
	if got, want := values.Arg, "rating"; got != want {
		t.Fatalf("arg = %q, want %q", got, want)
	}
	if findCompletion(values.Completions, "5") == nil {
		t.Errorf("prop:rating= completions = %#v, want the stored value 5", values.Completions)
	}

	// A declared property is spelled bare, so its values follow it directly.
	bare := ts.completions(url.Values{"expr": {"rating="}})
	if findCompletion(bare.Completions, "3") == nil {
		t.Errorf("rating= completions = %#v, want the stored value 3", bare.Completions)
	}

	dates := ts.completions(url.Values{"expr": {"age:"}})
	if findCompletion(dates.Completions, "reviewed") == nil {
		t.Errorf("age: completions = %#v, want the declared date property", dates.Completions)
	}
}

func TestQueryCompletionsAPI_CompletesWikilinkTargets(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.completions(url.Values{"expr": {"links-to:[[Deep"}})
	if got, want := payload.Kind, "value"; got != want {
		t.Fatalf("kind = %q, want %q; payload=%#v", got, want, payload)
	}
	if got, want := payload.Prefix, "Deep"; got != want {
		t.Errorf("prefix = %q, want %q", got, want)
	}
	// The replacement covers the [[ the user already typed.
	if payload.Replace != (queryReplaceRange{Start: 9, End: 15}) {
		t.Errorf("replace = %+v, want {9 15}", payload.Replace)
	}

	target := findCompletion(payload.Completions, "Deep Work")
	if target == nil {
		t.Fatalf("completions = %#v, want Deep Work", payload.Completions)
	}
	if got, want := target.Insert, "[[Deep Work]]"; got != want {
		t.Errorf("insert = %q, want %q", got, want)
	}
	if got, want := target.Kind, "note"; got != want {
		t.Errorf("kind = %q, want %q", got, want)
	}

	// A bare target position still inserts the brackets.
	bare := ts.completions(url.Values{"expr": {"near:"}})
	if got := findCompletion(bare.Completions, "Deep Work"); got == nil || got.Insert != "[[Deep Work]]" {
		t.Errorf("near: completions = %#v, want a bracketed Deep Work", bare.Completions)
	}
}

func TestQueryCompletionsAPI_QuotesValuesThatWouldResplit(t *testing.T) {
	ts := newQueryAPIServer(t)

	// A bare value containing a space would re-lex as two terms, so the insert
	// has to quote it even though the user typed no quote.
	payload := ts.completions(url.Values{"expr": {"status:in"}})
	spaced := findCompletion(payload.Completions, "in review")
	if spaced == nil {
		t.Fatalf("completions = %#v, want the spaced status value", payload.Completions)
	}
	if got, want := spaced.Insert, `"in review"`; got != want {
		t.Errorf("insert = %q, want %q", got, want)
	}

	inQuote := ts.completions(url.Values{"expr": {"status:\"do"}})
	if got, want := inQuote.Prefix, "do"; got != want {
		t.Fatalf("prefix = %q, want %q; payload=%#v", got, want, inQuote)
	}
	// The span covers the opening quote, so the insert must supply both.
	if inQuote.Replace != (queryReplaceRange{Start: 7, End: 10}) {
		t.Errorf("replace = %+v, want {7 10}", inQuote.Replace)
	}
	done := findCompletion(inQuote.Completions, "done")
	if done == nil {
		t.Fatalf("completions = %#v, want done", inQuote.Completions)
	}
	if got, want := done.Insert, `"done"`; got != want {
		t.Errorf("insert = %q, want %q", got, want)
	}
}

// TestQueryCompletionsAPI_ReadsPosAsUTF16CodeUnits pins the other half of the
// offset contract: pos arrives in the browser's units, so an astral rune before
// the caret must not shift which token the server thinks is under it.
func TestQueryCompletionsAPI_ReadsPosAsUTF16CodeUnits(t *testing.T) {
	ts := newQueryAPIServer(t)

	const expr = "tag:\U0001F3A7 sta"
	// 4 ASCII + 2 surrogate halves + 1 space + 3 = 10 code units, against 12
	// bytes and 9 runes.
	const caret = 10
	if got, want := len(expr), 12; got != want {
		t.Fatalf("fixture is %d bytes, want %d", got, want)
	}

	payload := ts.completions(url.Values{"expr": {expr}, "pos": {strconv.Itoa(caret)}})
	if got, want := payload.Kind, "field"; got != want {
		t.Fatalf("kind = %q, want %q; payload=%#v", got, want, payload)
	}
	if got, want := payload.Prefix, "sta"; got != want {
		t.Fatalf("prefix = %q, want %q", got, want)
	}
	if got, want := payload.Pos, caret; got != want {
		t.Errorf("pos = %d, want %d", got, want)
	}
	if payload.Replace != (queryReplaceRange{Start: 7, End: 10}) {
		t.Errorf("replace = %+v, want {7 10} in UTF-16 code units", payload.Replace)
	}
	if findCompletion(payload.Completions, "status") == nil {
		t.Errorf("completions = %#v, want status", payload.Completions)
	}
}

func TestQueryCompletionsAPI_RejectsABadPos(t *testing.T) {
	ts := newQueryAPIServer(t)

	for _, pos := range []string{"abc", "-1", "1.5"} {
		resp, err := http.Get(ts.URL + queryCompletionsAPIRoutePath + "?expr=tag:&pos=" + url.QueryEscape(pos)) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET with pos=%q: %v", pos, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("pos=%q status = %d, want %d; body=%q", pos, resp.StatusCode, http.StatusBadRequest, string(body))
		}
	}
}

func TestQueryCompletionsAPI_ClampsAnOutOfRangePos(t *testing.T) {
	ts := newQueryAPIServer(t)

	payload := ts.completions(url.Values{"expr": {"tag:wi"}, "pos": {"9999"}})
	if got, want := payload.Prefix, "wi"; got != want {
		t.Errorf("prefix = %q, want %q; payload=%#v", got, want, payload)
	}
	if got, want := payload.Pos, 6; got != want {
		t.Errorf("pos = %d, want it clamped to %d", got, want)
	}
}

func TestQueryCompletionsAPI_MatchesAccentedValuesAcrossUnicodeForms(t *testing.T) {
	ts := newQueryAPIServer(t)

	// The vault stores the composed spelling; a decomposed prefix must still
	// match it, because the compiler NFC-normalizes atom text either way.
	// Written with explicit escapes: two literal spellings of the same accented
	// word collapse to one form in Go source and make the test vacuous.
	const decomposed = "cafe\u0301"
	const composed = "caf\u00e9"
	if decomposed == composed {
		t.Fatal("fixture spellings are identical; the test would be vacuous")
	}

	payload := ts.completions(url.Values{"expr": {"tag:" + decomposed}})
	if findCompletion(payload.Completions, composed) == nil {
		t.Errorf("completions for a decomposed prefix = %#v, want the composed tag", payload.Completions)
	}
}

func findCompletion(completions []queryCompletion, value string) *queryCompletion {
	for i := range completions {
		if completions[i].Value == value {
			return &completions[i]
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
