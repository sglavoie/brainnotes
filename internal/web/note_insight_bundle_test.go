package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

func TestNoteInsightBundleOnlyLoadsForConfiguredPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{{"ordinary reading", false}, {"configured agent", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
				"Note.md": []byte("A thought worth keeping.\n"),
			}, func(cfg *config.Config) {
				if tc.enabled {
					cfg.Agent.Command = []string{"unused-test-agent"}
				}
			}, Options{})
			resp, err := http.Get(ts.URL + "/n/note") // #nosec G107 -- isolated local fixture
			if err != nil {
				t.Fatal(err)
			}
			body := readResponseBody(t, resp)
			if got := strings.Contains(body, `/static/note-insight-page.js`); got != tc.enabled {
				t.Fatalf("insight bundle included=%v, want %v", got, tc.enabled)
			}
			asset, err := http.Get(ts.URL + "/static/note-insight-page.js") // #nosec G107 -- isolated local fixture
			if err != nil {
				t.Fatal(err)
			}
			bundle := readResponseBody(t, asset)
			if asset.StatusCode != http.StatusOK || !strings.Contains(bundle, "data-note-insight") {
				t.Fatal("configured reading must serve the note insight controls")
			}
		})
	}
}
