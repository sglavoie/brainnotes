package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

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
