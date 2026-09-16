package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

const (
	briefInsightAPIRoutePath      = "/api/brief/insight"
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
	cfg := s.config()
	if !s.requireManagementPost(w, r, briefInsightAPIRoutePath, 0) {
		return
	}

	cardID := brief.CardID(strings.TrimSpace(r.Form.Get("card_id")))
	if cardID == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "brief card id is required"))
		return
	}
	userPrompt, ok := readAgentPrompt(w, r)
	if !ok {
		return
	}
	argv := append([]string(nil), cfg.Agent.Command...)
	if len(argv) == 0 {
		writeManagementJSON(w, http.StatusOK, briefInsightResponse{
			OK:      true,
			Enabled: false,
			Message: "AI insight is not configured.",
		})
		return
	}

	target, ok := s.resolveBriefInsightTarget(w, cardID)
	if !ok {
		return
	}

	packet, err := s.buildBriefInsightPacket(target.selection, target.resolved.card, userPrompt)
	if err != nil {
		writeBriefInsightBuildError(w, err)
		return
	}

	output, ok := s.runAgentForRequest(w, r, argv, packet, cfg.Agent.TimeoutSeconds, agentRunLabels{
		timeout: "brief insight command timed out",
		failure: "run brief insight command",
	})
	if !ok {
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
		CardID:       target.resolved.card.ID,
		Slot:         target.resolved.slot,
		Output:       string(output),
		RenderedHTML: string(rendered),
	})
}

// briefInsightTarget is the card an insight was asked about together with the
// selection corpus that produced it. They travel together because a card ID is
// only meaningful against one brief: resolving the card from a freshly built
// brief and then ranking against a different corpus would quote notes the person
// was never shown.
type briefInsightTarget struct {
	selection brief.SelectionCorpus
	resolved  resolvedBriefCard
}

// resolveBriefInsightTarget rebuilds today's brief and finds the card the
// request names. It has already answered when it reports false.
//
// A brief whose state could not be read is a conflict rather than a server
// error: the brief is regenerated from scratch each time it is asked for, so an
// unreadable state file means the card the person clicked may not be the card
// that ID now refers to.
func (s *Server) resolveBriefInsightTarget(w http.ResponseWriter, cardID brief.CardID) (briefInsightTarget, bool) {
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "snapshot error", err))
		return briefInsightTarget{}, false
	}
	runtime, err := s.newBriefRuntime(snapshot)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build brief service", err))
		return briefInsightTarget{}, false
	}
	daily, err := runtime.service.Daily(runtime.selection.Snapshot)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "load current brief", err))
		return briefInsightTarget{}, false
	}
	if hasStateUnreadableWarning(daily.Warnings) {
		writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "brief state unavailable"))
		return briefInsightTarget{}, false
	}
	resolved, ok := resolveBriefCardByID(daily.Brief, cardID)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "brief card not found or stale"))
		return briefInsightTarget{}, false
	}
	return briefInsightTarget{selection: runtime.selection, resolved: resolved}, true
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
