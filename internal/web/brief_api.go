package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

type briefViewResponse struct {
	OK       bool                  `json:"ok"`
	Activity brief.RollingActivity `json:"activity"`
}

type briefItemResponse struct {
	Destination string                `json:"destination,omitempty"`
	OK          bool                  `json:"ok"`
	Action      string                `json:"action"`
	CardID      brief.CardID          `json:"card_id"`
	Slot        brief.SlotKind        `json:"slot"`
	Daily       brief.DailyBrief      `json:"daily,omitempty"`
	Activity    brief.RollingActivity `json:"activity"`
	Affected    []brief.SlotResult    `json:"affected,omitempty"`
}

type briefRuntime struct {
	service   *brief.Service
	selection brief.SelectionCorpus
}

type resolvedBriefCard struct {
	slot brief.SlotKind
	card brief.Card
}

func (s *Server) handleBriefView(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/brief/view" {
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

	now, date, err := s.briefNowAndDate()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "brief clock error", err))
		return
	}
	activity, err := s.briefState.RecordVisit(date, now)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "record brief view", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, briefViewResponse{
		OK:       true,
		Activity: activity,
	})
}

func (s *Server) handleBriefItem(w http.ResponseWriter, r *http.Request) {
	cardID, action, valid := s.parseBriefItemRequest(w, r)
	if !valid {
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

	s.executeBriefAction(w, runtime, resolved, action)
}

func (s *Server) executeBriefAction(w http.ResponseWriter, runtime briefRuntime, resolved resolvedBriefCard, action string) {
	switch action {
	case "open":
		noteRelPath := openTargetRelPathForCard(resolved.card)
		notePath := filepath.Join(s.vault.Root, filepath.FromSlash(noteRelPath))
		destination, err := s.openBriefTarget(notePath, noteRelPath)
		if err != nil {
			writeBriefOpenError(w, err)
			return
		}
		activity, err := runtime.service.RecordOpen(resolved.card.ID)
		if err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "record brief open", err))
			return
		}
		writeManagementJSON(w, http.StatusOK, briefItemResponse{
			OK:          true,
			Action:      "open",
			Destination: destination,
			CardID:      resolved.card.ID,
			Slot:        resolved.slot,
			Activity:    activity,
		})
	case "another":
		another, err := runtime.service.Another(runtime.selection.Snapshot, resolved.slot)
		if err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "replace brief card", err))
			return
		}
		writeManagementJSON(w, http.StatusOK, briefItemResponse{
			OK:       true,
			Action:   "another",
			CardID:   resolved.card.ID,
			Slot:     resolved.slot,
			Daily:    another.Daily.Brief,
			Activity: another.Daily.Activity,
			Affected: another.Affected,
		})
	}
}

func openTargetRelPathForCard(card brief.Card) string {
	if card.Slot == brief.SlotConnect && card.Candidate != nil {
		candidate := strings.TrimSpace(card.Candidate.RelPath)
		if candidate != "" {
			return candidate
		}
	}
	return card.Source.RelPath
}

func (s *Server) newBriefRuntime(snapshot vaultSnapshot) (briefRuntime, error) {
	cfg := s.config()
	selection, err := brief.BuildSelectionCorpus(s.vault.Root, snapshot.notes, snapshot.graph, brief.SelectionCorpusOptions{
		MetadataProperties: append([]string(nil), cfg.Related.MetadataProperties...),
		ExcludeFolders:     append([]string(nil), cfg.Brief.ExcludeFolders...),
		ExcludeTypes:       append([]string(nil), cfg.Brief.ExcludeTypes...),
	})
	if err != nil {
		return briefRuntime{}, err
	}
	selector, err := brief.NewDeterministicSelector(selection, brief.DeterministicSelectorOptions{
		State: s.briefState,
		Now:   s.briefNow,
	})
	if err != nil {
		return briefRuntime{}, err
	}
	service, err := brief.NewService(s.briefState, selector, brief.WithServiceClock(s.briefNow))
	if err != nil {
		return briefRuntime{}, err
	}
	return briefRuntime{
		service:   service,
		selection: selection,
	}, nil
}

func (s *Server) briefNowAndDate() (time.Time, brief.LocalDate, error) {
	if s == nil {
		return time.Time{}, "", errors.New("web: nil server")
	}
	if s.briefNow == nil {
		return time.Time{}, "", errors.New("web: nil brief clock")
	}
	now := s.briefNow()
	if now.IsZero() {
		return time.Time{}, "", errors.New("web: zero brief clock time")
	}
	return now, brief.LocalDate(now.Format("2006-01-02")), nil
}

func resolveBriefCardByID(daily brief.DailyBrief, cardID brief.CardID) (resolvedBriefCard, bool) {
	for _, slot := range daily.Slots {
		if slot.Card == nil {
			continue
		}
		if slot.Card.ID != cardID {
			continue
		}
		return resolvedBriefCard{
			slot: slot.Slot,
			card: *slot.Card,
		}, true
	}
	return resolvedBriefCard{}, false
}

func hasStateUnreadableWarning(warnings []brief.ServiceWarning) bool {
	for _, warning := range warnings {
		if warning.Code == brief.WarningStateUnreadable {
			return true
		}
	}
	return false
}

func writeBriefOpenError(w http.ResponseWriter, err error) {
	switch failureStage(err) {
	case openFailureStat:
		if errors.Is(err, os.ErrNotExist) {
			writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "note not found"))
			return
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "note stat error", err))
		return
	case openFailureResolvePath:
		switch {
		case errors.Is(err, errOpenPathOutsideVault):
			writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "forbidden note path"))
		case errors.Is(err, os.ErrNotExist):
			writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "note not found"))
		default:
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "note path error", err))
		}
		return
	case openFailureEditorSpec:
		if errors.Is(err, editor.ErrNoEditorConfigured) {
			writeManagementErrorStatus(w, http.StatusPreconditionFailed, noteops.Errorf(noteops.CodeInvalidRequest, "editor not configured"))
			return
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "editor spec error", err))
		return
	case openFailureEditorTarget:
		writeManagementErrorStatus(w, http.StatusPreconditionFailed, noteops.Errorf(noteops.CodeInvalidRequest, "editor resolve error"))
		return
	case openFailureEditorStart:
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "editor start error", err))
		return
	default:
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "editor start error", err))
	}
}

func (s *Server) parseBriefItemRequest(w http.ResponseWriter, r *http.Request) (brief.CardID, string, bool) {
	if r.URL.Path != "/api/brief/item" {
		http.NotFound(w, r)
		return "", "", false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return "", "", false
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return "", "", false
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return "", "", false
	}

	cardID := brief.CardID(strings.TrimSpace(r.Form.Get("card_id")))
	if cardID == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "brief card id is required"))
		return "", "", false
	}
	action := strings.ToLower(strings.TrimSpace(r.Form.Get("action")))
	if action != "open" && action != "another" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported brief action %q", action))
		return "", "", false
	}

	return cardID, action, true
}
