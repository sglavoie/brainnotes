package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func propertiesTestVault() map[string][]byte {
	return map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\nstatus: active\ntags:\n  - go\n  - go\n  - web\n---\nBody\n"),
		"Notes/Beta.md":  []byte("---\ntitle: Beta\nstatus: retired\ntags:\n  - go\n---\nBody\n"),
		"Notes/Gamma.md": []byte("---\ntitle: Gamma\nmeta:\n  nested: 1\n---\nBody\n"),
	}
}

func propertiesTestConfig(cfg *config.Config) {
	cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true, Allowed: []string{"active", "draft"}},
		"area":   {Type: config.KindWikilink},
	}
}

func getJSON(t *testing.T, ts *httptest.Server, path string, out any) *http.Response {
	t.Helper()

	resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body := readResponseBody(t, resp)
	if out != nil {
		if err := json.Unmarshal([]byte(body), out); err != nil {
			t.Fatalf("decode %s: %v (body %s)", path, err, body)
		}
	}
	return resp
}

func TestPropertiesAPI_Catalog(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	var payload propertiesResponse
	resp := getJSON(t, ts, propertiesAPIRoutePath, &payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !payload.OK {
		t.Fatalf("ok = false, want true")
	}
	if payload.TotalNotes != 3 {
		t.Fatalf("total_notes = %d, want 3", payload.TotalNotes)
	}
	if payload.Generation != payload.Config.Generation {
		t.Fatalf("generation %d does not match config generation %d", payload.Generation, payload.Config.Generation)
	}

	byName := map[string]propertySummary{}
	for _, summary := range payload.Properties {
		byName[summary.Name] = summary
	}
	status, ok := byName["status"]
	if !ok {
		t.Fatalf("status missing from catalog; have %v", payload.Properties)
	}
	if !status.Configured || status.Definition == nil {
		t.Fatalf("status = %+v, want a configured property with a definition", status)
	}
	if status.Definition.Type != "string" || !status.Definition.Required {
		t.Fatalf("status definition = %+v, want a required string", status.Definition)
	}
	if status.Path != "/properties/status" {
		t.Fatalf("status path = %q, want /properties/status", status.Path)
	}
	if status.MissingRequired != 1 {
		t.Fatalf("status missing_required = %d, want 1", status.MissingRequired)
	}
	if status.Violations != status.ViolationCounts.Total() {
		t.Fatalf("violations %d does not match counts %+v", status.Violations, status.ViolationCounts)
	}

	tags := byName["tags"]
	if tags.Configured {
		t.Fatalf("tags reported as configured")
	}
	if tags.DistinctValues != 2 {
		t.Fatalf("tags distinct_values = %d, want 2 (a repeated list item counts once per note)", tags.DistinctValues)
	}
	if tags.Coverage <= 0 || tags.Coverage > 1 {
		t.Fatalf("tags coverage = %v, want a fraction", tags.Coverage)
	}

	area := byName["area"]
	if !area.Configured || area.NoteCount != 0 {
		t.Fatalf("area = %+v, want a configured property with no notes", area)
	}

	meta := byName["meta"]
	if meta.UnsupportedValues != 1 || meta.UnsupportedNotes != 1 {
		t.Fatalf("meta = %+v, want one unsupported value in one note", meta)
	}
}

func TestPropertiesAPI_CatalogSortsIssuesFirst(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	var payload propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &payload)

	if len(payload.Properties) < 2 {
		t.Fatalf("catalog = %d properties, want several", len(payload.Properties))
	}
	for i := 1; i < len(payload.Properties); i++ {
		previous, current := payload.Properties[i-1], payload.Properties[i]
		if previous.Issues < current.Issues {
			t.Fatalf("property %q (%d issues) sorted before %q (%d issues)",
				previous.Name, previous.Issues, current.Name, current.Issues)
		}
		if previous.Issues == current.Issues && previous.NoteCount < current.NoteCount {
			t.Fatalf("property %q (%d notes) sorted before %q (%d notes)",
				previous.Name, previous.NoteCount, current.Name, current.NoteCount)
		}
	}
	if payload.Properties[0].Name != "status" {
		t.Fatalf("first property = %q, want status (the one with the most issues)", payload.Properties[0].Name)
	}
}

func TestPropertiesAPI_CatalogIncludesProposals(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	var payload propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &payload)

	var found bool
	for _, proposal := range payload.Proposals {
		if proposal.Property == "tags" {
			found = true
			if proposal.Type != config.KindList || proposal.Items != config.KindString {
				t.Fatalf("tags proposal = %+v, want a list of string", proposal)
			}
			if !strings.Contains(proposal.TOML, "[frontmatter.properties.tags]") {
				t.Fatalf("tags proposal TOML = %q, want a definition block", proposal.TOML)
			}
		}
		if proposal.Property == "status" {
			t.Fatalf("proposed a definition for the already configured status property")
		}
	}
	if !found {
		t.Fatalf("no proposal for tags; got %+v", payload.Proposals)
	}
	if payload.Totals.Proposals != len(payload.Proposals) {
		t.Fatalf("totals.proposals = %d, want %d", payload.Totals.Proposals, len(payload.Proposals))
	}
}

func TestPropertiesAPI_CatalogRejectsNonGET(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	resp, err := http.Post(ts.URL+propertiesAPIRoutePath, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestPropertiesAPI_Detail(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	var payload propertyDetailResponse
	resp := getJSON(t, ts, propertiesDetailAPIRoutePath+"?property=tags", &payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if payload.Property.Name != "tags" {
		t.Fatalf("property = %q, want tags", payload.Property.Name)
	}
	if len(payload.Values) != 2 || payload.Values[0].Value.Text != "go" || payload.Values[0].Notes != 2 {
		t.Fatalf("values = %+v, want go in 2 notes then web", payload.Values)
	}
	if len(payload.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(payload.Rows))
	}
	if payload.Rows[0].RelPath != "Notes/Alpha.md" {
		t.Fatalf("first row = %q, want Notes/Alpha.md", payload.Rows[0].RelPath)
	}
	if payload.Rows[0].Value == nil || payload.Rows[0].Value.Kind != config.KindList {
		t.Fatalf("first row value = %+v, want a list", payload.Rows[0].Value)
	}
	if payload.Filter.Status != propertyFilterAll {
		t.Fatalf("filter = %+v, want the normalized all filter echoed back", payload.Filter)
	}
	if payload.Pagination.Limit != propertyRowLimitDefault {
		t.Fatalf("limit = %d, want the default %d", payload.Pagination.Limit, propertyRowLimitDefault)
	}
	if payload.Pagination.Total != 2 || payload.Pagination.Returned != 2 || payload.Pagination.HasMore {
		t.Fatalf("pagination = %+v, want both rows on one page", payload.Pagination)
	}
}

func TestPropertiesAPI_DetailUnsupportedValueStaysSafe(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	var payload propertyDetailResponse
	getJSON(t, ts, propertiesDetailAPIRoutePath+"?property=meta", &payload)

	if len(payload.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(payload.Rows))
	}
	row := payload.Rows[0]
	if !row.Unsupported {
		t.Fatalf("row = %+v, want it flagged as unsupported", row)
	}
	if row.Value == nil || row.Value.Supported {
		t.Fatalf("row value = %+v, want an unsupported value", row.Value)
	}
	if row.Value.Text != "<map>" || row.Value.Value != nil {
		t.Fatalf("row value = %+v, want a label-only representation", row.Value)
	}
}

func TestPropertiesAPI_DetailRowFilters(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"all", "property=status", []string{"Notes/Alpha.md", "Notes/Beta.md", "Notes/Gamma.md"}},
		{"violations", "property=status&filter=violations", []string{"Notes/Beta.md", "Notes/Gamma.md"}},
		{"missing", "property=status&filter=missing", []string{"Notes/Gamma.md"}},
		{"value", "property=status&value=active", []string{"Notes/Alpha.md"}},
		{"query", "property=status&q=beta", []string{"Notes/Beta.md"}},
		{"unsupported", "property=meta&filter=unsupported", []string{"Notes/Gamma.md"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var payload propertyDetailResponse
			getJSON(t, ts, propertiesDetailAPIRoutePath+"?"+testCase.query, &payload)

			got := make([]string, 0, len(payload.Rows))
			for _, row := range payload.Rows {
				got = append(got, row.RelPath)
			}
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("rows = %v, want %v", got, testCase.want)
			}
			if payload.Pagination.Total != len(testCase.want) {
				t.Fatalf("total = %d, want %d: the filter must run before pagination",
					payload.Pagination.Total, len(testCase.want))
			}
		})
	}
}

func TestPropertiesAPI_DetailValueFilterIsCaseSensitive(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	var payload propertyDetailResponse
	getJSON(t, ts, propertiesDetailAPIRoutePath+"?property=status&value=ACTIVE", &payload)

	if len(payload.Rows) != 0 {
		t.Fatalf("rows = %d, want 0: allowed values are case-sensitive everywhere else", len(payload.Rows))
	}
}

func TestPropertiesAPI_DetailPagination(t *testing.T) {
	files := map[string][]byte{}
	for i := 0; i < 260; i++ {
		rel := fmt.Sprintf("Notes/Note%03d.md", i)
		files[rel] = []byte(fmt.Sprintf("---\ntitle: Note %03d\nstatus: active\n---\nBody\n", i))
	}
	ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{})

	cases := []struct {
		name         string
		query        string
		wantLimit    int
		wantReturned int
		wantHasMore  bool
	}{
		{"default limit", "property=status", 50, 50, true},
		{"explicit limit", "property=status&limit=10", 10, 10, true},
		{"limit clamped to maximum", "property=status&limit=5000", propertyRowLimitMax, propertyRowLimitMax, true},
		{"offset past the end", "property=status&offset=1000", 50, 0, false},
		{"last page", "property=status&limit=200&offset=200", 200, 60, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var payload propertyDetailResponse
			getJSON(t, ts, propertiesDetailAPIRoutePath+"?"+testCase.query, &payload)

			if payload.Pagination.Limit != testCase.wantLimit {
				t.Fatalf("limit = %d, want %d", payload.Pagination.Limit, testCase.wantLimit)
			}
			if payload.Pagination.Returned != testCase.wantReturned || len(payload.Rows) != testCase.wantReturned {
				t.Fatalf("returned = %d (rows %d), want %d",
					payload.Pagination.Returned, len(payload.Rows), testCase.wantReturned)
			}
			if payload.Pagination.HasMore != testCase.wantHasMore {
				t.Fatalf("has_more = %v, want %v", payload.Pagination.HasMore, testCase.wantHasMore)
			}
			if payload.Pagination.Total != 260 {
				t.Fatalf("total = %d, want 260", payload.Pagination.Total)
			}
		})
	}
}

func TestPropertiesAPI_DetailRejectsBadRequests(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	cases := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"missing property", "", http.StatusBadRequest},
		{"blank property", "property=%20", http.StatusBadRequest},
		{"unknown property", "property=nope", http.StatusNotFound},
		{"unknown filter", "property=status&filter=weird", http.StatusBadRequest},
		{"non-numeric limit", "property=status&limit=abc", http.StatusBadRequest},
		{"zero limit", "property=status&limit=0", http.StatusBadRequest},
		{"negative offset", "property=status&offset=-1", http.StatusBadRequest},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resp := getJSON(t, ts, propertiesDetailAPIRoutePath+"?"+testCase.query, nil)
			if resp.StatusCode != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, testCase.wantStatus)
			}
		})
	}
}

func TestPropertiesAPI_DetailHandlesNamesNeedingEscaping(t *testing.T) {
	files := map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n\"my prop/sub\": value\n---\nBody\n"),
	}
	ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{})

	var catalog propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &catalog)

	var path string
	for _, summary := range catalog.Properties {
		if summary.Name == "my prop/sub" {
			path = summary.Path
		}
	}
	if path == "" {
		t.Fatalf("property with a space and slash missing from catalog: %+v", catalog.Properties)
	}
	if strings.Contains(strings.TrimPrefix(path, propertiesRoutePrefix), "/") {
		t.Fatalf("path = %q, want the property name escaped into one path segment", path)
	}

	var detail propertyDetailResponse
	getJSON(t, ts, propertiesDetailAPIRoutePath+"?property="+url.QueryEscape("my prop/sub"), &detail)
	if detail.Property.Name != "my prop/sub" {
		t.Fatalf("property = %q, want the escaped name resolved", detail.Property.Name)
	}
}

func TestPropertiesAPI_ReportsConfigStatus(t *testing.T) {
	ts, _, _ := newReloadableServer(t, reloadableConfigTOML, Options{})

	var payload propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &payload)

	if payload.Config.Status != ReloadOK || !payload.Config.Writable {
		t.Fatalf("config = %+v, want a valid writable configuration", payload.Config)
	}
	if payload.Generation != payload.Config.Generation {
		t.Fatalf("generation %d does not match config generation %d", payload.Generation, payload.Config.Generation)
	}
}

func TestPropertiesAPI_ReflectsConfigurationReload(t *testing.T) {
	ts, _, root := newReloadableServer(t, "[frontmatter.properties.title]\ntype = \"string\"\n", Options{})

	var before propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &before)
	for _, summary := range before.Properties {
		if summary.Name == "status" && summary.Configured {
			t.Fatalf("status configured before the definition was written")
		}
	}

	rewriteConfig(t, root, "[frontmatter.properties.title]\ntype = \"string\"\n\n"+
		"[frontmatter.properties.status]\ntype = \"string\"\nrequired = true\n")

	var after propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &after)
	var status propertySummary
	for _, summary := range after.Properties {
		if summary.Name == "status" {
			status = summary
		}
	}
	if !status.Configured {
		t.Fatalf("status still unconfigured after the definition was written; the property index must follow the configuration generation")
	}
	if status.Definition == nil || !status.Definition.Required {
		t.Fatalf("status definition = %+v, want the newly required definition", status.Definition)
	}
	if after.Config.Generation <= before.Config.Generation {
		t.Fatalf("generation did not advance: %d then %d", before.Config.Generation, after.Config.Generation)
	}
}

func TestPropertiesAPI_ServesCatalogWhileConfigIsInvalid(t *testing.T) {
	ts, _, root := newReloadableServer(t, reloadableConfigTOML, Options{})
	rewriteConfig(t, root, "[frontmatter.properties.status]\ntype = \"nonsense\"\n")

	var payload propertiesResponse
	resp := getJSON(t, ts, propertiesAPIRoutePath, &payload)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d: an invalid configuration is a reported state, not a request error",
			resp.StatusCode, http.StatusOK)
	}
	if payload.Config.Status != ReloadInvalid || payload.Config.Writable {
		t.Fatalf("config = %+v, want an invalid, non-writable configuration", payload.Config)
	}
	if payload.Config.Error == "" {
		t.Fatalf("config error is empty; the validation failure must be reported")
	}
	if payload.TotalNotes == 0 {
		t.Fatalf("catalog is empty; the last valid configuration must stay published")
	}
}
