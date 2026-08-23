package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func TestSynthesisPreviewAPI_ReturnsExactPlannedNote(t *testing.T) {
	ts, _, root := newSynthesisServer(t, Options{})

	before := vaultFileSet(t, root)
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	preview := buildSynthesisPreview(t, ts.URL, defaultSynthesisPreviewForm(built.ContextID))

	if !preview.OK {
		t.Fatal("ok = false, want a success envelope")
	}
	if len(preview.PreviewID) != 64 {
		t.Fatalf("preview_id length = %d, want 64", len(preview.PreviewID))
	}
	if _, err := time.Parse(time.RFC3339, preview.ExpiresAt); err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", preview.ExpiresAt, err)
	}
	if preview.ContextID != built.ContextID {
		t.Fatalf("context_id = %q, want %q", preview.ContextID, built.ContextID)
	}
	if preview.Destination != synthesisDraftRel {
		t.Fatalf("destination = %q, want %q", preview.Destination, synthesisDraftRel)
	}
	if preview.Markdown != synthesisPlannedNote {
		t.Fatalf("markdown =\n%q\nwant\n%q", preview.Markdown, synthesisPlannedNote)
	}
	if !strings.Contains(preview.RenderedHTML, "A synthesized paragraph about quorum size.") {
		t.Fatalf("rendered_html did not render the draft: %q", preview.RenderedHTML)
	}
	if !strings.Contains(preview.RenderedHTML, "<h2") {
		t.Fatalf("rendered_html did not render the Sources heading: %q", preview.RenderedHTML)
	}
	if strings.Contains(preview.RenderedHTML, "title: "+synthesisDraftTitle) || strings.Contains(preview.RenderedHTML, "<hr") {
		t.Fatalf("rendered_html rendered the frontmatter block: %q", preview.RenderedHTML)
	}

	requireKeyOrder(t, vaultFileSet(t, root), before, "vault contents")
	requireSynthesisRefusal(t, root)
}

// TestSynthesisPreviewAPI_AcceptsAnAgentGeneratedDraft proves the workbench's
// two draft origins converge: a candidate the command produced becomes the same
// planned note as one a user typed.
func TestSynthesisPreviewAPI_AcceptsAnAgentGeneratedDraft(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				return []byte(synthesisDraftBody + "\n"), nil
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	generate := url.Values{}
	generate.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, generate)
	if status != http.StatusOK {
		t.Fatalf("generate status = %d, body = %s", status, body)
	}
	generated := decodeSynthesisGenerate(t, body)

	form := defaultSynthesisPreviewForm(built.ContextID)
	form.Set("draft", generated.Output)

	preview := buildSynthesisPreview(t, ts.URL, form)
	if preview.Markdown != synthesisPlannedNote {
		t.Fatalf("markdown =\n%q\nwant\n%q", preview.Markdown, synthesisPlannedNote)
	}
}

// TestSynthesisPreviewAPI_UsesTheAnchorOnlySelection covers the smallest packet:
// one source, one link.
func TestSynthesisPreviewAPI_UsesTheAnchorOnlySelection(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	anchorOnly := url.Values{}
	anchorOnly.Set("source", synthesisAnchorKey)
	built := buildSynthesisContextRequest(t, ts.URL, anchorOnly)

	form := defaultSynthesisPreviewForm(built.ContextID)
	form.Set("folder", "")

	preview := buildSynthesisPreview(t, ts.URL, form)
	if preview.Destination != synthesisDraftTitle+".md" {
		t.Fatalf("destination = %q, want a vault-root note", preview.Destination)
	}
	if !strings.HasSuffix(preview.Markdown, "## Sources\n- [[Notes/Anchor|Anchor]]\n") {
		t.Fatalf("markdown does not end with the single source link:\n%q", preview.Markdown)
	}
}

func TestSynthesisPreviewAPI_RejectsBadRequests(t *testing.T) {
	ts, _, root := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())

	blankTitle := defaultSynthesisPreviewForm(built.ContextID)
	blankTitle.Set("title", "   ")

	emptyDraft := defaultSynthesisPreviewForm(built.ContextID)
	emptyDraft.Set("draft", "\n\n   \n")

	reservedHeading := defaultSynthesisPreviewForm(built.ContextID)
	reservedHeading.Set("draft", "Body text.\n\n## Sources\n- [[Notes/Alpha]]\n")

	oversizeDraft := defaultSynthesisPreviewForm(built.ContextID)
	oversizeDraft.Set("draft", strings.Repeat("x", noteops.MaxSynthesisDraftBytes+1))

	escapingFolder := defaultSynthesisPreviewForm(built.ContextID)
	escapingFolder.Set("folder", "../outside")

	for name, form := range map[string]url.Values{
		"missing context id": {"title": {synthesisDraftTitle}, "draft": {synthesisDraftBody}},
		"blank title":        blankTitle,
		"empty draft":        emptyDraft,
		"reserved heading":   reservedHeading,
		"oversize draft":     oversizeDraft,
		"escaping folder":    escapingFolder,
	} {
		t.Run(name, func(t *testing.T) {
			status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, synthesisTestToken, form)
			if status == http.StatusOK {
				t.Fatalf("preview accepted %s: %s", name, body)
			}
			decoded := decodeSynthesisError(t, body)
			if decoded.OK {
				t.Fatalf("ok = true for %s: %s", name, body)
			}
			requireSynthesisRefusal(t, root)
		})
	}
}

// TestSynthesisPreviewAPI_RefusesAnExcludedDestination keeps the workbench from
// creating a note the vault walk would never visit.
func TestSynthesisPreviewAPI_RefusesAnExcludedDestination(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())

	form := defaultSynthesisPreviewForm(built.ContextID)
	form.Set("folder", "Archive")

	status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
}

func TestSynthesisPreviewAPI_RejectsBadTransport(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())

	t.Run("non-POST", func(t *testing.T) {
		resp, err := http.Get(ts.URL + synthesisPreviewAPIRoutePath) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET preview: %v", err)
		}
		body := readResponseBody(t, resp)
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Fatalf("Allow = %q, want POST", got)
		}
		requireSynthesisError(t, resp.StatusCode, body, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	})

	t.Run("csrf", func(t *testing.T) {
		status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, "wrong-token", defaultSynthesisPreviewForm(built.ContextID))
		requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
	})
}

// TestSynthesisPreviewAPI_RefusesStaleContext covers a vault that moved between
// the context build and the preview: the draft was written against bytes that no
// longer exist, so no receipt is minted.
func TestSynthesisPreviewAPI_RefusesStaleContext(t *testing.T) {
	for name, disturb := range map[string]func(t *testing.T, root string){
		"edited anchor": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Anchor.md", "---\ntitle: Anchor\n---\nAnchor says something else now.\n\nA second paragraph.\n")
		},
		"edited supporter": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha says something else now.\n\nA second paragraph.\n")
		},
		"removed supporter": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "Notes", "Beta.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		},
		"changed configuration": func(t *testing.T, root string) {
			writeSynthesisConfig(t, root, synthesisTestConfigTOML+"\n[frontmatter.properties.status]\ntype = \"string\"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			ts, srv, root := newSynthesisServer(t, Options{})
			built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
			disturb(t, root)

			status, body := postSynthesisForm(t, ts.URL, synthesisPreviewAPIRoutePath, synthesisTestToken, defaultSynthesisPreviewForm(built.ContextID))
			requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
			if size := srv.synthesisPreviews.size(); size != 0 {
				t.Fatalf("preview store holds %d receipts after a refusal, want 0", size)
			}
			requireSynthesisRefusal(t, root)
		})
	}
}
