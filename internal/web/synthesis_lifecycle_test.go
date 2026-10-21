package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

// The lifecycle tests walk the four synthesis routes in the order a browser
// does, which the per-route tests never do: each of those starts from a helper
// that has already built whatever it needs. What is proven here is that the
// steps compose — the context a page load leads to is the one preview plans
// against, and the receipt preview returns is the one apply redeems — rather
// than any single route's edge cases.

const synthesisLifecycleSourcesBlock = "## Sources\n" +
	"- [[Notes/Anchor|Anchor]]\n" +
	"- [[Notes/Alpha|Alpha]]\n" +
	"- [[Notes/Beta|Beta]]\n"

// runSynthesisLifecycle posts the context, preview, and apply steps for one
// draft and returns the receipt beside the created note, so both walks assert
// the same endpoint without repeating the sequence.
func runSynthesisLifecycle(
	t *testing.T,
	baseURL, title, draft string,
) (synthesisPreviewResponse, noteops.Result) {
	t.Helper()

	built := buildSynthesisContextRequest(t, baseURL, defaultSynthesisContextForm())

	form := url.Values{}
	form.Set("context_id", built.ContextID)
	form.Set("title", title)
	form.Set("folder", synthesisDraftFolder)
	form.Set("draft", draft)
	preview := buildSynthesisPreview(t, baseURL, form)

	status, body := applySynthesisPreview(t, baseURL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", status, body)
	}
	result := decodeSynthesisResult(t, body)
	if !result.OK {
		t.Fatalf("apply ok = false: %s", body)
	}
	return preview, result
}

// requireSynthesisLifecycleNote asserts what every completed walk owes the
// user: the file holds exactly the bytes the preview showed, the returned route
// already serves it, and one journal entry can take it back.
func requireSynthesisLifecycleNote(
	t *testing.T,
	baseURL, root, relPath string,
	preview synthesisPreviewResponse,
	result noteops.Result,
) {
	t.Helper()

	if result.RelPath != relPath {
		t.Fatalf("rel_path = %q, want %q", result.RelPath, relPath)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath))) // #nosec G304 -- test-owned temp vault
	if err != nil {
		t.Fatalf("read created note: %v", err)
	}
	if string(onDisk) != preview.Markdown {
		t.Fatalf("created note =\n%q\nwant the previewed bytes\n%q", string(onDisk), preview.Markdown)
	}
	if !strings.Contains(string(onDisk), synthesisLifecycleSourcesBlock) {
		t.Fatalf("created note does not carry the ordered Sources block:\n%s", string(onDisk))
	}

	page, err := http.Get(baseURL + result.Path) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET created note: %v", err)
	}
	if page.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", result.Path, page.StatusCode)
	}
	readResponseBody(t, page)

	entries := synthesisJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("journal holds %d entries, want 1", len(entries))
	}
	if entries[0].ID != result.JournalID {
		t.Fatalf("journal id = %q, want %q", entries[0].ID, result.JournalID)
	}

	undo := url.Values{}
	undo.Set("id", result.JournalID)
	status, body := postSynthesisForm(t, baseURL, "/api/undo", synthesisTestToken, undo)
	if status != http.StatusOK {
		t.Fatalf("undo status = %d, body = %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath))); !os.IsNotExist(err) {
		t.Fatalf("stat destination after undo: err = %v, want not-exist", err)
	}
}

// TestSynthesisLifecycle_ManualWorkflow covers the vault with no agent
// configured: the whole workbench still has to reach a created note, and the
// generation step has to say so rather than fail.
func TestSynthesisLifecycle_ManualWorkflow(t *testing.T) {
	ts, _, root := newSynthesisServer(t, Options{})

	status, page := getSynthesizePage(t, ts.URL, "?source="+synthesisAnchorKey)
	if status != http.StatusOK {
		t.Fatalf("page status = %d, want 200", status)
	}
	if !strings.Contains(page, `data-synthesize-agent="disabled"`) {
		t.Fatal("page did not advertise the missing agent")
	}

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	generateForm := url.Values{}
	generateForm.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, generateForm)
	if status != http.StatusOK {
		t.Fatalf("generate status = %d, body = %s", status, body)
	}
	generated := decodeSynthesisGenerate(t, body)
	if generated.Enabled || generated.Output != "" {
		t.Fatalf("unconfigured agent returned enabled = %v, output = %q", generated.Enabled, generated.Output)
	}
	if !strings.Contains(generated.Message, "[agent].command") {
		t.Fatalf("message = %q, want the configuration key", generated.Message)
	}

	const title = "Manual Synthesis"
	const relPath = "Notes/Manual Synthesis.md"
	const draft = "Every one of these notes trades quorum size for latency.\n\nWhat is missing is a partition-time answer."

	preview, result := runSynthesisLifecycle(t, ts.URL, title, draft)
	if preview.Destination != relPath {
		t.Fatalf("destination = %q, want %q", preview.Destination, relPath)
	}
	if !strings.Contains(preview.Markdown, draft) {
		t.Fatal("preview Markdown does not carry the typed draft")
	}
	requireSynthesisLifecycleNote(t, ts.URL, root, relPath, preview, result)
}

// TestSynthesisLifecycle_AgentAssistedWorkflow covers the configured agent. The
// draft it previews is deliberately not the command's output: the workbench
// offers a candidate the user edits, so the created note holds the edited text
// and the command is never consulted again once the draft is in hand.
func TestSynthesisLifecycle_AgentAssistedWorkflow(t *testing.T) {
	const candidate = "# Candidate\n\nThe agent's paragraph about quorum size.\n"

	runs := 0
	var stdins []string
	ts, _, root := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(_ context.Context, _ []string, stdin []byte) ([]byte, error) {
				runs++
				stdins = append(stdins, string(stdin))
				return []byte(candidate), nil
			},
		},
	)

	status, page := getSynthesizePage(t, ts.URL, "?source="+synthesisAnchorKey)
	if status != http.StatusOK {
		t.Fatalf("page status = %d, want 200", status)
	}
	if !strings.Contains(page, `data-synthesize-agent="enabled"`) {
		t.Fatal("page did not advertise the configured agent")
	}

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	generateForm := url.Values{}
	generateForm.Set("context_id", built.ContextID)
	for attempt := 0; attempt < 2; attempt++ {
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, generateForm)
		if status != http.StatusOK {
			t.Fatalf("generate %d status = %d, body = %s", attempt, status, body)
		}
		generated := decodeSynthesisGenerate(t, body)
		if !generated.Enabled || generated.Output != candidate {
			t.Fatalf("generate %d returned enabled = %v, output = %q", attempt, generated.Enabled, generated.Output)
		}
	}
	if runs != 2 {
		t.Fatalf("agent ran %d times, want 2", runs)
	}
	for index, stdin := range stdins {
		if stdin != built.Markdown {
			t.Fatalf("agent run %d received bytes other than the stored packet", index)
		}
	}

	const title = "Agent Synthesis"
	const relPath = "Notes/Agent Synthesis.md"
	const draft = "# Candidate\n\nThe agent's paragraph about quorum size, rewritten in my words.\n\nMy own closing question."

	preview, result := runSynthesisLifecycle(t, ts.URL, title, draft)
	if !strings.Contains(preview.Markdown, "rewritten in my words") {
		t.Fatal("preview Markdown does not carry the edited candidate")
	}
	requireSynthesisLifecycleNote(t, ts.URL, root, relPath, preview, result)

	if runs != 2 {
		t.Fatalf("agent ran %d times across the whole walk, want 2", runs)
	}
}
