package web

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/properties"
	"github.com/sglavoie/brainnotes/internal/queryexpr"
)

// queryValueSource supplies the candidate values a field can take. Every source
// is built on first use: a caret in a field position needs no facet counts, and
// a caret after `tag:` needs no property index, so a keystroke pays only for
// what it asks.
type queryValueSource struct {
	server   *Server
	snapshot vaultSnapshot

	facets    *facetsResponse
	propIndex *properties.Index
}

func (s *queryValueSource) facetOptions() *facetsResponse {
	if s.facets == nil {
		payload, err := buildFacetsResponse(s.snapshot, s.server.vault.Root)
		if err != nil {
			payload = facetsResponse{}
		}
		s.facets = &payload
	}
	return s.facets
}

func (s *queryValueSource) propertyIndex() *properties.Index {
	if s.propIndex == nil {
		cfg, status := s.server.configState.publication()
		s.propIndex = s.server.state.propertyIndexFor(s.snapshot, &cfg.Frontmatter, status.Generation)
	}
	return s.propIndex
}

func (s *queryValueSource) frontmatter() config.FrontmatterConfig {
	return s.server.config().Frontmatter
}

// candidate is one unfiltered suggestion, before prefix matching and ranking.
type candidate struct {
	value    string
	kind     string
	detail   string
	count    int
	wikilink bool
}

func (s *queryValueSource) completionsFor(c queryexpr.Completion) []queryCompletion {
	var candidates []candidate
	if c.Kind == queryexpr.CompleteField {
		candidates = s.fieldCandidates()
	} else {
		candidates = s.valueCandidates(c)
	}
	return rankQueryCompletions(candidates, c)
}

// fieldCandidates is the whole query vocabulary: the reserved fields plus every
// property declared in brainnotes.toml, which is exactly the set a bare name
// resolves against.
func (s *queryValueSource) fieldCandidates() []candidate {
	fields := queryexpr.Fields()
	out := make([]candidate, 0, len(fields))
	for _, field := range fields {
		out = append(out, candidate{value: field.Name, kind: "field", detail: field.Hint})
	}

	fm := s.frontmatter()
	for _, name := range fm.PropertyNames() {
		if queryexpr.IsReservedField(name) {
			// A reserved name wins outright, so the bare spelling would not
			// reach the property; prop:<name> is how to say it.
			continue
		}
		detail := ""
		if def, ok := fm.Definition(name); ok {
			detail = def.Summary()
		}
		out = append(out, candidate{value: name, kind: "property", detail: detail})
	}
	return out
}

func (s *queryValueSource) valueCandidates(c queryexpr.Completion) []candidate {
	switch c.Field {
	case "tag":
		return facetCandidates(s.facetOptions().Tags, "tag")
	case "category":
		return facetCandidates(s.facetOptions().Categories, "category")
	case "status":
		return facetCandidates(s.facetOptions().Status, "status")
	case "type":
		return facetCandidates(s.facetOptions().Types, "type")
	case "folder":
		return facetCandidates(s.facetOptions().Folders, "folder")

	case "has", "missing":
		return s.propertyNameCandidates()
	case "prop":
		// prop takes the property name as its argument, so the caret is on the
		// name until one has been typed and on that property's values after.
		if c.Arg == "" {
			return s.propertyNameCandidates()
		}
		return s.propertyValueCandidates(c.Arg)
	case "age":
		if c.Arg == "" {
			return s.datePropertyCandidates()
		}
		return nil

	case "pinned", "orphan", "dead-end", "broken-links":
		return []candidate{
			{value: "true", kind: "boolean"},
			{value: "false", kind: "boolean"},
		}

	case "links-to", "linked-from", "near", "path-to":
		// A hop bound follows the target, and no vault data can suggest one.
		if c.Arg != "" {
			return nil
		}
		return s.noteTitleCandidates()

	case "backlinks", "links", "degree", "cluster-size":
		return nil
	}

	// A declared property is spelled bare, so its values are what follows it.
	if _, ok := s.frontmatter().Definition(c.Field); ok {
		return s.propertyValueCandidates(c.Field)
	}
	return nil
}

func (s *queryValueSource) propertyNameCandidates() []candidate {
	index := s.propertyIndex()
	if index == nil {
		return nil
	}
	out := make([]candidate, 0, len(index.Properties))
	for _, prop := range index.Properties {
		if prop == nil {
			continue
		}
		detail := ""
		if prop.Definition != nil {
			detail = prop.Definition.Summary()
		}
		out = append(out, candidate{value: prop.Name, kind: "property", detail: detail, count: prop.NoteCount})
	}
	return out
}

// datePropertyCandidates lists the declared date properties age can be taken
// relative to. age with no argument reads the note's last date, so an empty
// list here just means the vault declares no date property to name.
func (s *queryValueSource) datePropertyCandidates() []candidate {
	fm := s.frontmatter()
	var out []candidate
	for _, name := range fm.PropertyNames() {
		def, ok := fm.Definition(name)
		if !ok {
			continue
		}
		kind := def.ScalarKind()
		if kind != config.KindDate && kind != config.KindDatetime {
			continue
		}
		out = append(out, candidate{value: name, kind: "property", detail: def.Summary()})
	}
	return out
}

func (s *queryValueSource) propertyValueCandidates(name string) []candidate {
	index := s.propertyIndex()
	if index == nil {
		return nil
	}
	prop, ok := index.Property(name)
	if !ok {
		return nil
	}

	out := make([]candidate, 0, len(prop.Values))
	for _, value := range prop.Values {
		// A list value has no single spelling to compare against; its elements
		// are what a term matches, and Logical flattens to exactly those.
		for _, logical := range value.Value.Logical() {
			text := strings.TrimSpace(logical.Text)
			if text == "" {
				continue
			}
			out = append(out, candidate{value: text, kind: "value", count: value.Notes})
		}
	}
	return dedupeCandidates(out)
}

// noteTitleCandidates lists every note title the graph knows, which is the set
// a wikilink target resolves against.
func (s *queryValueSource) noteTitleCandidates() []candidate {
	if s.snapshot.graph == nil {
		return nil
	}
	out := make([]candidate, 0, len(s.snapshot.graph.OriginalTitles))
	for _, title := range s.snapshot.graph.OriginalTitles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		out = append(out, candidate{value: title, kind: "note", wikilink: true})
	}
	return dedupeCandidates(out)
}

func facetCandidates(options []facetOption, kind string) []candidate {
	out := make([]candidate, 0, len(options))
	for _, option := range options {
		out = append(out, candidate{value: option.Value, kind: kind, count: option.Count})
	}
	return out
}

// dedupeCandidates collapses values that differ only by case, keeping the
// highest count and the display spelling the facet counters would prefer.
func dedupeCandidates(in []candidate) []candidate {
	seen := make(map[string]int, len(in))
	out := make([]candidate, 0, len(in))
	for _, c := range in {
		key := foldQueryValue(c.value)
		if i, ok := seen[key]; ok {
			out[i].count += c.count
			if facetDisplayLess(c.value, out[i].value) {
				out[i].value = c.value
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, c)
	}
	return out
}
