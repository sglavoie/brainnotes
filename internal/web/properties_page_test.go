package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPropertiesPage_Renders(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	resp, err := http.Get(ts.URL + propertiesRoutePath) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", propertiesRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		`href="/static/app.css?v=properties-workbench-2"`,
		"data-properties-catalog",
		`data-properties-endpoint="/api/properties"`,
		`data-properties-detail-endpoint="/api/properties/detail"`,
		"data-properties-csrf=",
		"data-properties-list",
		"data-properties-proposal-list",
		"data-properties-config-open",
		"data-properties-dialog-host",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalog page missing %q", want)
		}
	}
}

func TestPropertiesPage_AppearsInSiteNav(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	for _, path := range []string{"/", libraryRoutePath, healthRoutePath, propertiesRoutePath} {
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if !strings.Contains(body, `<a href="/properties">`) && !strings.Contains(body, `<a href="/properties" aria-current="page">`) {
			t.Fatalf("%s is missing the Properties navigation link", path)
		}
	}
}

func TestPropertyDetailPage_Renders(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	resp, err := http.Get(ts.URL + "/properties/status") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET /properties/status: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		"data-property-detail",
		`data-property-name="status"`,
		"data-property-values",
		"data-property-rows",
		"data-property-filter-status",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail page missing %q", want)
		}
	}
}

func TestPropertyDetailPage_EscapedName(t *testing.T) {
	files := map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n\"my prop\": value\n---\nBody\n"),
	}
	ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{})

	resp, err := http.Get(ts.URL + propertiesRoutePrefix + url.PathEscape("my prop")) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET escaped property page: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `data-property-name="my prop"`) {
		t.Fatalf("detail page did not decode the escaped property name")
	}
}

// TestPropertyDetailPage_EscapedSlashName follows the exact URL the catalog
// publishes rather than calling propertyDetailPath, because the two sides only
// agree once the handler reads the escaped path.
func TestPropertyDetailPage_EscapedSlashName(t *testing.T) {
	const name = "my prop/sub"
	files := map[string][]byte{
		"Notes/Alpha.md": []byte("---\ntitle: Alpha\n\"" + name + "\": value\n---\nBody\n"),
	}
	ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{})

	var catalog propertiesResponse
	getJSON(t, ts, propertiesAPIRoutePath, &catalog)

	var path string
	for _, summary := range catalog.Properties {
		if summary.Name == name {
			path = summary.Path
		}
	}
	if path == "" {
		t.Fatalf("property %q missing from catalog: %+v", name, catalog.Properties)
	}

	resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `data-property-name="my prop/sub"`) {
		t.Fatalf("detail page did not decode the catalog URL back to %q", name)
	}

	multiSegment, err := http.Get(ts.URL + propertiesRoutePrefix + "my%20prop/sub") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET multi-segment property route: %v", err)
	}
	defer multiSegment.Body.Close()
	if multiSegment.StatusCode != http.StatusNotFound {
		t.Fatalf("multi-segment route status = %d, want %d", multiSegment.StatusCode, http.StatusNotFound)
	}
}

func TestPropertyDetailPage_UnknownPropertyIsNotFound(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	resp, err := http.Get(ts.URL + "/properties/nope") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET /properties/nope: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestPropertyDetailPage_BareTrailingSlashRedirects(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, propertiesTestVault(), propertiesTestConfig, Options{})

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(ts.URL + propertiesRoutePrefix)
	if err != nil {
		t.Fatalf("GET %s: %v", propertiesRoutePrefix, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
	}
	if got := resp.Header.Get("Location"); got != propertiesRoutePath {
		t.Fatalf("Location = %q, want %q", got, propertiesRoutePath)
	}
}

func TestPropertyNameFromPath(t *testing.T) {
	cases := []struct {
		path     string
		wantName string
		wantOK   bool
	}{
		{"/properties/status", "status", true},
		{"/properties/my%20prop", "my prop", true},
		{"/properties/my%20prop%2Fsub", "my prop/sub", true},
		{"/properties/", "", false},
		{"/properties/a/b", "", false},
		{"/properties/my%20prop/sub", "", false},
		{"/properties", "", false},
		{"/properties/%20", "", false},
	}

	for _, testCase := range cases {
		name, ok := propertyNameFromPath(testCase.path)
		if ok != testCase.wantOK || name != testCase.wantName {
			t.Fatalf("propertyNameFromPath(%q) = (%q, %v), want (%q, %v)",
				testCase.path, name, ok, testCase.wantName, testCase.wantOK)
		}
	}
}

func TestPropertyDetailPath(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"status", "/properties/status"},
		{"my prop", "/properties/my%20prop"},
		{"a/b", "/properties/a%2Fb"},
		{"  ", propertiesRoutePath},
	}

	for _, testCase := range cases {
		if got := propertyDetailPath(testCase.name); got != testCase.want {
			t.Fatalf("propertyDetailPath(%q) = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
