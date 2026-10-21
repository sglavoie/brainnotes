package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	noteInsightAPIRoutePath = "/api/note/insight"
	noteInsightPurpose      = "note-question"
)

type noteInsightResponse struct {
	OK           bool   `json:"ok"`
	Enabled      bool   `json:"enabled"`
	Message      string `json:"message,omitempty"`
	Key          string `json:"key,omitempty"`
	Output       string `json:"output,omitempty"`
	RenderedHTML string `json:"rendered_html,omitempty"`
}

func (s *Server) handleNoteInsight(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if !s.requireManagementPost(w, r, noteInsightAPIRoutePath, 0) {
		return
	}

	key := strings.TrimSpace(r.Form.Get("key"))
	if key == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "note key is required"))
		return
	}
	userPrompt, ok := readAgentPrompt(w, r)
	if !ok {
		return
	}

	argv := append([]string(nil), cfg.Agent.Command...)
	if len(argv) == 0 {
		writeManagementJSON(w, http.StatusOK, noteInsightResponse{
			OK:      true,
			Enabled: false,
			Message: "Ask AI is unavailable until [agent].command is configured.",
		})
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "snapshot error", err))
		return
	}
	note, normalizedKey, found := noteForRoute(snapshot, key)
	if !found {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "note not found or stale"))
		return
	}
	packet, err := s.buildNoteInsightPacket(snapshot, note, userPrompt)
	if err != nil {
		writeNoteInsightBuildError(w, err)
		return
	}

	output, ok := s.runAgentForRequest(w, r, argv, packet, cfg.Agent.TimeoutSeconds, agentRunLabels{
		timeout: "note insight command timed out",
		failure: "run note insight command",
	})
	if !ok {
		return
	}
	rendered, err := s.markdown.render(string(output))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "render note insight output", err))
		return
	}

	writeManagementJSON(w, http.StatusOK, noteInsightResponse{
		OK:           true,
		Enabled:      true,
		Key:          normalizedKey,
		Output:       string(output),
		RenderedHTML: string(rendered),
	})
}

func (s *Server) buildNoteInsightPacket(snapshot vaultSnapshot, sourceNote *vault.Note, userPrompt string) ([]byte, error) {
	cfg := s.config()
	documents, err := recall.ExtractCorpus(s.vault.Root, snapshot.notes, recall.ExtractOptions{
		MetadataProperties: append([]string(nil), cfg.Related.MetadataProperties...),
	})
	if err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, errors.New("web: note insight corpus is empty")
	}
	sourcePath := noteVaultPath(s.vault.Root, sourceNote)
	source, ok := documentByPath(documents, sourcePath)
	if !ok {
		return nil, fmt.Errorf("web: note insight source %q is unavailable", sourcePath)
	}

	ranker, err := recall.BuildRanker(documents, snapshot.graph, recall.RankOptions{Now: s.briefNow})
	if err != nil {
		return nil, err
	}
	results, err := ranker.Rank(source.Path, recall.ModeBalanced)
	if err != nil {
		return nil, err
	}
	instructions := "Analyze the source note, surface its important ideas and unanswered questions, and suggest concrete next steps."
	if userPrompt != "" {
		instructions += "\n\nUser request (prioritize this when it conflicts with the default guidance):\n" + userPrompt
	}
	options := recall.DefaultContextBundleOptions()
	options.Purpose = recall.ContextPurpose{
		Name:         noteInsightPurpose,
		Instructions: instructions,
	}
	bundle, err := recall.BuildContextBundle(source, documents, results, options)
	if err != nil {
		return nil, err
	}
	return recall.RenderContextMarkdown(bundle)
}

func writeNoteInsightBuildError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, recall.ErrSourceBodyTooLarge), errors.Is(err, recall.ErrRequiredContextTooLarge):
		writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "note insight context exceeds byte limit"))
	default:
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build note insight context", err))
	}
}
