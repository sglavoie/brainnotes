package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// recordHistoryEntry journals one change directly, so a test can build a
// history with origins no single HTTP route produces — an import, a companion
// pass, a resolution. Each recorded path is rewritten between the capture and
// the finalize, so the entry describes a real change rather than a no-op.
func recordHistoryEntry(t *testing.T, root, command string, at time.Time, rels ...string) string {
	t.Helper()

	id := "entry-" + at.UTC().Format("20060102150405.000000000")
	rec, err := journal.NewRecorder(
		root,
		noteops.JournalRoot(root),
		command,
		journal.WithClock(func() time.Time { return at }),
		journal.WithIDGenerator(func(time.Time) (string, error) { return id, nil }),
	)
	if err != nil {
		t.Fatalf("new recorder for %q: %v", command, err)
	}
	for _, rel := range rels {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			t.Fatalf("mkdir for %q: %v", rel, err)
		}
		if err := rec.Capture(abs); err != nil {
			t.Fatalf("capture %q: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("after "+command+"\n"), 0o600); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize %q: %v", command, err)
	}
	return entry.ID
}

func getHistory(t *testing.T, baseURL string, query url.Values) historyPayload {
	t.Helper()

	resp := getHistoryResponse(t, baseURL, query)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history status for %q: got %d, want %d; body=%q", query.Encode(), resp.StatusCode, http.StatusOK, body)
	}
	return decodeHistoryResult(t, body)
}

func getHistoryResponse(t *testing.T, baseURL string, query url.Values) *http.Response {
	t.Helper()

	target := baseURL + "/api/history"
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	resp, err := http.Get(target) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	return resp
}

// historyWindow is what a page says about itself. A pager reads all five facts
// together — "two of five, starting at zero, and there is more" — so they are
// asserted as one value rather than as a compound condition per call, which is
// also what lets a failure print the whole window instead of the first field
// that disagreed.
type historyWindow struct {
	Total   int
	Entries int
	Offset  int
	Limit   int
	HasMore bool
}

func historyWindowOf(payload historyPayload) historyWindow {
	return historyWindow{
		Total:   payload.Total,
		Entries: len(payload.Entries),
		Offset:  payload.Offset,
		Limit:   payload.Limit,
		HasMore: payload.HasMore,
	}
}

func historyEntryIDs(payload historyPayload) []string {
	ids := make([]string, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// newHistoryFixture builds a vault whose journal holds one entry of each origin
// this surface distinguishes, oldest first, and returns the base URL, the vault
// root, and the entry identifiers by origin.
func newHistoryFixture(t *testing.T) (string, string, map[string]string) {
	t.Helper()

	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Alpha.md": []byte("alpha\n"),
		"Notes/Beta.md":  []byte("beta\n"),
		"Notes/Gamma.md": []byte("gamma\n"),
		"Notes/Delta.md": []byte("delta\n"),
		// The directories the classifier keys on are settings, and this fixture
		// builds its configuration by hand rather than through the defaults, so
		// they are named here explicitly — the test depends on them.
	}, func(cfg *config.Config) {
		cfg.Attachments.Dir = config.DefaultAttachmentsDir
		cfg.Templates.Dir = "Templates"
	}, Options{CSRFToken: "csrf-token"})

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ids := map[string]string{
		"browser":    recordHistoryEntry(t, root, "bn web source save", base, "Notes/Alpha.md", "attachments/alpha.png"),
		"import":     recordHistoryEntry(t, root, "bn sync apply", base.Add(time.Minute), "Notes/Beta.md"),
		"settings":   recordHistoryEntry(t, root, "bn serve template save", base.Add(2*time.Minute), "Templates/Daily.md"),
		"resolution": recordHistoryEntry(t, root, "bn serve sync resolve", base.Add(3*time.Minute), "Notes/Gamma.md"),
		"companion":  recordHistoryEntry(t, root, "bn-vault-sync", base.Add(4*time.Minute), "Notes/Delta.md"),
	}
	return ts.URL, root, ids
}

// The whole point of the session's "bounded pagination ... beyond the current
// 20-entry view": a client can walk the journal a page at a time and always
// knows how much of it there is.
func TestServer_HistoryAPI_Paginates(t *testing.T) {
	baseURL, _, ids := newHistoryFixture(t)

	first := getHistory(t, baseURL, url.Values{"limit": {"2"}})
	if got := historyWindowOf(first); got != (historyWindow{Total: 5, Entries: 2, Limit: 2, HasMore: true}) {
		t.Fatalf("first page = %#v, want 2 of 5 with more", got)
	}
	// Newest first, so the companion entry leads.
	if got := historyEntryIDs(first); got[0] != ids["companion"] || got[1] != ids["resolution"] {
		t.Fatalf("first page ids = %v, want companion then resolution", got)
	}

	last := getHistory(t, baseURL, url.Values{"limit": {"2"}, "offset": {"4"}})
	if got := historyWindowOf(last); got != (historyWindow{Total: 5, Entries: 1, Offset: 4, Limit: 2}) {
		t.Fatalf("last page = %#v, want the final entry with no more", got)
	}
	if got := historyEntryIDs(last); got[0] != ids["browser"] {
		t.Fatalf("last page ids = %v, want the oldest entry", got)
	}

	// An offset past the end is an empty page, not a failure: a journal that was
	// pruned between two requests is ordinary.
	past := getHistory(t, baseURL, url.Values{"offset": {"99"}})
	want := historyWindow{Total: 5, Offset: 99, Limit: historyEntryLimit}
	if got := historyWindowOf(past); got != want {
		t.Fatalf("past-the-end page = %#v, want an empty page", got)
	}

	// No query at all still answers the page the single panel has always shown.
	def := getHistory(t, baseURL, nil)
	if got := historyWindowOf(def); got != (historyWindow{Total: 5, Entries: 5, Limit: historyEntryLimit}) {
		t.Fatalf("default page = %#v, want the default limit and every entry", got)
	}
}

func TestServer_HistoryAPI_ReportsOriginsAndClasses(t *testing.T) {
	baseURL, _, ids := newHistoryFixture(t)
	payload := getHistory(t, baseURL, nil)

	byID := map[string]historyEntryPayload{}
	for _, entry := range payload.Entries {
		byID[entry.ID] = entry
	}
	assertHistoryOrigins(t, payload, byID, map[string]string{
		ids["browser"]:    "browser",
		ids["import"]:     "import",
		ids["settings"]:   "settings",
		ids["resolution"]: "resolution",
		ids["companion"]:  "companion",
	})

	// The source save is the entry that carries an attachment, and that has to be
	// visible on the row without opening it.
	assertHistorySaveSummary(t, byID[ids["browser"]])
	assertHistoryFilterVocabulary(t, payload)
}

// assertHistorySaveSummary checks the row summarizes what the entry touched
// without being opened: how many files, in which classes, and whether each of
// them is in the content history and reaches the other replica.
func assertHistorySaveSummary(t *testing.T, save historyEntryPayload) {
	t.Helper()

	if save.FileCount != 2 || len(save.Classes) != 2 {
		t.Fatalf("source save = %#v, want two files in two classes", save)
	}
	if save.Classes[0].Class != "note" || save.Classes[0].Count != 1 ||
		save.Classes[1].Class != "attachment" || save.Classes[1].Count != 1 {
		t.Fatalf("source save classes = %#v, want one note and one attachment", save.Classes)
	}
	for _, file := range save.Files {
		if !file.Recorded || !file.Travels {
			t.Errorf("file %#v should be recorded and portable", file)
		}
	}
}

// assertHistoryOrigins checks that each entry was classified by the command it
// recorded, and that the classification came with a label — the row shows both,
// and a value with no label would render as a blank badge beside the command it
// is supposed to be reading.
func assertHistoryOrigins(t *testing.T, payload historyPayload, byID map[string]historyEntryPayload, want map[string]string) {
	t.Helper()

	for id, origin := range want {
		entry, ok := byID[id]
		if !ok {
			t.Fatalf("entry %q missing from %v", id, historyEntryIDs(payload))
		}
		if entry.Origin != origin {
			t.Errorf("entry %q origin = %q, want %q", entry.Command, entry.Origin, origin)
		}
		if entry.OriginLabel == "" {
			t.Errorf("entry %q has no origin label", entry.Command)
		}
	}
}

// assertHistoryFilterVocabulary checks that the reply publishes the whole
// vocabulary rather than the values this page happened to contain. A filter
// offering only what is already visible loses the option that empties the list,
// which is the one a person could then never undo.
func assertHistoryFilterVocabulary(t *testing.T, payload historyPayload) {
	t.Helper()

	if len(payload.Origins) != len(historyOrigins) || len(payload.Classes) != len(historyPathClasses) {
		t.Fatalf("filter vocabulary = %d origins / %d classes, want %d / %d",
			len(payload.Origins), len(payload.Classes), len(historyOrigins), len(historyPathClasses))
	}
	for _, option := range append(append([]historyOptionPayload{}, payload.Origins...), payload.Classes...) {
		if option.Value == "" || option.Label == "" {
			t.Errorf("filter option %#v is missing a value or a label", option)
		}
	}
}

func TestServer_HistoryAPI_Filters(t *testing.T) {
	baseURL, _, ids := newHistoryFixture(t)

	cases := []struct {
		name  string
		query url.Values
		want  []string
	}{
		{"one origin", url.Values{"origin": {"import"}}, []string{ids["import"]}},
		{
			"two origins are a union",
			url.Values{"origin": {"import", "resolution"}},
			[]string{ids["resolution"], ids["import"]},
		},
		{"a repeated origin is not a duplicate", url.Values{"origin": {"import", "import"}}, []string{ids["import"]}},
		{"class picks out the attachment", url.Values{"class": {"attachment"}}, []string{ids["browser"]}},
		{"class picks out the template", url.Values{"class": {"template"}}, []string{ids["settings"]}},
		{"path substring", url.Values{"path": {"beta"}}, []string{ids["import"]}},
		{"path substring is case-folded", url.Values{"path": {"BETA"}}, []string{ids["import"]}},
		{"path matches a directory too", url.Values{"path": {"attachments/"}}, []string{ids["browser"]}},
		{
			"origin and class are an intersection",
			url.Values{"origin": {"browser"}, "class": {"attachment"}},
			[]string{ids["browser"]},
		},
		{
			"an intersection that matches nothing is empty",
			url.Values{"origin": {"import"}, "class": {"attachment"}},
			nil,
		},
		{"an origin with no entries is empty", url.Values{"origin": {"unknown"}}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := getHistory(t, baseURL, tc.query)
			got := historyEntryIDs(payload)
			if len(got) != len(tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ids = %v, want %v", got, tc.want)
				}
			}
			if payload.Total != len(tc.want) {
				t.Fatalf("total = %d, want %d", payload.Total, len(tc.want))
			}
		})
	}
}

// Total counts the filter's matches, not the page's, so a narrowed list can say
// how much of the journal it is showing.
func TestServer_HistoryAPI_TotalCountsMatchesNotThePage(t *testing.T) {
	baseURL, _, _ := newHistoryFixture(t)

	payload := getHistory(t, baseURL, url.Values{"limit": {"1"}, "origin": {"import", "resolution", "companion"}})
	if payload.Total != 3 || len(payload.Entries) != 1 || !payload.HasMore {
		t.Fatalf("payload = %#v, want 1 of 3 matches with more", payload)
	}
}

// A filter the server does not understand is refused. Dropping it silently
// would answer a narrow question with the whole journal, and the reply would be
// indistinguishable from a filter that matched everything.
func TestServer_HistoryAPI_RefusesUnusableQueries(t *testing.T) {
	baseURL, _, _ := newHistoryFixture(t)

	for _, query := range []url.Values{
		{"limit": {"0"}},
		{"limit": {"-1"}},
		{"limit": {"201"}},
		{"limit": {"all"}},
		{"offset": {"-1"}},
		{"offset": {"soon"}},
		{"origin": {"not-an-origin"}},
		{"origin": {"browser", "not-an-origin"}},
		{"class": {"notes"}},
	} {
		resp := getHistoryResponse(t, baseURL, query)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("query %q: got %d, want %d; body=%q", query.Encode(), resp.StatusCode, http.StatusBadRequest, body)
			continue
		}
		if got := decodeManagementError(t, body).Code; got != "invalid_request" {
			t.Errorf("query %q code = %q, want invalid_request", query.Encode(), got)
		}
	}

	// The documented ceiling is accepted, so a client that reads MaxPageSize off
	// the page and asks for exactly that is not refused by an off-by-one.
	if payload := getHistory(t, baseURL, url.Values{"limit": {"200"}}); payload.Limit != historyEntryMaxLimit {
		t.Fatalf("limit at the ceiling = %d, want %d", payload.Limit, historyEntryMaxLimit)
	}
}

// An unreadable entry is reported on every page and under every filter. It has
// no command and no file list, so there is no way to tell whether it would have
// matched — and a narrowed view that hid it would look complete while part of
// the journal was unaccounted for.
func TestServer_HistoryAPI_WarningsSurviveFilteringAndPaging(t *testing.T) {
	baseURL, root, ids := newHistoryFixture(t)

	manifest := filepath.Join(noteops.JournalRoot(root), ids["import"], journal.ManifestFileName)
	if err := os.WriteFile(manifest, []byte("{\"id\":\"trunc"), 0o600); err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}

	for _, query := range []url.Values{
		nil,
		{"origin": {"browser"}},
		{"path": {"no-such-path"}},
		{"limit": {"1"}, "offset": {"3"}},
	} {
		payload := getHistory(t, baseURL, query)
		if payload.WarningCount != 1 || len(payload.Warnings) != 1 {
			t.Errorf("query %q warnings = %d/%d, want 1/1", query.Encode(), len(payload.Warnings), payload.WarningCount)
			continue
		}
		if payload.Warnings[0].ID != ids["import"] {
			t.Errorf("query %q warning id = %q, want %q", query.Encode(), payload.Warnings[0].ID, ids["import"])
		}
	}

	// And the corrupt entry costs exactly itself: the other four still list.
	if payload := getHistory(t, baseURL, nil); payload.Total != 4 {
		t.Fatalf("total after corruption = %d, want 4", payload.Total)
	}
}
