package queryexpr

import (
	"errors"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

// predicateBuilder turns an already-validated term into a predicate once an Env
// carrying the graph is available.
//
// The split is what lets Compile reject a bad query without building a link
// graph: every check that depends only on the query text — the field name, the
// operator, the shape and syntax of the value — happens when the builder is
// produced, and only the parts that need vault state are deferred to the
// closure. Plan.NeedsGraph then tells a caller whether building that graph is
// worth it at all.
type predicateBuilder func(Env) (query.Predicate, error)

// reservedField is one built-in query field: a name, what vault state its
// predicate needs, and how to lower a term naming it.
//
// This mirrors the active/flag/build table in query.FromFlags
// (internal/query/parse.go:141-176) — the same "map a named filter onto a
// graph-taking builder" shape, extended with the operator and value checking a
// text surface needs and FromFlags does not.
type reservedField struct {
	name string
	// needsGraph marks a field whose predicate reads the link graph. It is the
	// per-field half of Plan.NeedsGraph, which must stay compatible with
	// findFlags.needsGraph in internal/cli/findflags.go.
	needsGraph bool
	// needsMetrics marks a field that also needs GraphMetrics. Plan.Predicate
	// derives metrics from the graph when a caller supplies only the graph.
	needsMetrics bool
	lower        func(t *Term, src string) (predicateBuilder, error)
	// lowerWithEnv is the variant for a field whose *validation* — not just its
	// evaluation — depends on vault configuration. Only prop needs it: which
	// comparison a property name lowers onto, and which operators are even
	// legal on it, follow from that property's declaration. Exactly one of
	// lower and lowerWithEnv is set.
	lowerWithEnv func(t *Term, src string, env Env) (predicateBuilder, error)
}

// build lowers a term naming this field, using whichever lowering the field
// declared.
func (f reservedField) build(t *Term, src string, env Env) (predicateBuilder, error) {
	if f.lowerWithEnv != nil {
		return f.lowerWithEnv(t, src, env)
	}
	return f.lower(t, src)
}

// reservedFields is the whole reserved vocabulary. Lookup is case-insensitive
// (the parser stores field names verbatim), and every name here is also a
// did-you-mean candidate for an unknown field.
//
// The six graph-traversal and structure fields are declared in graph_terms.go
// and merged in by withGraphFields: they need compile-time target resolution and
// a shared BFS memo that no other field has, and keeping their lowerings next to
// that machinery is what stops it leaking into every other field's table entry.
//
// Sort and limit are deliberately absent: they are not expression syntax. Plan
// carries them as flags a caller fills in, so `bn find -q '...' --sort
// relevance` keeps one spelling for an ordering that queryexpr never performs
// itself.
var reservedFields = withGraphFields(map[string]reservedField{
	"tag":      {name: "tag", lower: stringField("tag", query.Tag)},
	"category": {name: "category", lower: stringField("category", query.Category)},
	"status":   {name: "status", lower: stringField("status", query.Status)},
	"type":     {name: "type", lower: stringField("type", query.Type)},
	"has":      {name: "has", lower: stringField("has", query.Has)},
	"missing":  {name: "missing", lower: stringField("missing", query.Missing)},
	"folder":   {name: "folder", lower: lowerFolder},
	"age":      {name: "age", lower: lowerAge},
	"prop":     {name: "prop", lowerWithEnv: lowerProp},

	"pinned":       {name: "pinned", lower: boolField("pinned", func(Env) query.Predicate { return query.Pinned() })},
	"orphan":       {name: "orphan", needsGraph: true, lower: boolField("orphan", func(env Env) query.Predicate { return query.Orphan(env.Graph) })},
	"dead-end":     {name: "dead-end", needsGraph: true, lower: boolField("dead-end", func(env Env) query.Predicate { return query.DeadEnd(env.Graph) })},
	"broken-links": {name: "broken-links", needsGraph: true, lower: boolField("broken-links", func(env Env) query.Predicate { return query.BrokenLinks(env.Graph) })},

	"backlinks": {name: "backlinks", needsGraph: true, lower: countField("backlinks", query.MinBacklinks, query.MaxBacklinks)},
	"links":     {name: "links", needsGraph: true, lower: countField("links", query.MinLinks, query.MaxLinks)},
})

// lookupReserved resolves a field name to its reserved definition. Names are
// folded to lower case: the parser keeps what the user typed, so `Tag:foo` and
// `tag:foo` would otherwise be different fields.
func lookupReserved(name string) (reservedField, bool) {
	f, ok := reservedFields[strings.ToLower(strings.TrimSpace(name))]
	return f, ok
}

// reservedFieldNames returns every reserved name in sorted order.
func reservedFieldNames() []string {
	out := make([]string, 0, len(reservedFields))
	for name := range reservedFields {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// termBuilder validates one term and returns its builder.
//
// Field resolution runs in three steps: a reserved field wins outright; a name
// declared under [frontmatter.properties] in brainnotes.toml lowers onto the
// typed comparison in typed.go; anything else is an unknown field, reported
// with did-you-mean candidates drawn from both sets.
//
// A property with no declaration is deliberately *not* the third step. Letting
// any bare identifier become a property comparison would mean `backlnks>3`
// silently searched for a property named "backlnks" instead of suggesting
// "backlinks", which would cost the whole did-you-mean diagnostic. Undeclared
// properties stay reachable through the prop field instead — prop:backlnks>3
// says "I really do mean the frontmatter key" in as many characters as it takes
// to mean it.
func termBuilder(t *Term, src string, env Env) (predicateBuilder, error) {
	if t == nil {
		return func(Env) (query.Predicate, error) { return query.And(), nil }, nil
	}
	if t.Field == "" {
		return lowerText(t, src)
	}

	field, ok := lookupReserved(t.Field)
	if !ok {
		if def, declared := definitionFor(env, t.Field); declared {
			return propertyTerm(t, src, t.Field, def, true)
		}
		return nil, unknownField(t, src, env)
	}
	build, err := field.build(t, src, env)
	if err != nil {
		return nil, shadowedPropertyHint(err, field.name, env)
	}
	if !field.needsGraph {
		return build, nil
	}
	return func(env Env) (query.Predicate, error) {
		if env.Graph == nil {
			return nil, &MissingStateError{Field: field.name, Need: "link graph"}
		}
		if field.needsMetrics && env.Metrics == nil {
			return nil, &MissingStateError{Field: field.name, Need: "graph metrics"}
		}
		return build(env)
	}, nil
}

// shadowedPropertyHint extends a reserved field's lowering error when the vault
// also declares a property of the same name. The reserved field wins (see
// termBuilder), so `pinned>-1` in a vault declaring an int `pinned` reaches the
// boolean pinned field and fails there; without the hint the error reads as if
// the declared property did not exist. prop:<name> is the spelling that reaches
// the property, and it keeps the declared type.
func shadowedPropertyHint(err error, name string, env Env) error {
	var pe *ParseError
	if name == "prop" || !errors.As(err, &pe) {
		return err
	}
	if _, declared := definitionFor(env, name); !declared {
		return err
	}
	hinted := *pe
	hinted.Msg += "; the reserved field " + quote(name) + " takes precedence over the declared property of that name, so write prop:" + name + " to compare the property"
	return &hinted
}

// unknownField builds the ParseError for a field name nothing recognises,
// pointing the carets at the name itself rather than the whole term.
func unknownField(t *Term, src string, env Env) *ParseError {
	return NewParseError(src, fieldSpan(t), "unknown field "+quote(t.Field), suggestFields(t.Field, env))
}

// definitionFor looks name up among the vault's declared properties.
//
// The lookup is exact where reserved field names are case-folded, and the
// asymmetry is real rather than an oversight: `Tag:foo` and `tag:foo` are the
// same field, but `Status` and `status` are different frontmatter keys, so
// folding a property name would silently query one when the user meant the
// other. A nil Frontmatter means "no vault config", under which nothing is
// declared.
func definitionFor(env Env, name string) (config.PropertyDefinition, bool) {
	if env.Frontmatter == nil {
		return config.PropertyDefinition{}, false
	}
	return env.Frontmatter.Definition(name)
}

// lowerProp compares a frontmatter property named in the term's argument:
// prop:rating>3, or equivalently prop(rating)>3.
//
// It exists for two cases a bare `rating>3` cannot serve. An *undeclared*
// property has no declaration to distinguish it from a typo, so it is only
// reachable by saying so explicitly — CLAUDE.md keeps undeclared properties
// legal, and this is where they stay legal. And a property whose name collides
// with a reserved field (a vault that really does declare a `status` or a
// `type` key) is otherwise shadowed by the field; prop:type=note reads the raw
// frontmatter key instead.
//
// A declared property named here still takes the typed path, so the escape
// hatch never bypasses a declaration — it only lets you name the key.
//
// One shape is not spellable: `:` is not in the operator token set the argument
// form can be followed by, so the comparison is written prop:tags=go rather
// than prop:tags:go. Nothing is lost — for an undeclared property `:` and `=`
// are the same fold-equal comparison, and matching one element of a stored list
// does not depend on the operator.
func lowerProp(t *Term, src string, env Env) (predicateBuilder, error) {
	const name = "prop"
	if !t.HasArg {
		return nil, NewParseError(src, t.Span,
			"field "+quote(name)+" needs a property name, e.g. prop:rating>3", nil)
	}
	prop := strings.TrimSpace(t.Arg.Text)
	if prop == "" {
		return nil, NewParseError(src, t.Arg.Span,
			"field "+quote(name)+" needs a property name, e.g. prop:rating>3", nil)
	}
	def, declared := definitionFor(env, prop)
	return propertyTerm(withoutArg(t), src, prop, def, declared)
}

// withoutArg returns a copy of t with the argument removed, so the property
// lowering sees the same term shape a bare `rating>3` has and its diagnostics
// do not have to know which of the two spellings produced them.
func withoutArg(t *Term) *Term {
	clone := *t
	clone.HasArg = false
	clone.Arg = Atom{}
	return &clone
}

// fieldSpan narrows a term's span to its field name. Field text is
// NFC-normalized while the span is raw bytes, so the end is clamped to the
// term's own span rather than trusted blindly.
func fieldSpan(t *Term) Span {
	span := Span{Start: t.Span.Start, End: t.Span.Start + len(t.Field)}
	if span.End > t.Span.End {
		span.End = t.Span.End
	}
	return span
}
