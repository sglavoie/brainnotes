package properties

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/schema"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// Issue is one schema violation scoped to a single note and property.
type Issue struct {
	// Kind is missing, type, or value, matching schema.Violation.
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// NoteRow is one note's participation in a property. Present is false only for
// a note that is missing a required property; every other row carries the value
// the note actually stores.
type NoteRow struct {
	RelPath string `json:"rel_path"`
	Title   string `json:"title"`
	Folder  string `json:"folder"`
	// Hash is the SHA-256 of the note bytes this row was built from. An
	// immediate single-note edit sends it back so a write that races an
	// external change is refused instead of clobbering it.
	Hash    string  `json:"hash"`
	Present bool    `json:"present"`
	Value   Value   `json:"value"`
	Issues  []Issue `json:"issues"`
}

// HasUnsupported reports whether the row stores a value with no supported
// scalar spelling, either directly or as a list element.
func (r NoteRow) HasUnsupported() bool {
	if !r.Present {
		return false
	}
	if !r.Value.Supported {
		return true
	}
	for _, item := range r.Value.Items {
		if !item.Supported {
			return true
		}
	}
	return false
}

// ValueCount is one distinct logical value and the number of notes using it.
type ValueCount struct {
	Value Value `json:"value"`
	Notes int   `json:"notes"`
}

// ViolationCounts breaks a property's violations down by schema kind.
type ViolationCounts struct {
	Missing int `json:"missing"`
	Type    int `json:"type"`
	Value   int `json:"value"`
}

// Total returns the number of violations across all kinds.
func (c ViolationCounts) Total() int { return c.Missing + c.Type + c.Value }

// Property is the aggregate intelligence for one frontmatter property.
type Property struct {
	Name       string                     `json:"name"`
	Configured bool                       `json:"configured"`
	Definition *config.PropertyDefinition `json:"definition,omitempty"`

	// NoteCount is the number of notes that store the property.
	NoteCount int `json:"note_count"`
	// TotalNotes is the number of notes in the vault, so coverage can be
	// recomputed by any consumer without a second lookup.
	TotalNotes int `json:"total_notes"`
	// MissingRequired counts notes that omit a property declared required.
	MissingRequired int `json:"missing_required"`
	// ObservedKinds lists every kind observed in stored values, sorted. A list
	// property contributes both list and its element kinds.
	ObservedKinds []Kind `json:"observed_kinds"`
	// Values holds every distinct logical value with the number of notes using
	// it, counted at most once per note.
	Values []ValueCount `json:"values"`
	// ViolationNotes is the number of notes with at least one violation.
	ViolationNotes  int             `json:"violation_notes"`
	ViolationCounts ViolationCounts `json:"violation_counts"`
	// UnsupportedValues counts logical values with no supported scalar
	// spelling, counted at most once per note per label.
	UnsupportedValues int `json:"unsupported_values"`
	// UnsupportedNotes is the number of notes storing at least one such value.
	UnsupportedNotes int `json:"unsupported_notes"`

	// Rows lists every note that stores the property plus every note missing it
	// while it is required, sorted by vault-relative path.
	Rows []NoteRow `json:"rows"`

	// Proposal is the inferred definition for an unconfigured property whose
	// observed values unanimously support one kind.
	Proposal *Proposal `json:"proposal,omitempty"`
}

// Coverage is the fraction of vault notes that store the property.
func (p *Property) Coverage() float64 {
	if p == nil || p.TotalNotes <= 0 {
		return 0
	}
	return float64(p.NoteCount) / float64(p.TotalNotes)
}

// DistinctValues is the number of distinct logical values observed.
func (p *Property) DistinctValues() int {
	if p == nil {
		return 0
	}
	return len(p.Values)
}

// Totals aggregates the index across every property.
type Totals struct {
	Properties      int `json:"properties"`
	Configured      int `json:"configured"`
	Unconfigured    int `json:"unconfigured"`
	Notes           int `json:"notes"`
	Violations      int `json:"violations"`
	ViolationNotes  int `json:"violation_notes"`
	MissingRequired int `json:"missing_required"`
	Unsupported     int `json:"unsupported_values"`
	Proposals       int `json:"proposals"`
}

// Index is the immutable property intelligence for one vault snapshot and one
// configuration. Build never mutates its inputs, so a published index can be
// shared by concurrent readers.
type Index struct {
	TotalNotes int
	Properties []*Property
	Totals     Totals

	byName map[string]*Property
}

// Property returns the aggregate for name.
func (i *Index) Property(name string) (*Property, bool) {
	if i == nil {
		return nil, false
	}
	prop, ok := i.byName[name]
	return prop, ok
}

// Names returns every indexed property name in sorted order.
func (i *Index) Names() []string {
	if i == nil {
		return nil
	}
	names := make([]string, 0, len(i.Properties))
	for _, prop := range i.Properties {
		names = append(names, prop.Name)
	}
	return names
}

// builder accumulates one property while notes are walked.
type builder struct {
	prop      *Property
	valueKeys map[string]int
	values    []ValueCount
	// unanimous tracks whether the observed values still support a single
	// inferred kind.
	inference inferenceState
}

// Build indexes every property observed in notes plus every configured
// property, whether or not a note uses it. root is the vault root used to
// render vault-relative row paths; fm supplies the configured definitions.
func Build(root string, notes []*vault.Note, fm *config.FrontmatterConfig) *Index {
	builders := make(map[string]*builder)
	order := make([]string, 0, len(notes))

	ensure := func(name string) *builder {
		if b, ok := builders[name]; ok {
			return b
		}
		b := &builder{
			prop:      &Property{Name: name},
			valueKeys: make(map[string]int),
		}
		builders[name] = b
		order = append(order, name)
		return b
	}

	var definitions map[string]config.PropertyDefinition
	if fm != nil {
		definitions = fm.Properties
	}
	for name, def := range definitions {
		b := ensure(name)
		b.prop.Configured = true
		declared := def
		b.prop.Definition = &declared
	}

	total := 0
	violationNotes := make(map[string]struct{})
	for _, note := range notes {
		if note == nil {
			continue
		}
		front, err := note.Front()
		if err != nil || front == nil {
			continue
		}
		total++

		rel := relPath(root, note)
		title := note.Title()
		folder := folderOf(rel)
		hash := note.SourceHash()

		issues := make(map[string][]Issue)
		for _, violation := range schema.ValidateNote(front, rel, definitions) {
			issues[violation.Property] = append(issues[violation.Property], Issue{
				Kind:   violation.Kind,
				Detail: violation.Detail,
			})
		}
		if len(issues) > 0 {
			violationNotes[rel] = struct{}{}
		}

		for _, key := range front.Keys() {
			value, ok := ClassifyProperty(front, key)
			if !ok {
				continue
			}
			b := ensure(key)
			b.addNote(NoteRow{
				RelPath: rel,
				Title:   title,
				Folder:  folder,
				Hash:    hash,
				Present: true,
				Value:   value,
				Issues:  issues[key],
			})
		}

		// A required property that the note omits still deserves a row so the
		// workbench can list exactly which notes need a value.
		for property, propertyIssues := range issues {
			if front.Has(property) {
				continue
			}
			b := ensure(property)
			b.addNote(NoteRow{
				RelPath: rel,
				Title:   title,
				Folder:  folder,
				Hash:    hash,
				Present: false,
				Issues:  propertyIssues,
			})
		}
	}

	index := &Index{
		TotalNotes: total,
		byName:     make(map[string]*Property, len(order)),
	}
	sort.Strings(order)
	for _, name := range order {
		b := builders[name]
		b.finish(total)
		index.Properties = append(index.Properties, b.prop)
		index.byName[name] = b.prop
	}

	index.Totals = Totals{
		Properties:     len(index.Properties),
		Notes:          total,
		ViolationNotes: len(violationNotes),
	}
	for _, prop := range index.Properties {
		if prop.Configured {
			index.Totals.Configured++
		} else {
			index.Totals.Unconfigured++
		}
		index.Totals.Violations += prop.ViolationCounts.Total()
		index.Totals.MissingRequired += prop.MissingRequired
		index.Totals.Unsupported += prop.UnsupportedValues
		if prop.Proposal != nil {
			index.Totals.Proposals++
		}
	}
	return index
}

func (b *builder) addNote(row NoteRow) {
	b.prop.Rows = append(b.prop.Rows, row)
	for _, issue := range row.Issues {
		switch issue.Kind {
		case "missing":
			b.prop.ViolationCounts.Missing++
		case "type":
			b.prop.ViolationCounts.Type++
		case "value":
			b.prop.ViolationCounts.Value++
		}
	}
	if len(row.Issues) > 0 {
		b.prop.ViolationNotes++
	}
	if !row.Present {
		b.prop.MissingRequired++
		return
	}

	b.prop.NoteCount++
	b.inference.observe(row.Value)

	// Each logical value counts at most once per note, so a note repeating a
	// list item does not inflate the catalog statistics.
	seen := make(map[string]struct{}, len(row.Value.Items)+1)
	unsupported := 0
	for _, logical := range row.Value.Logical() {
		key := logical.key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if !logical.Supported {
			unsupported++
		}
		if idx, ok := b.valueKeys[key]; ok {
			b.values[idx].Notes++
			continue
		}
		b.valueKeys[key] = len(b.values)
		b.values = append(b.values, ValueCount{Value: logical, Notes: 1})
	}
	if unsupported > 0 {
		b.prop.UnsupportedValues += unsupported
		b.prop.UnsupportedNotes++
	}
}

func (b *builder) finish(totalNotes int) {
	b.prop.TotalNotes = totalNotes
	b.prop.ObservedKinds = b.inference.observedKinds()
	b.prop.Values = b.values
	sort.SliceStable(b.prop.Values, func(i, j int) bool {
		if b.prop.Values[i].Notes != b.prop.Values[j].Notes {
			return b.prop.Values[i].Notes > b.prop.Values[j].Notes
		}
		return displayLess(b.prop.Values[i].Value.Text, b.prop.Values[j].Value.Text)
	})
	sort.SliceStable(b.prop.Rows, func(i, j int) bool {
		return b.prop.Rows[i].RelPath < b.prop.Rows[j].RelPath
	})
	if !b.prop.Configured {
		b.prop.Proposal = b.inference.proposal(b.prop.Name, b.prop.NoteCount)
	}
}

func relPath(root string, note *vault.Note) string {
	if note == nil || note.Path == "" {
		return ""
	}
	if root != "" {
		rel, err := filepath.Rel(root, note.Path)
		if err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(note.Path))
}

func folderOf(rel string) string {
	idx := strings.LastIndex(rel, "/")
	if idx < 0 {
		return ""
	}
	return rel[:idx]
}

func displayLess(left, right string) bool {
	leftFolded := strings.ToLower(left)
	rightFolded := strings.ToLower(right)
	if leftFolded != rightFolded {
		return leftFolded < rightFolded
	}
	return left < right
}
