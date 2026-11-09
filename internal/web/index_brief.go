package web

import (
	"errors"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/editor"
	"github.com/sglavoie/brainnotes/internal/query"
)

func rootBriefSlotViewFor(
	slot brief.SlotKind,
	result brief.SlotResult,
	editorConfigured bool,
	editorStatusMsg string,
	insightConfigured bool,
	insightStatusMsg string,
	stateUnreadable bool,
) rootBriefSlotView {
	view := rootBriefSlotView{
		Slot:             string(slot),
		Label:            slotLabel(slot),
		UnavailableTitle: unavailableTitle(slot),
	}
	if result.Slot == "" {
		result.Slot = slot
	}
	if result.Card == nil {
		view.UnavailableMsg = unavailableMessage(slot, result)
		return view
	}

	card := *result.Card
	sourceTitle := strings.TrimSpace(card.Source.Title)
	if sourceTitle == "" {
		sourceTitle = card.Source.RelPath
	}
	sourcePath := mustManagementNotePath(card.Source.RelPath)
	openTargetPath := mustManagementNotePath(openTargetRelPathForCard(card))
	candidateTitle := ""
	candidateRelPath := ""
	candidatePath := ""
	if card.Candidate != nil {
		candidateRelPath = card.Candidate.RelPath
		candidateTitle = strings.TrimSpace(card.Candidate.Title)
		if candidateTitle == "" {
			candidateTitle = candidateRelPath
		}
		candidatePath = mustManagementNotePath(candidateRelPath)
	}

	openEnabled := openTargetPath != "" && editorConfigured && !stateUnreadable
	openDisabledReason := ""
	switch {
	case stateUnreadable:
		openDisabledReason = "Open is unavailable until brief state is readable."
	case openTargetPath == "":
		openDisabledReason = "Open is unavailable because this note route could not be resolved."
	case !editorConfigured:
		openDisabledReason = editorStatusMsg
	}

	anotherEnabled := !stateUnreadable
	anotherDisabledMsg := ""
	if !anotherEnabled {
		anotherDisabledMsg = "Another is unavailable until brief state is readable."
	}

	insightEnabled := insightConfigured && !stateUnreadable
	insightDisabledMsg := ""
	switch {
	case stateUnreadable:
		insightDisabledMsg = "Ask AI is unavailable until brief state is readable."
	case !insightConfigured:
		insightDisabledMsg = insightStatusMsg
	}

	reasons := make([]string, 0, len(card.Metadata.Reasons))
	for _, reason := range card.Metadata.Reasons {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			continue
		}
		if trimmed == card.Explanation {
			continue
		}
		reasons = append(reasons, trimmed)
	}

	view.Card = &rootBriefCardView{
		ID:                 card.ID,
		SourceTitle:        sourceTitle,
		SourcePath:         sourcePath,
		SourceRelPath:      card.Source.RelPath,
		CandidateTitle:     candidateTitle,
		CandidatePath:      candidatePath,
		CandidateRelPath:   candidateRelPath,
		Explanation:        card.Explanation,
		Excerpt:            card.Excerpt,
		Reasons:            reasons,
		RelationshipLabel:  relationshipLabel(card.Metadata.Relationship),
		Bridge:             strings.TrimSpace(card.Metadata.Bridge),
		OpenEnabled:        openEnabled,
		OpenDisabledReason: openDisabledReason,
		AnotherEnabled:     anotherEnabled,
		AnotherDisabledMsg: anotherDisabledMsg,
		InsightEnabled:     insightEnabled,
		InsightDisabledMsg: insightDisabledMsg,
	}
	return view
}

func formatBriefDate(date brief.LocalDate) (string, string) {
	iso := strings.TrimSpace(string(date))
	if iso == "" {
		return "", "Today"
	}
	parsed, err := time.ParseInLocation("2006-01-02", iso, time.Local)
	if err != nil {
		return iso, iso
	}
	return iso, parsed.Format("Monday, January 2, 2006")
}

func stateUnreadableWarningMessage(warnings []brief.ServiceWarning) (bool, string) {
	for _, warning := range warnings {
		if warning.Code == brief.WarningStateUnreadable {
			return true, warning.Message
		}
	}
	return false, ""
}

func editorAvailability(configured, envEditor string) (bool, string) {
	_, err := editor.Spec(configured, envEditor)
	if err == nil {
		return true, ""
	}
	if errors.Is(err, editor.ErrNoEditorConfigured) {
		return false, "The external editor is not configured. Select a note title to read it here. Configure [open].editor or $EDITOR to open it in an editor."
	}
	return false, "The external editor configuration is invalid. Select a note title to read it here."
}

func slotLabel(slot brief.SlotKind) string {
	switch slot {
	case brief.SlotContinue:
		return "Continue"
	case brief.SlotRediscover:
		return "Rediscover"
	case brief.SlotConnect:
		return "Connect"
	default:
		return "Recommendation"
	}
}

func unavailableTitle(slot brief.SlotKind) string {
	switch slot {
	case brief.SlotContinue:
		return "No substantive recent note"
	case brief.SlotRediscover:
		return "No rediscover candidate"
	case brief.SlotConnect:
		return "No valid connection"
	default:
		return "No recommendation available"
	}
}

func unavailableMessage(slot brief.SlotKind, result brief.SlotResult) string {
	if result.Unavailable != nil && strings.TrimSpace(result.Unavailable.Message) != "" {
		return strings.TrimSpace(result.Unavailable.Message)
	}
	switch slot {
	case brief.SlotContinue:
		return "No substantive notes are available."
	case brief.SlotRediscover:
		return "No rediscover candidate is available."
	case brief.SlotConnect:
		return "No connect candidate is available."
	default:
		return "No recommendation is available."
	}
}

func relationshipLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "direct":
		return "Direct link"
	case "shared-neighbor":
		return "Shared neighbor"
	case "two-hop":
		return "Two-hop path"
	default:
		return ""
	}
}

func buildLibraryPageData(snapshot vaultSnapshot, root string) (libraryPageData, error) {
	summary, err := buildOverviewPageSummary(snapshot, root)
	if err != nil {
		return libraryPageData{}, err
	}
	return libraryPageData{
		Title:         defaultSearchTitle,
		FacetControls: overviewFacetControlsData(),
		Recent:        summary.Recent,
	}, nil
}

func buildHealthPageData(snapshot vaultSnapshot, root string) (healthPageData, error) {
	summary, err := buildOverviewPageSummary(snapshot, root)
	if err != nil {
		return healthPageData{}, err
	}
	return healthPageData{
		Title:             defaultHealthTitle,
		NoteCount:         summary.NoteCount,
		BrokenTargetCount: summary.BrokenTargetCount,
		OrphanCount:       summary.OrphanCount,
		GraphPath:         graphRoutePath,
		BrokenPath:        brokenRoutePath,
		LibraryPath:       libraryRoutePath,
		FacetControls:     overviewFacetControlsData(),
	}, nil
}

func buildOverviewPageSummary(snapshot vaultSnapshot, root string) (overviewPageSummary, error) {
	if snapshot.graph == nil {
		return overviewPageSummary{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return overviewPageSummary{}, errorsf("empty vault root")
	}

	orphan := query.Orphan(snapshot.graph)
	brokenTargets := map[string]struct{}{}
	recent := make([]indexRecentCandidate, 0, len(snapshot.notes))

	noteCount := 0
	orphanCount := 0
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}

		noteCount++
		if orphan(note) {
			orphanCount++
		}

		addOverviewBrokenTargets(brokenTargets, note, snapshot)

		candidate, err := overviewRecentCandidate(note, key, root)
		if err != nil {
			return overviewPageSummary{}, err
		}
		recent = append(recent, candidate)
	}

	return overviewPageSummary{
		NoteCount:         noteCount,
		BrokenTargetCount: len(brokenTargets),
		OrphanCount:       orphanCount,
		Recent:            overviewRecentViews(recent, root),
	}, nil
}
