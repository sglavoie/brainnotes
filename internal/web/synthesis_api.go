package web

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

const (
	synthesisContextAPIRoutePath  = "/api/synthesis/context"
	synthesisGenerateAPIRoutePath = "/api/synthesis/generate"

	// synthesisContextBodyLimit bounds the encoded context request. An anchor
	// key, ten candidate keys, a purpose name, and a prompt at the shared agent
	// prompt limit fit well inside it, so a larger body is malformed rather than
	// an ambitious selection.
	synthesisContextBodyLimit = 32 << 10
	// synthesisGenerateBodyLimit bounds the generate request, which carries only
	// a context identifier and a CSRF token.
	synthesisGenerateBodyLimit = 4 << 10
	// synthesisCandidateMaxBytes bounds the agent's candidate output. It matches
	// the draft limit the created-note contract enforces, so a candidate that
	// could never become a draft is refused where it is produced.
	synthesisCandidateMaxBytes = 1 << 20
)

// synthesisManifestResponse reports what the packet contains. Notes carries the
// ordered identities the shared context contract asks the manifest to record;
// they are derived from the stored record rather than from
// recall.ContextManifest, so the bytes `bn context --format json` emits stay
// unchanged.
type synthesisManifestResponse struct {
	SourcePath string                          `json:"source_path"`
	MaxBytes   int                             `json:"max_bytes"`
	ByteCount  int                             `json:"byte_count"`
	Notes      []synthesisNoteIdentity         `json:"notes"`
	Excerpts   []recall.ContextManifestExcerpt `json:"excerpts"`
}

type synthesisContextResponse struct {
	OK           bool                      `json:"ok"`
	ContextID    string                    `json:"context_id"`
	ExpiresAt    string                    `json:"expires_at"`
	VaultID      string                    `json:"vault_id"`
	AgentEnabled bool                      `json:"agent_enabled"`
	Purpose      string                    `json:"purpose"`
	Prompt       string                    `json:"prompt,omitempty"`
	Anchor       synthesisNoteIdentity     `json:"anchor"`
	Supporters   []synthesisNoteIdentity   `json:"supporters"`
	Manifest     synthesisManifestResponse `json:"manifest"`
	Markdown     string                    `json:"markdown"`
}

type synthesisGenerateResponse struct {
	OK           bool   `json:"ok"`
	Enabled      bool   `json:"enabled"`
	Message      string `json:"message,omitempty"`
	ContextID    string `json:"context_id,omitempty"`
	Output       string `json:"output,omitempty"`
	RenderedHTML string `json:"rendered_html,omitempty"`
}

// synthesisContextRequest is the validated form of one context build request.
type synthesisContextRequest struct {
	sourceKey     string
	candidateKeys []string
	purposeName   string
	prompt        string
}

func (s *Server) handleSynthesisContext(w http.ResponseWriter, r *http.Request) {
	if !s.requireSynthesisPost(w, r, synthesisContextAPIRoutePath, synthesisContextBodyLimit) {
		return
	}

	request, err := parseSynthesisContextForm(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	record, bundle, err := s.buildSynthesisContext(request)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	stored, err := s.synthesisContexts.store(record)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, synthesisContextResponse{
		OK:           true,
		ContextID:    stored.ID,
		ExpiresAt:    stored.ExpiresAt.Format(time.RFC3339),
		VaultID:      s.vaultID,
		AgentEnabled: len(s.config().Agent.Command) > 0,
		Purpose:      stored.PurposeName,
		Prompt:       stored.Prompt,
		Anchor:       stored.Anchor,
		Supporters:   stored.Supporters,
		Manifest: synthesisManifestResponse{
			SourcePath: bundle.Manifest.SourcePath,
			MaxBytes:   bundle.Manifest.MaxBytes,
			ByteCount:  bundle.Manifest.ByteCount,
			Notes:      stored.notes(),
			Excerpts:   bundle.Manifest.Excerpts,
		},
		Markdown: string(stored.Packet),
	})
}

func (s *Server) handleSynthesisGenerate(w http.ResponseWriter, r *http.Request) {
	if !s.requireSynthesisPost(w, r, synthesisGenerateAPIRoutePath, synthesisGenerateBodyLimit) {
		return
	}

	record, err := s.synthesisContexts.lookup(r.Form.Get("context_id"))
	if err != nil {
		writeManagementError(w, err)
		return
	}

	cfg := s.config()
	argv := append([]string(nil), cfg.Agent.Command...)
	if len(argv) == 0 {
		writeManagementJSON(w, http.StatusOK, synthesisGenerateResponse{
			OK:        true,
			Enabled:   false,
			ContextID: record.ID,
			Message:   "Generation is unavailable until [agent].command is configured.",
		})
		return
	}

	if err := s.revalidateSynthesisContext(record); err != nil {
		writeManagementError(w, err)
		return
	}

	output, ok := s.runAgentForRequest(w, r, argv, record.Packet, cfg.Agent.TimeoutSeconds, agentRunLabels{
		timeout: "synthesis: generation command timed out",
		failure: "run synthesis generation command",
	})
	if !ok {
		return
	}
	if len(output) > synthesisCandidateMaxBytes {
		writeManagementError(w, noteops.Errorf(noteops.CodeInternal,
			"synthesis: generated candidate is larger than the %d byte draft limit", synthesisCandidateMaxBytes))
		return
	}
	if !utf8.Valid(output) {
		writeManagementError(w, noteops.Errorf(noteops.CodeInternal, "synthesis: generated candidate is not valid UTF-8"))
		return
	}

	// Revalidate after the run as well. The command may take as long as the
	// configured timeout allows, and a candidate reasoning over sources that
	// changed while it ran describes a vault that no longer exists.
	if err := s.revalidateSynthesisContext(record); err != nil {
		writeManagementError(w, err)
		return
	}

	rendered, err := s.markdown.render(string(output))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "render synthesis candidate", err))
		return
	}

	writeManagementJSON(w, http.StatusOK, synthesisGenerateResponse{
		OK:           true,
		Enabled:      true,
		ContextID:    record.ID,
		Output:       string(output),
		RenderedHTML: string(rendered),
	})
}

func parseSynthesisContextForm(r *http.Request) (synthesisContextRequest, error) {
	request := synthesisContextRequest{
		sourceKey:   strings.TrimSpace(r.Form.Get("source")),
		purposeName: strings.ToLower(strings.TrimSpace(r.Form.Get("purpose"))),
		prompt:      strings.TrimSpace(r.Form.Get("prompt")),
	}
	if request.sourceKey == "" {
		return synthesisContextRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "synthesis: an anchor note key is required")
	}
	if request.purposeName == "" {
		request.purposeName = recall.PurposeSynthesis
	}
	if _, ok := recall.StandardPurpose(request.purposeName); !ok {
		return synthesisContextRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"synthesis: purpose must be one of %s", strings.Join(recall.StandardPurposeNames(), ", "))
	}
	if !utf8.ValidString(request.prompt) {
		return synthesisContextRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "synthesis: prompt must be valid UTF-8")
	}
	if utf8.RuneCountInString(request.prompt) > agentPromptMaxRunes {
		return synthesisContextRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"synthesis: prompt must be %d characters or fewer", agentPromptMaxRunes)
	}

	for _, raw := range r.Form["candidate"] {
		candidate := strings.TrimSpace(raw)
		if candidate == "" {
			continue
		}
		request.candidateKeys = append(request.candidateKeys, candidate)
	}
	if len(request.candidateKeys) > recall.MaxCuratedSupporters {
		return synthesisContextRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"synthesis: at most %d supporting notes may be selected", recall.MaxCuratedSupporters)
	}
	return request, nil
}

// buildSynthesisContext assembles one packet from the vault as it is right now.
// It returns the record to store plus the bundle it was rendered from, because
// the response reports manifest numbers the record itself does not keep.
func (s *Server) buildSynthesisContext(request synthesisContextRequest) (synthesisContextRecord, recall.ContextBundle, error) {
	if err := s.reloadConfig(); err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, noteops.WrapError(noteops.CodeInternal, "synthesis: reload configuration", err)
	}
	configDigest, configRequires, err := s.synthesisConfigState()
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, err
	}
	cfg, status := s.configState.publication()
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, noteops.WrapError(noteops.CodeInternal, "synthesis: load snapshot", err)
	}
	corpus, err := s.state.relatedCorpusFor(snapshot, cfg.Related.MetadataProperties, status.Generation)
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, noteops.WrapError(noteops.CodeInternal, "synthesis: build corpus", err)
	}

	anchor, anchorDocument, err := s.synthesisIdentity(snapshot, corpus, request.sourceKey)
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, err
	}
	supporters, err := s.resolveSynthesisSupporters(snapshot, corpus, anchor, request.candidateKeys)
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, err
	}

	results, err := s.rankSynthesisSupporters(corpus, anchor, supporters)
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, err
	}

	options := recall.DefaultCuratedBundleOptions()
	options.Purpose = synthesisPurpose(request.purposeName, request.prompt)
	bundle, err := recall.BuildCuratedContextBundle(anchorDocument, corpus.documents, results, options)
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, synthesisBundleError(err)
	}
	packet, err := recall.RenderContextMarkdown(bundle)
	if err != nil {
		return synthesisContextRecord{}, recall.ContextBundle{}, noteops.WrapError(noteops.CodeInternal, "synthesis: render context", err)
	}

	return synthesisContextRecord{
		PurposeName:    request.purposeName,
		Purpose:        options.Purpose,
		Prompt:         request.prompt,
		Anchor:         anchor,
		Supporters:     supporters,
		Packet:         packet,
		Manifest:       bundle.Manifest,
		ConfigDigest:   configDigest,
		ConfigRequires: configRequires,
		Generation:     status.Generation,
	}, bundle, nil
}

// resolveSynthesisSupporters turns the requested keys into identities in the
// order they were asked for.
//
// The two refusals are about the selection rather than about any one note: a
// packet whose anchor is also one of its supporters would quote the same note
// twice in two roles, and a note named twice would weight itself. Both are
// invalid requests and neither is recoverable by dropping the duplicate,
// because a caller that sent one did not mean what the trimmed selection says.
func (s *Server) resolveSynthesisSupporters(
	snapshot vaultSnapshot,
	corpus *relatedCorpus,
	anchor synthesisNoteIdentity,
	keys []string,
) ([]synthesisNoteIdentity, error) {
	supporters := make([]synthesisNoteIdentity, 0, len(keys))
	selected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		identity, _, err := s.synthesisIdentity(snapshot, corpus, key)
		if err != nil {
			return nil, err
		}
		if identity.RelPath == anchor.RelPath {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest,
				"synthesis: the anchor note cannot also be a supporting note")
		}
		if _, exists := selected[identity.RelPath]; exists {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest,
				"synthesis: %s is selected more than once", identity.RelPath)
		}
		selected[identity.RelPath] = struct{}{}
		supporters = append(supporters, identity)
	}
	return supporters, nil
}

// rankSynthesisSupporters scores the chosen notes without letting the score
// change the selection. Recall history is bound so useful and opened signals
// still explain why a note belongs, while RankSelected keeps caller order and
// ignores dismissal, snoozing, and mode eligibility.
func (s *Server) rankSynthesisSupporters(
	corpus *relatedCorpus,
	anchor synthesisNoteIdentity,
	supporters []synthesisNoteIdentity,
) ([]recall.Result, error) {
	requestNow := s.briefNow()
	stateStore := recall.NewStateStore(
		s.vault.Root,
		recall.WithClock(func() time.Time { return requestNow }),
		recall.WithNotePaths(corpus.notePaths),
	)
	if err := stateStore.Validate(); err != nil {
		return nil, noteops.WrapError(noteops.CodeConflict, "recall state is unreadable", err)
	}
	ranker := corpus.ranker.WithOptions(recall.RankOptions{
		Now:    func() time.Time { return requestNow },
		Recall: stateStore,
	})
	paths := make([]string, len(supporters))
	for index := range supporters {
		paths[index] = supporters[index].RelPath
	}
	results, err := ranker.RankSelected(anchor.RelPath, paths)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeConflict, "synthesis: rank selected notes", err)
	}
	return results, nil
}

// synthesisIdentity resolves one route key against the current snapshot and
// returns both the routable identity and the extracted document the packet needs.
// The title comes from the document rather than the note so it matches the text
// the packet itself carries.
func (s *Server) synthesisIdentity(
	snapshot vaultSnapshot,
	corpus *relatedCorpus,
	key string,
) (synthesisNoteIdentity, recall.Document, error) {
	note, normalizedKey, ok := noteForRoute(snapshot, key)
	if !ok {
		return synthesisNoteIdentity{}, recall.Document{}, noteops.Errorf(noteops.CodeNotFound, "synthesis: note %q was not found", key)
	}
	relPath := noteVaultPath(s.vault.Root, note)
	document, ok := documentByPath(corpus.documents, relPath)
	if !ok {
		return synthesisNoteIdentity{}, recall.Document{}, noteops.Errorf(noteops.CodeNotFound, "synthesis: note %q is not available for synthesis", key)
	}
	route, err := notePathForKey(normalizedKey)
	if err != nil {
		return synthesisNoteIdentity{}, recall.Document{}, noteops.WrapError(noteops.CodeInternal, "synthesis: route note", err)
	}
	hash := note.SourceHash()
	if hash == "" {
		return synthesisNoteIdentity{}, recall.Document{}, noteops.Errorf(noteops.CodeConflict, "synthesis: note %q has no readable revision", key)
	}
	title := document.Title
	if title == "" {
		title = normalizedKey
	}
	return synthesisNoteIdentity{
		Key:     normalizedKey,
		Title:   title,
		RelPath: relPath,
		Route:   route,
		Folder:  folderFromSlashPath(relPath),
		SHA256:  hash,
	}, document, nil
}

// synthesisPurpose folds the user prompt into the standard instructions. The
// prompt travels inside the packet rather than beside it, so the stored bytes
// stay self-contained and every later step can send them unchanged.
func synthesisPurpose(name, prompt string) recall.ContextPurpose {
	purpose, _ := recall.StandardPurpose(name)
	if prompt != "" {
		purpose.Instructions += "\n\nUser request (prioritize this when it conflicts with the default guidance):\n" + prompt
	}
	return purpose
}
