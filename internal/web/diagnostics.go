package web

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type diagnosticsSummary struct {
	Cards             []diagnosticCard
	BrokenTargets     []diagnosticGroup
	OrphanNotes       []diagnosticNote
	DeadEndNotes      []diagnosticNote
	DuplicateTitles   []diagnosticGroup
	FrontmatterErrors []diagnosticFileError
	SkippedNotes      []diagnosticFileError
	SchemaViolations  []diagnosticGroup
	LongTailValues    []diagnosticLongTailGroup
}

type diagnosticCard struct {
	Key   string
	Label string
	Count int
	Path  string
}

type diagnosticGroup struct {
	Label string
	Count int
	Items []diagnosticNote
	Path  string
}

type diagnosticNote struct {
	Title      string
	Path       string
	Folder     string
	FolderPath string
	Location   string
}

type diagnosticFileError struct {
	Path  string
	Error string
}

type diagnosticLongTailGroup struct {
	Property string
	Values   []diagnosticValueCount
}

type diagnosticValueCount struct {
	Value string
	Count int
}

type diagnosticLoadedNote struct {
	note *vault.Note
	rel  string
}

type diagnosticsData struct {
	loaded            []diagnosticLoadedNote
	notes             []*vault.Note
	graph             *query.Graph
	frontmatterErrors []diagnosticFileError
	skippedNotes      []diagnosticFileError
}

func buildDiagnosticsSummary(v *vault.Vault, cfg *config.Config) (diagnosticsSummary, error) {
	if v == nil {
		return diagnosticsSummary{}, errorsf("nil vault")
	}
	if cfg == nil {
		return diagnosticsSummary{}, errorsf("nil config")
	}

	data, err := loadDiagnosticsData(v)
	if err != nil {
		return diagnosticsSummary{}, err
	}
	return buildDiagnosticsSummaryFromData(data, cfg), nil
}

func loadDiagnosticsData(v *vault.Vault) (diagnosticsData, error) {
	// Load through the shared bounded loader so the diagnostics walk classifies
	// timed-out reads the same way the server snapshot does. This call keeps no
	// persistent quarantine of its own; the shared snapshot owns that.
	result, err := loadVaultNotes(v, nil, 0, nil)
	if err != nil {
		return diagnosticsData{}, err
	}
	notes := make([]*vault.Note, 0, len(result.loaded))
	for _, item := range result.loaded {
		notes = append(notes, item.note)
	}

	return diagnosticsData{
		loaded:            result.loaded,
		notes:             notes,
		graph:             query.BuildGraphForRoot(v.Root, notes),
		frontmatterErrors: result.parseErrors,
		skippedNotes:      result.skipped,
	}, nil
}

// diagnosticsDataFromSnapshot adapts the resilient shared snapshot into the
// diagnosticsData view the homepage diagnostics summary and Daily Fixes panel
// consume, so a single bounded load feeds index data, health diagnostics, and
// fixes counts without re-walking the vault. The snapshot already carries the
// classified frontmatter parse errors and skipped (timed-out) notes; loaded
// pairs each healthy note with its vault-relative path in the snapshot's walk
// order.
func diagnosticsDataFromSnapshot(snapshot vaultSnapshot, root string) diagnosticsData {
	loaded := make([]diagnosticLoadedNote, 0, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		loaded = append(loaded, diagnosticLoadedNote{note: note, rel: displayPath(note.Path, root)})
	}
	return diagnosticsData{
		loaded:            loaded,
		notes:             snapshot.notes,
		graph:             snapshot.graph,
		frontmatterErrors: snapshot.parseErrors,
		skippedNotes:      snapshot.skipped,
	}
}

func buildDiagnosticsSummaryFromData(data diagnosticsData, cfg *config.Config) diagnosticsSummary {
	if cfg == nil {
		cfg = &config.Config{}
	}

	summary := diagnosticsSummary{
		BrokenTargets:     diagnosticBrokenTargets(data.loaded, data.graph),
		OrphanNotes:       diagnosticIssueNotes(data.loaded, data.graph, query.Orphan(data.graph)),
		DeadEndNotes:      diagnosticIssueNotes(data.loaded, data.graph, query.DeadEnd(data.graph)),
		DuplicateTitles:   diagnosticDuplicateTitles(data.loaded, data.graph),
		FrontmatterErrors: data.frontmatterErrors,
		SkippedNotes:      data.skippedNotes,
		SchemaViolations:  diagnosticSchemaViolations(data.loaded, data.graph, cfg),
		LongTailValues:    diagnosticLongTailValues(data.loaded, cfg),
	}
	summary.Cards = diagnosticCards(summary)
	return summary
}

func diagnosticCards(summary diagnosticsSummary) []diagnosticCard {
	brokenLinkCount := 0
	for _, group := range summary.BrokenTargets {
		brokenLinkCount += group.Count
	}

	duplicateNoteCount := 0
	for _, group := range summary.DuplicateTitles {
		duplicateNoteCount += group.Count
	}

	longTailCount := 0
	for _, group := range summary.LongTailValues {
		longTailCount += len(group.Values)
	}

	return []diagnosticCard{
		{Key: "broken", Label: "Broken links", Count: brokenLinkCount, Path: healthRoutePath + "#diagnostic-broken-targets"},
		{Key: "orphan", Label: "Orphans", Count: len(summary.OrphanNotes), Path: healthRoutePath + "#diagnostic-orphans"},
		{Key: "dead_end", Label: "Dead ends", Count: len(summary.DeadEndNotes), Path: healthRoutePath + "#diagnostic-dead-ends"},
		{Key: "duplicate_titles", Label: "Duplicate titles", Count: duplicateNoteCount, Path: healthRoutePath + "#diagnostic-duplicate-titles"},
		{Key: "frontmatter_errors", Label: "Frontmatter errors", Count: len(summary.FrontmatterErrors), Path: healthRoutePath + "#diagnostic-frontmatter-errors"},
		{Key: "skipped", Label: "Skipped notes", Count: len(summary.SkippedNotes), Path: healthRoutePath + "#diagnostic-skipped-notes"},
		{Key: "schema_violations", Label: "Schema violations", Count: diagnosticGroupItemCount(summary.SchemaViolations), Path: healthRoutePath + "#diagnostic-schema-violations"},
		{Key: "long_tail", Label: "Long-tail values", Count: longTailCount, Path: healthRoutePath + "#diagnostic-long-tail-values"},
	}
}
