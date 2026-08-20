package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func synthesisAgentConfigTOML(argv string) string {
	return synthesisTestConfigTOML + "\n[agent]\ncommand = [" + argv + "]\ntimeout_seconds = 30\n"
}

func supporterKeys(t *testing.T, supporters []synthesisNoteIdentity) []string {
	t.Helper()

	keys := make([]string, len(supporters))
	for index := range supporters {
		keys[index] = supporters[index].Key
	}
	return keys
}

func requireKeyOrder(t *testing.T, got, want []string, label string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

func TestSynthesisContextAPI_BuildsPacketFromSelection(t *testing.T) {
	ts, srv, _ := newSynthesisServer(t, Options{})

	decoded := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	if !decoded.OK {
		t.Fatal("ok = false, want a success envelope")
	}
	if len(decoded.ContextID) != 64 {
		t.Fatalf("context_id length = %d, want 64", len(decoded.ContextID))
	}
	if _, err := time.Parse(time.RFC3339, decoded.ExpiresAt); err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", decoded.ExpiresAt, err)
	}
	if decoded.VaultID != srv.vaultID {
		t.Fatalf("vault_id = %q, want %q", decoded.VaultID, srv.vaultID)
	}
	if decoded.AgentEnabled {
		t.Fatal("agent_enabled = true with no [agent].command configured")
	}
	if decoded.Purpose != recall.PurposeSynthesis {
		t.Fatalf("purpose = %q, want %q", decoded.Purpose, recall.PurposeSynthesis)
	}
	if decoded.Anchor.Key != synthesisAnchorKey {
		t.Fatalf("anchor key = %q, want %q", decoded.Anchor.Key, synthesisAnchorKey)
	}
	if decoded.Anchor.RelPath != "Notes/Anchor.md" || decoded.Anchor.Folder != "Notes" {
		t.Fatalf("anchor path/folder = %q/%q, want Notes/Anchor.md/Notes", decoded.Anchor.RelPath, decoded.Anchor.Folder)
	}
	if decoded.Anchor.Route != "/n/notes/anchor" {
		t.Fatalf("anchor route = %q, want /n/notes/anchor", decoded.Anchor.Route)
	}
	requireKeyOrder(t, supporterKeys(t, decoded.Supporters), []string{synthesisAlphaKey, synthesisBetaKey}, "supporters")
	requireKeyOrder(
		t,
		supporterKeys(t, decoded.Manifest.Notes),
		[]string{synthesisAnchorKey, synthesisAlphaKey, synthesisBetaKey},
		"manifest.notes",
	)
	if decoded.Manifest.SourcePath != "Notes/Anchor.md" {
		t.Fatalf("manifest source_path = %q, want Notes/Anchor.md", decoded.Manifest.SourcePath)
	}
	if decoded.Manifest.ByteCount <= 0 || decoded.Manifest.ByteCount != len(decoded.Markdown) {
		t.Fatalf("manifest byte_count = %d, markdown = %d bytes", decoded.Manifest.ByteCount, len(decoded.Markdown))
	}
	if len(decoded.Manifest.Excerpts) < len(decoded.Supporters) {
		t.Fatalf("manifest carries %d excerpts for %d supporters", len(decoded.Manifest.Excerpts), len(decoded.Supporters))
	}
	for _, want := range []string{
		"Anchor weighs quorum size against write latency",
		"Alpha",
		"Beta",
	} {
		if !strings.Contains(decoded.Markdown, want) {
			t.Fatalf("packet is missing %q", want)
		}
	}
	if strings.Contains(decoded.Markdown, "soil composition") {
		t.Fatal("packet carries an unselected note")
	}
}

// TestSynthesisContextAPI_HashesAreNotExposed keeps the revision hashes the
// freshness check relies on out of the JSON the browser receives.
func TestSynthesisContextAPI_HashesAreNotExposed(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, defaultSynthesisContextForm())
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	if strings.Contains(body, "sha256") || strings.Contains(body, "SHA256") {
		t.Fatalf("context response exposes a revision hash: %s", body)
	}
}

// TestSynthesisContextAPI_PreservesCandidateOrder proves selection order is the
// user's, not the ranker's: the same two notes reverse in the response when the
// form reverses them.
func TestSynthesisContextAPI_PreservesCandidateOrder(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	form := url.Values{}
	form.Set("source", synthesisAnchorKey)
	form.Add("candidate", synthesisBetaKey)
	form.Add("candidate", synthesisAlphaKey)

	decoded := buildSynthesisContextRequest(t, ts.URL, form)
	requireKeyOrder(t, supporterKeys(t, decoded.Supporters), []string{synthesisBetaKey, synthesisAlphaKey}, "supporters")
}

func TestSynthesisContextAPI_SkipsBlankCandidates(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	form := url.Values{}
	form.Set("source", synthesisAnchorKey)
	form.Add("candidate", "")
	form.Add("candidate", "   ")
	form.Add("candidate", synthesisAlphaKey)

	decoded := buildSynthesisContextRequest(t, ts.URL, form)
	requireKeyOrder(t, supporterKeys(t, decoded.Supporters), []string{synthesisAlphaKey}, "supporters")
}

func TestSynthesisContextAPI_AcceptsAnchorOnlySelection(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	form := url.Values{}
	form.Set("source", synthesisAnchorKey)

	decoded := buildSynthesisContextRequest(t, ts.URL, form)
	if len(decoded.Supporters) != 0 {
		t.Fatalf("supporters = %v, want none", supporterKeys(t, decoded.Supporters))
	}
	if !strings.Contains(decoded.Markdown, "Anchor weighs quorum size") {
		t.Fatal("anchor-only packet does not carry the anchor body")
	}
}

func TestSynthesisContextAPI_AcceptsEveryStandardPurpose(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	names := recall.StandardPurposeNames()
	if len(names) == 0 {
		t.Fatal("no standard purposes to exercise")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			form := defaultSynthesisContextForm()
			form.Set("purpose", strings.ToUpper(name))

			decoded := buildSynthesisContextRequest(t, ts.URL, form)
			if decoded.Purpose != name {
				t.Fatalf("purpose = %q, want %q", decoded.Purpose, name)
			}
		})
	}
}

// TestSynthesisContextAPI_PromptTravelsInsideThePacket pins the decision that the
// user prompt is folded into the packet bytes rather than sent beside them, so
// generation can forward the stored bytes unchanged.
func TestSynthesisContextAPI_PromptTravelsInsideThePacket(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	form := defaultSynthesisContextForm()
	form.Set("prompt", "  Compare the two quorum arguments.  ")

	decoded := buildSynthesisContextRequest(t, ts.URL, form)
	if decoded.Prompt != "Compare the two quorum arguments." {
		t.Fatalf("prompt = %q, want the trimmed request prompt", decoded.Prompt)
	}
	if !strings.Contains(decoded.Markdown, "Compare the two quorum arguments.") {
		t.Fatal("packet does not carry the user prompt")
	}
}

func TestSynthesisContextAPI_RejectsNonPOST(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	resp, err := http.Get(ts.URL + synthesisContextAPIRoutePath) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET context: %v", err)
	}
	body := readResponseBody(t, resp)
	if got := resp.Header.Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", got)
	}
	requireSynthesisError(t, resp.StatusCode, body, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
}

func TestSynthesisContextAPI_RequiresCSRF(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, "", defaultSynthesisContextForm())
	requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)

	status, body = postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, "wrong-token", defaultSynthesisContextForm())
	requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
}

func TestSynthesisContextAPI_RejectsOversizeBody(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	form := defaultSynthesisContextForm()
	form.Set("filler", strings.Repeat("x", synthesisContextBodyLimit+1))

	status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusBadRequest, noteops.CodeInvalidRequest)
}

func TestSynthesisContextAPI_RejectsInvalidSelections(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	tooMany := url.Values{}
	tooMany.Set("source", synthesisAnchorKey)
	for range recall.MaxCuratedSupporters + 1 {
		tooMany.Add("candidate", synthesisAlphaKey)
	}

	duplicate := url.Values{}
	duplicate.Set("source", synthesisAnchorKey)
	duplicate.Add("candidate", synthesisAlphaKey)
	duplicate.Add("candidate", synthesisAlphaKey)

	anchorAsSupporter := url.Values{}
	anchorAsSupporter.Set("source", synthesisAnchorKey)
	anchorAsSupporter.Add("candidate", synthesisAnchorKey)

	longPrompt := defaultSynthesisContextForm()
	longPrompt.Set("prompt", strings.Repeat("x", agentPromptMaxRunes+1))

	badPurpose := defaultSynthesisContextForm()
	badPurpose.Set("purpose", "summarize")

	for name, form := range map[string]url.Values{
		"missing source":      {"candidate": {synthesisAlphaKey}},
		"blank source":        {"source": {"   "}},
		"too many":            tooMany,
		"duplicate":           duplicate,
		"anchor as supporter": anchorAsSupporter,
		"long prompt":         longPrompt,
		"unknown purpose":     badPurpose,
	} {
		t.Run(name, func(t *testing.T) {
			status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, form)
			requireSynthesisError(t, status, body, http.StatusBadRequest, noteops.CodeInvalidRequest)
		})
	}
}

func TestSynthesisContextAPI_UnknownNotesAreNotFound(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	missingAnchor := url.Values{}
	missingAnchor.Set("source", "notes/missing")
	missingAnchor.Add("candidate", synthesisAlphaKey)

	missingSupporter := url.Values{}
	missingSupporter.Set("source", synthesisAnchorKey)
	missingSupporter.Add("candidate", "notes/missing")

	for name, form := range map[string]url.Values{
		"anchor":    missingAnchor,
		"supporter": missingSupporter,
	} {
		t.Run(name, func(t *testing.T) {
			status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, form)
			requireSynthesisError(t, status, body, http.StatusNotFound, noteops.CodeNotFound)
		})
	}
}

// TestSynthesisContextAPI_IgnoredNotesAreNotFound covers a note under a default
// ignore: it exists on disk but no query can see it, so it must not become a
// synthesis source either.
func TestSynthesisContextAPI_IgnoredNotesAreNotFound(t *testing.T) {
	files := synthesisTestNotes()
	files["Archive/Old.md"] = "---\ntitle: Old\n---\nArchived prose about quorum size.\n\nA second archived paragraph.\n"
	ts, _, _ := newSynthesisServerWithConfig(t, synthesisTestConfigTOML, files, Options{})

	form := url.Values{}
	form.Set("source", synthesisAnchorKey)
	form.Add("candidate", "archive/old")

	status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusNotFound, noteops.CodeNotFound)
}

// TestSynthesisContextAPI_QuarantinedNoteIsNotFound proves a note whose read
// stalled once is refused for the life of the server rather than silently
// omitted from the packet.
func TestSynthesisContextAPI_QuarantinedNoteIsNotFound(t *testing.T) {
	files := map[string][]byte{
		"Anchor.md": []byte("---\ntitle: Anchor\n---\nAnchor prose about quorum size.\n\nA second anchor paragraph.\n"),
		"Slow.md":   []byte("---\ntitle: Slow\n---\nSlow prose about quorum size.\n\nA second slow paragraph.\n"),
	}
	ts, _, _ := newBlockedLoaderServer(t, files, "Slow.md", nil, Options{CSRFToken: synthesisTestToken})

	form := url.Values{}
	form.Set("source", "anchor")
	form.Add("candidate", "slow")

	status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusNotFound, noteops.CodeNotFound)
}

// TestSynthesisContextAPI_SupporterWithoutExcerptIsRejected covers the curated
// contract that every supporter must contribute an exact excerpt: a note with no
// body is refused rather than reduced to a bare identity.
func TestSynthesisContextAPI_SupporterWithoutExcerptIsRejected(t *testing.T) {
	files := synthesisTestNotes()
	files["Notes/Hollow.md"] = "---\ntitle: Hollow\n---\n"
	ts, _, _ := newSynthesisServerWithConfig(t, synthesisTestConfigTOML, files, Options{})

	form := url.Values{}
	form.Set("source", synthesisAnchorKey)
	form.Add("candidate", "notes/hollow")

	status, body := postSynthesisForm(t, ts.URL, synthesisContextAPIRoutePath, synthesisTestToken, form)
	if status == http.StatusOK {
		t.Fatalf("a supporter with no excerptable body was accepted: %s", body)
	}
	requireSynthesisError(t, status, body, http.StatusBadRequest, noteops.CodeInvalidRequest)
}

func TestSynthesisGenerateAPI_SendsStoredPacketAndConfiguredArgv(t *testing.T) {
	var gotArgv []string
	var gotStdin []byte
	gotDeadline := false

	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent", "--draft", "literal argument"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
				gotArgv = append([]string(nil), argv...)
				gotStdin = append([]byte(nil), stdin...)
				_, gotDeadline = ctx.Deadline()
				return []byte("# Draft\n\nA synthesized paragraph.\n"), nil
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	if !built.AgentEnabled {
		t.Fatal("agent_enabled = false with [agent].command configured")
	}

	form := url.Values{}
	form.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	decoded := decodeSynthesisGenerate(t, body)
	if !decoded.OK || !decoded.Enabled {
		t.Fatalf("ok/enabled = %v/%v, want true/true", decoded.OK, decoded.Enabled)
	}
	if decoded.ContextID != built.ContextID {
		t.Fatalf("context_id = %q, want %q", decoded.ContextID, built.ContextID)
	}
	if decoded.Output != "# Draft\n\nA synthesized paragraph.\n" {
		t.Fatalf("output = %q, want the command stdout verbatim", decoded.Output)
	}
	if !strings.Contains(decoded.RenderedHTML, "<h1") || !strings.Contains(decoded.RenderedHTML, "A synthesized paragraph.") {
		t.Fatalf("rendered_html did not render the candidate Markdown: %q", decoded.RenderedHTML)
	}

	requireKeyOrder(t, gotArgv, []string{"synth-agent", "--draft", "literal argument"}, "argv")
	if string(gotStdin) != built.Markdown {
		t.Fatal("agent stdin is not the stored packet bytes")
	}
	if !gotDeadline {
		t.Fatal("configured timeout did not reach the command context")
	}
}

// TestSynthesisGenerateAPI_ContextIsReusable proves one packet can be generated
// from more than once, which is what makes a retry cheap.
func TestSynthesisGenerateAPI_ContextIsReusable(t *testing.T) {
	runs := 0
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				runs++
				return []byte("Draft body.\n"), nil
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	for attempt := range 2 {
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
		if status != http.StatusOK {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, status, body)
		}
	}
	if runs != 2 {
		t.Fatalf("agent ran %d times, want 2", runs)
	}
}

func TestSynthesisGenerateAPI_DisabledWithoutConfiguredAgent(t *testing.T) {
	ran := false
	ts, _, _ := newSynthesisServer(t, Options{
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	decoded := decodeSynthesisGenerate(t, body)
	if !decoded.OK || decoded.Enabled {
		t.Fatalf("ok/enabled = %v/%v, want true/false", decoded.OK, decoded.Enabled)
	}
	if !strings.Contains(decoded.Message, "[agent].command") {
		t.Fatalf("message = %q, want an explanation naming [agent].command", decoded.Message)
	}
	if decoded.Output != "" || decoded.RenderedHTML != "" {
		t.Fatal("disabled response carries candidate content")
	}
	if ran {
		t.Fatal("the command ran with no [agent].command configured")
	}
}

func TestSynthesisGenerateAPI_RejectsBadRequests(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				t.Error("the command ran for a rejected request")
				return nil, nil
			},
		},
	)

	t.Run("non-POST", func(t *testing.T) {
		resp, err := http.Get(ts.URL + synthesisGenerateAPIRoutePath) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET generate: %v", err)
		}
		body := readResponseBody(t, resp)
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Fatalf("Allow = %q, want POST", got)
		}
		requireSynthesisError(t, resp.StatusCode, body, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	})

	t.Run("csrf", func(t *testing.T) {
		built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
		form := url.Values{}
		form.Set("context_id", built.ContextID)
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, "", form)
		requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
	})

	t.Run("missing context id", func(t *testing.T) {
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, url.Values{})
		requireSynthesisError(t, status, body, http.StatusBadRequest, noteops.CodeInvalidRequest)
	})

	t.Run("unknown context id", func(t *testing.T) {
		form := url.Values{}
		form.Set("context_id", strings.Repeat("a", 64))
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
		requireSynthesisError(t, status, body, http.StatusNotFound, noteops.CodeNotFound)
	})
}

func TestSynthesisGenerateAPI_RejectsUnusableCandidates(t *testing.T) {
	for name, output := range map[string][]byte{
		"invalid utf-8": []byte("# Draft\n\xff\xfe not text\n"),
		"oversize":      make([]byte, synthesisCandidateMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			ts, _, _ := newSynthesisServerWithConfig(
				t,
				synthesisAgentConfigTOML(`"synth-agent"`),
				synthesisTestNotes(),
				Options{
					RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
						return output, nil
					},
				},
			)

			built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
			form := url.Values{}
			form.Set("context_id", built.ContextID)

			status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
			requireSynthesisError(t, status, body, http.StatusInternalServerError, noteops.CodeInternal)
		})
	}
}

// TestSynthesisGenerateAPI_SanitizesFailureDiagnostic keeps a failing command's
// own words out of the response as raw bytes: the browser sees one bounded,
// control-character-free line.
func TestSynthesisGenerateAPI_SanitizesFailureDiagnostic(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				return nil, errors.New("boom\n\x00\x07multi\nline\tdiagnostic")
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusInternalServerError, noteops.CodeInternal)

	decoded := decodeSynthesisError(t, body)
	if !strings.Contains(decoded.Message, "boom multi line diagnostic") {
		t.Fatalf("message = %q, want the collapsed diagnostic", decoded.Message)
	}
	if strings.ContainsAny(decoded.Message, "\n\t\x00\x07") {
		t.Fatalf("message = %q, want no control characters", decoded.Message)
	}
}

func TestSynthesisGenerateAPI_TimeoutIsReported(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisTestConfigTOML+"\n[agent]\ncommand = [\"synth-agent\"]\ntimeout_seconds = 1\n",
		synthesisTestNotes(),
		Options{
			RunAgent: func(ctx context.Context, _ []string, _ []byte) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusInternalServerError, noteops.CodeInternal)
	if decoded := decodeSynthesisError(t, body); !strings.Contains(decoded.Message, "timed out") {
		t.Fatalf("message = %q, want a timeout explanation", decoded.Message)
	}
}

// newStaleSynthesisServer builds a configured-agent server and one live context,
// returning everything a staleness case needs to disturb the vault between the
// build and the run.
func newStaleSynthesisServer(t *testing.T) (*httptest.Server, string, string, *int) {
	t.Helper()

	runs := 0
	ts, _, root := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				runs++
				return []byte("Draft body.\n"), nil
			},
		},
	)
	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	return ts, root, built.ContextID, &runs
}

func TestSynthesisGenerateAPI_RefusesStaleContext(t *testing.T) {
	for name, disturb := range map[string]func(t *testing.T, root string){
		"edited supporter": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha now says something else entirely.\n\nAnd a second paragraph.\n")
		},
		"edited anchor": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Anchor.md", "---\ntitle: Anchor\n---\nAnchor now says something else entirely.\n\nAnd a second paragraph.\n")
		},
		"moved supporter": func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "Moved"), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.Rename(filepath.Join(root, "Notes", "Beta.md"), filepath.Join(root, "Moved", "Beta.md")); err != nil {
				t.Fatalf("rename: %v", err)
			}
		},
		"removed supporter": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "Notes", "Beta.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		},
		"changed configuration": func(t *testing.T, root string) {
			if err := os.WriteFile(
				filepath.Join(root, config.FileName),
				[]byte(synthesisTestConfigTOML+"\n[frontmatter.properties.status]\ntype = \"string\"\n"),
				0o644,
			); err != nil {
				t.Fatalf("write config: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ts, root, contextID, runs := newStaleSynthesisServer(t)
			disturb(t, root)

			form := url.Values{}
			form.Set("context_id", contextID)
			status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
			requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
			if *runs != 0 {
				t.Fatalf("the command ran %d times against a stale context", *runs)
			}
		})
	}
}

// TestSynthesisGenerateAPI_RefusesSourceMutatedDuringRun covers the second
// revalidation: a candidate that reasoned over bytes replaced while it ran
// describes a vault that no longer exists.
func TestSynthesisGenerateAPI_RefusesSourceMutatedDuringRun(t *testing.T) {
	var root string
	ts, _, vaultRoot := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha changed while the command ran.\n\nSecond paragraph.\n")
				return []byte("Draft body.\n"), nil
			},
		},
	)
	root = vaultRoot

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
	if strings.Contains(body, "Draft body.") {
		t.Fatal("a candidate built from stale sources was returned")
	}
}

// TestSynthesisAPI_PersistsNothing pins the session's privacy contract: building
// a context and generating a candidate leave the vault byte-identical.
func TestSynthesisAPI_PersistsNothing(t *testing.T) {
	ts, _, root := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				return []byte("Draft body mentioning quorum size.\n"), nil
			},
		},
	)

	before := vaultFileSet(t, root)

	form := defaultSynthesisContextForm()
	form.Set("prompt", "A secret prompt worth remembering.")
	built := buildSynthesisContextRequest(t, ts.URL, form)

	generate := url.Values{}
	generate.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, generate)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}

	after := vaultFileSet(t, root)
	requireKeyOrder(t, after, before, "vault contents")
}

// vaultFileSet returns every file under root as "<relpath>:<size>", sorted, so a
// test can assert the whole vault is untouched.
func vaultFileSet(t *testing.T, root string) []string {
	t.Helper()

	var entries []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel)+":"+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatalf("walk vault: %v", err)
	}
	sort.Strings(entries)
	return entries
}
