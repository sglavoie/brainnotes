package web

import (
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/properties"
)

// propertyDefinitionView is the configured definition rendered for transport.
type propertyDefinitionView struct {
	Type     string   `json:"type"`
	Items    string   `json:"items,omitempty"`
	Required bool     `json:"required"`
	Allowed  []string `json:"allowed,omitempty"`
	Summary  string   `json:"summary"`
}

// propertySummary is one catalog row: what the configuration declares, what the
// vault actually stores, and how far apart the two are.
type propertySummary struct {
	Name       string                  `json:"name"`
	Path       string                  `json:"path"`
	Configured bool                    `json:"configured"`
	Definition *propertyDefinitionView `json:"definition,omitempty"`

	NoteCount       int      `json:"note_count"`
	TotalNotes      int      `json:"total_notes"`
	Coverage        float64  `json:"coverage"`
	MissingRequired int      `json:"missing_required"`
	ObservedKinds   []string `json:"observed_kinds"`
	DistinctValues  int      `json:"distinct_values"`

	ViolationNotes    int                        `json:"violation_notes"`
	Violations        int                        `json:"violations"`
	ViolationCounts   properties.ViolationCounts `json:"violation_counts"`
	UnsupportedValues int                        `json:"unsupported_values"`
	UnsupportedNotes  int                        `json:"unsupported_notes"`
	// Issues is the issue-first sort key: every violation plus every value the
	// workbench cannot represent.
	Issues int `json:"issues"`

	Proposal *properties.Proposal `json:"proposal,omitempty"`
}

// propertyRowView is one note's participation in a property.
type propertyRowView struct {
	RelPath     string             `json:"rel_path"`
	Title       string             `json:"title"`
	Folder      string             `json:"folder"`
	Path        string             `json:"path"`
	Hash        string             `json:"hash"`
	Present     bool               `json:"present"`
	Value       *properties.Value  `json:"value,omitempty"`
	Issues      []properties.Issue `json:"issues,omitempty"`
	Unsupported bool               `json:"unsupported"`
}

// propertyRowFilter is the echoed, normalized row filter.
type propertyRowFilter struct {
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
	Query  string `json:"q,omitempty"`
}

type propertyPagination struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

const (
	propertyFilterAll         = "all"
	propertyFilterViolations  = "violations"
	propertyFilterMissing     = "missing"
	propertyFilterUnsupported = "unsupported"
)

func buildPropertySummary(prop *properties.Property) propertySummary {
	if prop == nil {
		return propertySummary{}
	}

	summary := propertySummary{
		Name:              prop.Name,
		Path:              propertyDetailPath(prop.Name),
		Configured:        prop.Configured,
		NoteCount:         prop.NoteCount,
		TotalNotes:        prop.TotalNotes,
		Coverage:          prop.Coverage(),
		MissingRequired:   prop.MissingRequired,
		ObservedKinds:     kindStrings(prop.ObservedKinds),
		DistinctValues:    prop.DistinctValues(),
		ViolationNotes:    prop.ViolationNotes,
		Violations:        prop.ViolationCounts.Total(),
		ViolationCounts:   prop.ViolationCounts,
		UnsupportedValues: prop.UnsupportedValues,
		UnsupportedNotes:  prop.UnsupportedNotes,
		Proposal:          prop.Proposal,
	}
	summary.Issues = summary.Violations + summary.UnsupportedValues
	if prop.Definition != nil {
		def := *prop.Definition
		summary.Definition = &propertyDefinitionView{
			Type:     string(def.Type),
			Items:    string(def.Items),
			Required: def.Required,
			Allowed:  def.Allowed,
			Summary:  def.Summary(),
		}
	}
	return summary
}

// buildPropertySummaries renders the catalog issue-first: the properties that
// need attention lead, then the most widely used, then alphabetical.
func buildPropertySummaries(index *properties.Index) []propertySummary {
	if index == nil {
		return []propertySummary{}
	}
	summaries := make([]propertySummary, 0, len(index.Properties))
	for _, prop := range index.Properties {
		summaries = append(summaries, buildPropertySummary(prop))
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].Issues != summaries[j].Issues {
			return summaries[i].Issues > summaries[j].Issues
		}
		if summaries[i].NoteCount != summaries[j].NoteCount {
			return summaries[i].NoteCount > summaries[j].NoteCount
		}
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

func buildPropertyProposals(index *properties.Index) []properties.Proposal {
	proposals := []properties.Proposal{}
	if index == nil {
		return proposals
	}
	for _, prop := range index.Properties {
		if prop.Proposal != nil {
			proposals = append(proposals, *prop.Proposal)
		}
	}
	return proposals
}

// selectPropertyRows filters, then pages, the note rows for one property. The
// filter runs before pagination so the reported total is the number of rows the
// user is actually browsing.
func selectPropertyRows(
	prop *properties.Property,
	filter propertyRowFilter,
	limit, offset int,
) ([]propertyRowView, propertyPagination) {
	rows := []propertyRowView{}
	if prop == nil {
		return rows, propertyPagination{Limit: limit, Offset: offset}
	}

	matched := make([]properties.NoteRow, 0, len(prop.Rows))
	for _, row := range prop.Rows {
		if propertyRowMatches(row, filter) {
			matched = append(matched, row)
		}
	}

	total := len(matched)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	for _, row := range matched[start:end] {
		rows = append(rows, buildPropertyRowView(row))
	}

	return rows, propertyPagination{
		Limit:    limit,
		Offset:   offset,
		Total:    total,
		Returned: len(rows),
		HasMore:  end < total,
	}
}

func buildPropertyRowView(row properties.NoteRow) propertyRowView {
	view := propertyRowView{
		RelPath:     row.RelPath,
		Title:       row.Title,
		Folder:      row.Folder,
		Path:        mustManagementNotePath(row.RelPath),
		Hash:        row.Hash,
		Present:     row.Present,
		Issues:      row.Issues,
		Unsupported: row.HasUnsupported(),
	}
	if row.Present {
		value := row.Value
		view.Value = &value
	}
	return view
}

func propertyRowMatches(row properties.NoteRow, filter propertyRowFilter) bool {
	switch filter.Status {
	case propertyFilterViolations:
		if len(row.Issues) == 0 {
			return false
		}
	case propertyFilterMissing:
		if row.Present {
			return false
		}
	case propertyFilterUnsupported:
		if !row.HasUnsupported() {
			return false
		}
	}

	if filter.Value != "" && !rowHasLogicalValue(row, filter.Value) {
		return false
	}
	if filter.Query != "" {
		needle := strings.ToLower(filter.Query)
		if !strings.Contains(strings.ToLower(row.RelPath), needle) &&
			!strings.Contains(strings.ToLower(row.Title), needle) {
			return false
		}
	}
	return true
}

// rowHasLogicalValue matches a value exactly, because allowed values and
// wikilink targets are case-sensitive everywhere else in the vault.
func rowHasLogicalValue(row properties.NoteRow, value string) bool {
	if !row.Present {
		return false
	}
	for _, logical := range row.Value.Logical() {
		if logical.Text == value {
			return true
		}
	}
	return false
}

func kindStrings(kinds []properties.Kind) []string {
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, string(kind))
	}
	return out
}
