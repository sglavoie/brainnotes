package web

import (
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

func TestCapturePageLinksOnlyExistingInbox(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "existing"}[exists], func(t *testing.T) {
			files := map[string]string{"Other.md": "Other note\n"}
			if exists {
				files["Ideas/Inbox été.md"] = "## Captures\n"
			}
			ts, srv, _ := newCaptureTestServer(t, files, func(cfg *config.Config) {
				cfg.Capture.InboxNote = "Ideas/Inbox été.md"
			})
			resp, err := ts.Client().Get(ts.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			body := readResponseBody(t, resp)
			if !strings.Contains(body, `data-capture-vault="`+srv.vaultID+`"`) {
				t.Fatal("capture is missing its vault identity")
			}
			link := `<a href="/n/ideas/inbox%20%C3%A9t%C3%A9"><code>Ideas/Inbox été.md</code></a>`
			if strings.Contains(body, link) != exists {
				t.Fatalf("inbox link present = %v, want %v", strings.Contains(body, link), exists)
			}
		})
	}
}
