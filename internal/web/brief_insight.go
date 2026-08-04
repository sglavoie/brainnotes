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
	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

const (
	briefInsightPurposeContinue   = "brief-continue"
	briefInsightPurposeRediscover = "brief-rediscover"
	briefInsightPurposeConnect    = "brief-connect"
	agentPromptMaxRunes           = 2000
)

var errBriefInsightMissingCandidate = errors.New("web: connect card is missing its displayed candidate")

type briefInsightResponse struct {
	OK           bool           `json:"ok"`
	Enabled      bool           `json:"enabled"`
	Message      string         `json:"message,omitempty"`
	CardID       brief.CardID   `json:"card_id,omitempty"`
	Slot         brief.SlotKind `json:"slot,omitempty"`
	Output       string         `json:"output,omitempty"`
	RenderedHTML string         `json:"rendered_html,omitempty"`
}

func (s *Server) handleBriefInsight(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/brief/insight" {
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

	cardID := brief.CardID(strings.TrimSpace(r.Form.Get("card_id")))
	if cardID == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "brief card id is required"))
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
	argv := append([]string(nil), s.cfg.Agent.Command...)
	if len(argv) == 0 {
		writeManagementJSON(w, http.StatusOK, briefInsightResponse{
			OK:      true,
			Enabled: false,
			Message: "AI insight is not configured.",
		})
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "snapshot error", err))
		return
	}
	runtime, err := s.newBriefRuntime(snapshot)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build brief service", err))
		return
	}
	daily, err := runtime.service.Daily(runtime.selection.Snapshot)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "load current brief", err))
		return
	}
	if hasStateUnreadableWarning(daily.Warnings) {
		writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "brief state unavailable"))
		return
	}
	resolved, ok := resolveBriefCardByID(daily.Brief, cardID)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "brief card not found or stale"))
		return
	}

	packet, err := s.buildBriefInsightPacket(runtime.selection, resolved.card, userPrompt)
	if err != nil {
		writeBriefInsightBuildError(w, err)
		return
	}

	ctx := r.Context()
	if timeout := time.Duration(s.cfg.Agent.TimeoutSeconds) * time.Second; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	output, runErr := s.runAgent(ctx, argv, packet)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeManagementError(w, noteops.Errorf(noteops.CodeInternal, "brief insight command timed out"))
			return
		}
		message := "run brief insight command"
		if diagnostic := agent.SanitizedDiagnostic(runErr); diagnostic != "" {
			message += ": " + diagnostic
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, message, runErr))
		return
	}
	rendered, err := s.markdown.render(string(output))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "render brief insight output", err))
		return
	}

	writeManagementJSON(w, http.StatusOK, briefInsightResponse{
		OK:           true,
		Enabled:      true,
		CardID:       resolved.card.ID,
		Slot:         resolved.slot,
		Output:       string(output),
		RenderedHTML: string(rendered),
	})
}

func (s *Server) buildBriefInsightPacket(selection brief.SelectionCorpus, card brief.Card, userPrompt string) ([]byte, error) {
	documents := selection.RecallDocuments()
	if len(documents) == 0 {
		return nil, errors.New("web: brief insight corpus is empty")
	}
	source, ok := documentByPath(documents, card.Source.RelPath)
	if !ok {
		return nil, fmt.Errorf("web: brief insight source %q is unavailable", card.Source.RelPath)
	}

	purpose, mode, requiredPath, err := briefInsightPrompt(card, userPrompt)
	if err != nil {
		return nil, err
	}

	ranker, err := recall.BuildRanker(documents, selection.RecallGraph(), recall.RankOptions{Now: s.briefNow})
	if err != nil {
		return nil, err
	}
	results, err := ranker.Rank(source.Path, mode)
	if err != nil {
		return nil, err
	}
	if requiredPath != "" {
		results, err = prioritizeRequiredRelated(results, requiredPath)
		if err != nil {
			return nil, err
		}
	}

	options := recall.DefaultContextBundleOptions()
	options.Purpose = purpose
	bundle, err := recall.BuildContextBundle(source, documents, results, options)
	if err != nil {
		return nil, err
	}
	return recall.RenderContextMarkdown(bundle)
}

func briefInsightPrompt(card brief.Card, userPrompt string) (recall.ContextPurpose, recall.Mode, string, error) {
	withUserPrompt := func(instructions string) string {
		if userPrompt == "" {
			return instructions
		}
		return instructions + "\n\nUser request (prioritize this when it conflicts with the default guidance):\n" + userPrompt
	}
	switch card.Slot {
	case brief.SlotContinue:
		return recall.ContextPurpose{
			Name:         briefInsightPurposeContinue,
			Instructions: withUserPrompt("Identify unanswered questions and the next supporting evidence around the source note."),
		}, recall.ModeBalanced, "", nil
	case brief.SlotRediscover:
		return recall.ContextPurpose{
			Name:         briefInsightPurposeRediscover,
			Instructions: withUserPrompt("Synthesize why this longer-unseen note may matter now and which related paths are most useful to revisit."),
		}, recall.ModeBalanced, "", nil
	case brief.SlotConnect:
		if card.Candidate == nil {
			return recall.ContextPurpose{}, "", "", errBriefInsightMissingCandidate
		}
		return recall.ContextPurpose{
			Name: briefInsightPurposeConnect,
			Instructions: withUserPrompt(fmt.Sprintf(
				"Compare %q and %q, calling out tensions, missing evidence, and a plausible connection path.",
				card.Source.Title,
				card.Candidate.Title,
			)),
		}, recall.ModeConnect, card.Candidate.RelPath, nil
	default:
		return recall.ContextPurpose{}, "", "", fmt.Errorf("web: unsupported brief slot %q", card.Slot)
	}
}

func prioritizeRequiredRelated(results []recall.Result, requiredPath string) ([]recall.Result, error) {
	if requiredPath == "" {
		return append([]recall.Result(nil), results...), nil
	}
	requiredIndex := -1
	for i, result := range results {
		if result.Path == requiredPath {
			requiredIndex = i
			break
		}
	}
	if requiredIndex < 0 {
		return nil, fmt.Errorf("web: required related path %q is unavailable", requiredPath)
	}
	if requiredIndex == 0 {
		return append([]recall.Result(nil), results...), nil
	}
	ordered := make([]recall.Result, 0, len(results))
	ordered = append(ordered, results[requiredIndex])
	ordered = append(ordered, results[:requiredIndex]...)
	ordered = append(ordered, results[requiredIndex+1:]...)
	return ordered, nil
}

func documentByPath(documents []recall.Document, path string) (recall.Document, bool) {
	for _, document := range documents {
		if document.Path == path {
			return document, true
		}
	}
	return recall.Document{}, false
}

func writeBriefInsightBuildError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBriefInsightMissingCandidate):
		writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "connect card candidate is unavailable for insight"))
	case errors.Is(err, recall.ErrSourceBodyTooLarge), errors.Is(err, recall.ErrRequiredContextTooLarge):
		writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "brief insight context exceeds byte limit"))
	default:
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build brief insight context", err))
	}
}
