package web

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

const (
	indexRecentLimit   = 12
	indexTimeLayout    = "2006-01-02 15:04"
	indexDefaultTitle  = "Brief - bn serve"
	libraryRoutePath   = "/library"
	healthRoutePath    = "/health"
	graphRoutePath     = "/graph"
	brokenRoutePath    = "/broken"
	defaultSearchTitle = "Library - bn serve"
	defaultHealthTitle = "Health - bn serve"
)

type rootPageData struct {
	Title                string
	Heading              string
	CSRFToken            string
	DateISO              string
	DateLabel            string
	BriefViewEndpoint    string
	BriefItemEndpoint    string
	BriefInsightEndpoint string
	LibraryPath          string
	HealthPath           string
	GraphPath            string
	Activity             rootActivityData
	Continue             rootBriefSlotView
	Rediscover           rootBriefSlotView
	Connect              rootBriefSlotView
	IsEmptyVault         bool
	HasPartialSnapshot   bool
	ParseErrorCount      int
	SkippedNoteCount     int
	StateUnreadable      bool
	StateUnreadableMsg   string
	EditorConfigured     bool
	EditorStatusMsg      string
	InsightConfigured    bool
	InsightStatusMsg     string
	Recent               []indexRecentNote
	Health               rootCompactHealthData
}

type rootActivityData struct {
	VisitDays int
	OpenCount int
}

type rootBriefSlotView struct {
	Slot             string
	Label            string
	Card             *rootBriefCardView
	UnavailableTitle string
	UnavailableMsg   string
}

type rootBriefCardView struct {
	ID                 brief.CardID
	SourceTitle        string
	SourcePath         string
	SourceRelPath      string
	CandidateTitle     string
	CandidatePath      string
	CandidateRelPath   string
	Explanation        string
	Excerpt            string
	Reasons            []string
	RelationshipLabel  string
	Bridge             string
	OpenEnabled        bool
	OpenDisabledReason string
	AnotherEnabled     bool
	AnotherDisabledMsg string
	InsightEnabled     bool
	InsightDisabledMsg string
}

type rootCompactHealthData struct {
	BrokenTargetCount int
	OrphanCount       int
	ParseErrorCount   int
	SkippedNoteCount  int
	TotalIssueCount   int
}

type libraryPageData struct {
	Title         string
	CSRFToken     string
	FacetControls facetControlsData
	Recent        []indexRecentNote
}

type healthPageData struct {
	Title             string
	CSRFToken         string
	NoteCount         int
	BrokenTargetCount int
	OrphanCount       int
	GraphPath         string
	BrokenPath        string
	LibraryPath       string
	FacetControls     facetControlsData
	Fixes             indexFixesPanelData
	Diagnostics       diagnosticsSummary
}

type overviewPageSummary struct {
	NoteCount         int
	BrokenTargetCount int
	OrphanCount       int
	Recent            []indexRecentNote
}

// indexFixesPanelData is the server-rendered shell for the homepage Daily Fixes
// panel. It seeds the counts and any queue warnings; the task list itself is
// hydrated client-side from GET /api/cockpit so the interactive fix affordances
// (preview/apply) always run against the live queue.
type indexFixesPanelData struct {
	Counts      cockpit.Counts
	Warnings    []indexFixesWarning
	HasTasks    bool
	HasWarnings bool
}

type indexFixesWarning struct {
	Message string
}

// buildIndexFixesPanelData maps a filtered cockpit queue onto the homepage
// panel shell.
func buildIndexFixesPanelData(queue cockpit.Queue) indexFixesPanelData {
	warnings := make([]indexFixesWarning, 0, len(queue.Warnings))
	for _, w := range queue.Warnings {
		warnings = append(warnings, indexFixesWarning{Message: w.Message})
	}
	return indexFixesPanelData{
		Counts:      queue.Counts,
		Warnings:    warnings,
		HasTasks:    len(queue.Tasks) > 0,
		HasWarnings: len(warnings) > 0,
	}
}

type indexRecentNote struct {
	Title   string
	Path    string
	Folder  string
	Updated string
}

type indexRecentCandidate struct {
	view    indexRecentNote
	modTime time.Time
}

func (s *Server) buildRootPageData(snapshot vaultSnapshot) (rootPageData, error) {
	if s == nil {
		return rootPageData{}, errorsf("nil server")
	}
	if s.cfg == nil || s.vault == nil {
		return rootPageData{}, errorsf("nil server dependencies")
	}

	summary, err := buildOverviewPageSummary(snapshot, s.vault.Root)
	if err != nil {
		return rootPageData{}, err
	}
	runtime, err := s.newBriefRuntime(snapshot)
	if err != nil {
		return rootPageData{}, err
	}
	daily, err := runtime.service.Daily(runtime.selection.Snapshot)
	if err != nil {
		return rootPageData{}, err
	}

	stateUnreadable, stateUnreadableMsg := stateUnreadableWarningMessage(daily.Warnings)
	editorConfigured, editorStatusMsg := editorAvailability(s.cfg.Open.Editor, os.Getenv("EDITOR"))
	insightConfigured := len(s.cfg.Agent.Command) > 0
	insightStatusMsg := ""
	if !insightConfigured {
		insightStatusMsg = "AI insight is not configured. Set [agent].command to enable Ask AI."
	}

	slotsByKind := make(map[brief.SlotKind]brief.SlotResult, len(daily.Brief.Slots))
	for _, slot := range daily.Brief.Slots {
		slotsByKind[slot.Slot] = slot
	}
	continueSlot := rootBriefSlotViewFor(
		brief.SlotContinue,
		slotsByKind[brief.SlotContinue],
		editorConfigured,
		editorStatusMsg,
		insightConfigured,
		insightStatusMsg,
		stateUnreadable,
	)
	rediscoverSlot := rootBriefSlotViewFor(
		brief.SlotRediscover,
		slotsByKind[brief.SlotRediscover],
		editorConfigured,
		editorStatusMsg,
		insightConfigured,
		insightStatusMsg,
		stateUnreadable,
	)
	connectSlot := rootBriefSlotViewFor(
		brief.SlotConnect,
		slotsByKind[brief.SlotConnect],
		editorConfigured,
		editorStatusMsg,
		insightConfigured,
		insightStatusMsg,
		stateUnreadable,
	)

	dateISO, dateLabel := formatBriefDate(daily.Brief.Date)
	parseErrorCount := len(snapshot.parseErrors)
	skippedNoteCount := len(snapshot.skipped)
	health := rootCompactHealthData{
		BrokenTargetCount: summary.BrokenTargetCount,
		OrphanCount:       summary.OrphanCount,
		ParseErrorCount:   parseErrorCount,
		SkippedNoteCount:  skippedNoteCount,
	}
	health.TotalIssueCount = health.BrokenTargetCount + health.OrphanCount + health.ParseErrorCount + health.SkippedNoteCount

	hasPartialSnapshot := parseErrorCount > 0 || skippedNoteCount > 0
	isEmptyVault := summary.NoteCount == 0 && !hasPartialSnapshot

	return rootPageData{
		Title:                indexDefaultTitle,
		Heading:              "Daily Brief",
		DateISO:              dateISO,
		DateLabel:            dateLabel,
		BriefViewEndpoint:    "/api/brief/view",
		BriefItemEndpoint:    "/api/brief/item",
		BriefInsightEndpoint: "/api/brief/insight",
		LibraryPath:          libraryRoutePath,
		HealthPath:           healthRoutePath,
		GraphPath:            graphRoutePath,
		Activity: rootActivityData{
			VisitDays: daily.Activity.Last7Days.VisitDays,
			OpenCount: daily.Activity.Last7Days.OpenCount,
		},
		Continue:           continueSlot,
		Rediscover:         rediscoverSlot,
		Connect:            connectSlot,
		IsEmptyVault:       isEmptyVault,
		HasPartialSnapshot: hasPartialSnapshot,
		ParseErrorCount:    parseErrorCount,
		SkippedNoteCount:   skippedNoteCount,
		StateUnreadable:    stateUnreadable,
		StateUnreadableMsg: stateUnreadableMsg,
		EditorConfigured:   editorConfigured,
		EditorStatusMsg:    editorStatusMsg,
		InsightConfigured:  insightConfigured,
		InsightStatusMsg:   insightStatusMsg,
		Recent:             summary.Recent,
		Health:             health,
	}, nil
}

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
		return false, "Open is disabled until [open].editor or $EDITOR is configured."
	}
	return false, "Open is disabled because editor configuration is invalid."
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

		issues := query.UnresolvedLinks(note, snapshot.graph)
		for _, issue := range issues {
			target := snapshot.graph.UnresolvedTargetKey(issue.Link.Target)
			if target == "" {
				target = strings.ToLower(strings.TrimSpace(issue.Link.Target))
			}
			if target == "" {
				continue
			}
			brokenTargets[target] = struct{}{}
		}

		path, err := notePathForKey(key)
		if err != nil {
			return overviewPageSummary{}, fmt.Errorf("route path for %q: %w", key, err)
		}
		folder, err := note.Folder(root)
		if err != nil {
			return overviewPageSummary{}, fmt.Errorf("folder for %q: %w", key, err)
		}
		info, err := os.Stat(note.Path)
		if err != nil {
			return overviewPageSummary{}, fmt.Errorf("stat %q: %w", note.Path, err)
		}

		title := note.Title()
		if title == "" {
			title = key
		}
		recent = append(recent, indexRecentCandidate{
			view: indexRecentNote{
				Title:   title,
				Path:    path,
				Folder:  folder,
				Updated: info.ModTime().Local().Format(indexTimeLayout),
			},
			modTime: info.ModTime(),
		})
	}

	sort.SliceStable(recent, func(i, j int) bool {
		if !recent[i].modTime.Equal(recent[j].modTime) {
			return recent[i].modTime.After(recent[j].modTime)
		}
		leftTitle := strings.ToLower(recent[i].view.Title)
		rightTitle := strings.ToLower(recent[j].view.Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return recent[i].view.Path < recent[j].view.Path
	})
	if len(recent) > indexRecentLimit {
		recent = recent[:indexRecentLimit]
	}

	recentViews := make([]indexRecentNote, 0, len(recent))
	for _, item := range recent {
		recentViews = append(recentViews, item.view)
	}

	return overviewPageSummary{
		NoteCount:         noteCount,
		BrokenTargetCount: len(brokenTargets),
		OrphanCount:       orphanCount,
		Recent:            recentViews,
	}, nil
}
