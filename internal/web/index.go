package web

import (
	"os"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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
	PinnedPath           string
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
	// CaptureConfigured reports only that an inbox note and heading are
	// configured. The note itself is bootstrapped by the first capture, so the
	// box renders whether or not the file exists yet.
	CaptureConfigured bool
	CaptureRelPath    string
	CaptureHeading    string
	Recent            []indexRecentNote
	Pinned            []pinnedNoteView
	Health            rootCompactHealthData
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
	if s.vault == nil {
		return rootPageData{}, errorsf("nil server dependencies")
	}
	cfg := s.config()

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
	editorConfigured, editorStatusMsg, insightConfigured, insightStatusMsg := s.briefAvailability(cfg)

	slotsByKind := make(map[brief.SlotKind]brief.SlotResult, len(daily.Brief.Slots))
	for _, slot := range daily.Brief.Slots {
		slotsByKind[slot.Slot] = slot
	}
	continueSlot := rootBriefSlotViewFor(brief.SlotContinue, slotsByKind[brief.SlotContinue],
		editorConfigured, editorStatusMsg, insightConfigured, insightStatusMsg, stateUnreadable)
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
		BriefViewEndpoint:    briefViewAPIRoutePath,
		BriefItemEndpoint:    briefItemAPIRoutePath,
		BriefInsightEndpoint: briefInsightAPIRoutePath,
		LibraryPath:          libraryRoutePath,
		HealthPath:           healthRoutePath,
		GraphPath:            graphRoutePath,
		PinnedPath:           pinnedRoutePath,
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
		Pinned:             buildPinnedNoteViews(snapshot, s.vault.Root, indexPinnedLimit),
		Health:             health,
	}, nil
}

func (s *Server) briefAvailability(cfg *config.Config) (bool, string, bool, string) {
	editorConfigured, editorStatusMsg := editorAvailability(cfg.Open.Editor, os.Getenv("EDITOR"))
	if s.caps.browserOpen {
		// The Open action navigates instead of starting an editor, so the local
		// editor configuration is not a precondition for it.
		editorConfigured, editorStatusMsg = true, ""
	}
	insightConfigured, insightStatusMsg := s.agentAvailability(cfg, "AI insight is not configured. Set [agent].command to enable Ask AI.")

	return editorConfigured, editorStatusMsg, insightConfigured, insightStatusMsg
}
