package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// A triage action that edits frontmatter is a property write, so an invalid
// configuration refuses it the way the frontmatter routes and pins do, instead
// of applying a mutation against definitions nobody can currently read.
func TestServer_CockpitAPI_NoteWritesRefuseAnInvalidConfiguration(t *testing.T) {
	const note = "---\ntitle: Terminal\nstatus: complete\n---\nIntentionally terminal.\n"
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		config.FileName: []byte(""),
		"Terminal.md":   []byte(note),
	}, nil, Options{CSRFToken: "csrf-token"})

	task := findCockpitTask(t, getCockpitPayload(t, ts.URL).Queue, cockpit.RuleDeadEndNote, "Terminal.md")
	writeTestFile(t, root, config.FileName, "[[[ not toml\n")

	resp := postCockpitItem(t, ts.URL, task.ID, string(cockpit.ActionExemptDeadEnd), "", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%q", resp.StatusCode, body)
	}
	if !strings.Contains(body, "configuration is invalid") {
		t.Fatalf("refusal does not name the invalid configuration: %q", body)
	}
	if got := readNoteFile(t, filepath.Join(root, "Terminal.md")); got != note {
		t.Fatalf("refused exemption changed the note:\n%s", got)
	}
}

// The queue decided which notes a triage write changes, so a note edited after
// the snapshot it was generated from is refused as a conflict rather than
// rewritten on a decision made about other bytes.
func TestVerifyCockpitAffected_RefusesANoteChangedSinceTheSnapshot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Terminal.md", "---\ntitle: Terminal\n---\nBody\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	snapshot, err := srv.state.refreshSnapshot()
	if err != nil {
		t.Fatalf("refreshSnapshot: %v", err)
	}
	if err := verifyCockpitAffected(root, snapshot, []string{"Terminal.md"}); err != nil {
		t.Fatalf("unchanged note refused: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "Terminal.md"), []byte("---\ntitle: Edited\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write external edit: %v", err)
	}
	err = verifyCockpitAffected(root, snapshot, []string{"Terminal.md"})
	if got := noteops.CodeOf(err); got != noteops.CodeConflict {
		t.Fatalf("code = %q (err %v), want %q", got, err, noteops.CodeConflict)
	}
	if err := verifyCockpitAffected(root, snapshot, []string{"Other.md"}); err != nil {
		t.Fatalf("a note outside the task was judged: %v", err)
	}
}
