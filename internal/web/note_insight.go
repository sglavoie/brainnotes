package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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
	if r.URL.Path != noteInsightAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return
	}

	key := strings.TrimSpace(r.Form.Get("key"))
	if key == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "note key is required"))
		return
	}
	userPrompt := strings.TrimSpace(r.Form.Get("prompt"))
	if !utf8.ValidString(userPrompt) {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "AI prompt must be valid UTF-8"))
		return
	}
	if utf8.RuneCountInString(userPrompt) > agentPromptMaxRunes {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "AI prompt must be %d characters or fewer", agentPromptMaxRunes))
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
	note, normalizedKey, ok := noteForRoute(snapshot, key)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "note not found or stale"))
		return
	}
	packet, err := s.buildNoteInsightPacket(snapshot, note, userPrompt)
	if err != nil {
		writeNoteInsightBuildError(w, err)
		return
	}

	ctx := r.Context()
	if timeout := time.Duration(cfg.Agent.TimeoutSeconds) * time.Second; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	output, runErr := s.launchAgent(ctx, argv, packet)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeManagementError(w, noteops.Errorf(noteops.CodeInternal, "note insight command timed out"))
			return
		}
		message := "run note insight command"
		if diagnostic := agent.SanitizedDiagnostic(runErr); diagnostic != "" {
			message += ": " + diagnostic
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, message, runErr))
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
