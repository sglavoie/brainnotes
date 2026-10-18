package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestSyncPage_NamesEveryAttributeTheReviewSurfaceReads is the Go half of the
// contract between internal/web/templates/sync.html and the jsdom fixture in
// static_src/sync.test-helpers.ts.
//
// That fixture is a second expression of this markup, because a Node process
// cannot render a Go template. The *attribute names* are the whole contract
// between them, and a name asserted only in the fixture can be renamed in this
// template with the entire frontend suite staying green while the control it
// names goes inert on the page a browser is actually served. So every attribute
// the review surface reads is pinned here, against a real response — this is the
// same list editor_page_test.go keeps for /edit, and it exists because that page
// learned the lesson first.
func TestSyncPage_NamesEveryAttributeTheReviewSurfaceReads(t *testing.T) {
	world := newSyncWorld(t)

	code, body := world.get(t, syncRoutePath)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", syncRoutePath, code, body)
	}

	for _, want := range []string{
		// The status panel: where it reads from, and the three regions it writes.
		`data-sync-panel`,
		`data-sync-status-endpoint="` + syncStatusAPIRoutePath + `"`,
		`data-sync-refresh`,
		`data-sync-summary`,
		`data-sync-facts`,
		`data-sync-problems`,

		// The review panel: the three routes a decision travels through, the token
		// it carries, the identity a merge is kept under, and the two regions.
		`data-sync-conflicts`,
		`data-sync-conflicts-endpoint="` + syncConflictsAPIRoutePath + `"`,
		`data-sync-preview-endpoint="` + syncPreviewAPIRoutePath + `"`,
		`data-sync-resolve-endpoint="` + syncResolveAPIRoutePath + `"`,
		`data-sync-csrf="` + syncTestToken + `"`,
		`data-sync-vault`,
		`data-sync-conflicts-status`,
		`data-sync-operations`,

		`data-sync-details`,
		`data-sync-checked`,
		`data-sync-checked-exact`,
	} {
		if !namesAttribute(body, want) {
			t.Fatalf("sync page is missing %q", want)
		}
	}
	if namesAttribute(body, "data-sync-line") {
		t.Fatal("the Sync page duplicates its summary in the global sync line")
	}
}

// TestSyncPage_KeysAMergeToTheSameVaultTheEditorKeysADraftTo pins the identity
// itself rather than the attribute.
//
// A merge composed here and a draft typed on /edit are the same person's text in
// the same browser against the same vault, and both are keyed by this value. If
// the two pages ever published different identities, a merge would be kept under
// a name nothing reads back — which is indistinguishable, from the page, from
// keeping nothing at all.
func TestSyncPage_KeysAMergeToTheSameVaultTheEditorKeysADraftTo(t *testing.T) {
	world := newSyncWorld(t)

	identity := world.srv.vaultIdentity()
	if identity == "" {
		t.Fatal("the served vault has no identity, so no merge can be kept")
	}
	if !strings.HasPrefix(identity, enrolledVaultIdentityPrefix) {
		t.Fatalf("identity = %q, want the enrolled vault's own name", identity)
	}

	_, sync := world.get(t, syncRoutePath)
	if !strings.Contains(sync, `data-sync-vault="`+identity+`"`) {
		t.Fatalf("sync page does not publish identity %q", identity)
	}
	_, editor := world.get(t, editorRoutePath+"?key=alpha")
	if !strings.Contains(editor, `data-source-vault-id="`+identity+`"`) {
		t.Fatalf("editor page does not publish identity %q", identity)
	}
}

// TestSyncPage_AnswersOnlyAGet keeps the page a read. Every write on this surface
// is one of the three APIs, each of which checks the token; a page route that
// accepted a POST would be a fourth door with no such check.
func TestSyncPage_AnswersOnlyAGet(t *testing.T) {
	world := newSyncWorld(t)

	resp, err := http.Post(world.ts.URL+syncRoutePath, "application/json", strings.NewReader("{}")) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("POST %s: %v", syncRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST %s = %d, want %d: %s", syncRoutePath, resp.StatusCode, http.StatusMethodNotAllowed, body)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodGet)
	}
}

// TestSyncPage_IsNotServedUnderAnotherPath pins the exact-path check. The route is
// registered on a prefix, so without it /sync/anything would render this page —
// and a review surface reachable under paths nobody audited is a surface whose
// guard coverage is a guess.
func TestSyncPage_IsNotServedUnderAnotherPath(t *testing.T) {
	world := newSyncWorld(t)

	code, body := world.get(t, syncRoutePath+"/pkg-1")
	if code != http.StatusNotFound {
		t.Fatalf("GET %s/pkg-1 = %d, want 404: %s", syncRoutePath, code, body)
	}
}
