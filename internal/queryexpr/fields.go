package queryexpr

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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
		return nil, err
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

// suggestFields returns up to three did-you-mean candidates for an unknown
// field, drawn from the reserved names and the vault's declared frontmatter
// properties. Candidates are within Damerau-Levenshtein distance 2 and ordered
// by distance then name, so the list is deterministic for a given vault.
func suggestFields(name string, env Env) []string {
	const maxDistance = 2
	const maxSuggestions = 3

	candidates := reservedFieldNames()
	if env.Frontmatter != nil {
		candidates = append(candidates, env.Frontmatter.PropertyNames()...)
	}

	want := strings.ToLower(strings.TrimSpace(name))
	type scored struct {
		name string
		dist int
	}
	var hits []scored
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		dist := damerauLevenshtein(want, strings.ToLower(candidate))
		if dist <= maxDistance {
			hits = append(hits, scored{name: candidate, dist: dist})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > maxSuggestions {
		hits = hits[:maxSuggestions]
	}
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.name)
	}
	return out
}

// damerauLevenshtein returns the optimal string alignment distance between a
// and b, counting a transposition of two adjacent runes as one edit.
//
// It is spelled out here rather than pulled in: go.mod has nine small direct
// dependencies and a fifty-line matrix over a pair of short field names is not
// worth a tenth. Comparison is over runes, so an accented property name costs
// one edit rather than two.
func damerauLevenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	rows := make([][]int, len(ar)+1)
	for i := range rows {
		rows[i] = make([]int, len(br)+1)
		rows[i][0] = i
	}
	for j := 0; j <= len(br); j++ {
		rows[0][j] = j
	}
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			best := minInt(rows[i-1][j]+1, rows[i][j-1]+1)
			best = minInt(best, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				best = minInt(best, rows[i-2][j-2]+1)
			}
			rows[i][j] = best
		}
	}
	return rows[len(ar)][len(br)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// lowerText lowers a bare word or quoted phrase onto query.Text. The needle is
// folded once, at compile time, which is exactly what query.Text's pre-folded
// contract asks for.
//
// A wikilink written as a bare term (`[[Deep Work]]` with no field) searches
// for its text; use links-to: to follow the edge instead.
func lowerText(t *Term, src string) (predicateBuilder, error) {
	if t.Value.Kind != ValueAtom || len(t.Value.Atoms) == 0 {
		return nil, NewParseError(src, t.Span, "expected a word, phrase, or field term", nil)
	}
	needle := query.FoldText(t.Value.Atoms[0].Text)
	pred := query.Text(needle)
	return func(Env) (query.Predicate, error) { return pred, nil }, nil
}

// stringField lowers a term onto a builder taking one string, treating `:` and
// `=` alike (every one of these builders already compares case-insensitively)
// and `!=` as the negation. A list value is any-of, so `tag:[a,b]` is
// `tag:a or tag:b`.
func stringField(name string, build func(string) query.Predicate) func(*Term, string) (predicateBuilder, error) {
	return func(t *Term, src string) (predicateBuilder, error) {
		atoms, negated, err := matchAtoms(t, src, name)
		if err != nil {
			return nil, err
		}
		ps := make([]query.Predicate, 0, len(atoms))
		for _, atom := range atoms {
			ps = append(ps, build(atom.Text))
		}
		pred := anyOf(ps, negated)
		return func(Env) (query.Predicate, error) { return pred, nil }, nil
	}
}

// lowerFolder is stringField for query.Folder, which additionally needs the
// vault root — and so is the one string field whose predicate cannot be built
// until Env arrives.
func lowerFolder(t *Term, src string) (predicateBuilder, error) {
	atoms, negated, err := matchAtoms(t, src, "folder")
	if err != nil {
		return nil, err
	}
	return func(env Env) (query.Predicate, error) {
		if env.Root == "" {
			return nil, &MissingStateError{Field: "folder", Need: "vault root path"}
		}
		ps := make([]query.Predicate, 0, len(atoms))
		for _, atom := range atoms {
			ps = append(ps, query.Folder(atom.Text, env.Root))
		}
		return anyOf(ps, negated), nil
	}, nil
}

// boolField lowers a flag-shaped field. A bare `orphan` is a text term by the
// grammar — a word with no field is always free text — so the spelling is
// `orphan:true`, with `orphan:false` (and `orphan!=true`) as its negation.
func boolField(name string, build func(Env) query.Predicate) func(*Term, string) (predicateBuilder, error) {
	return func(t *Term, src string) (predicateBuilder, error) {
		atoms, negated, err := matchAtoms(t, src, name)
		if err != nil {
			return nil, err
		}
		if len(atoms) != 1 {
			return nil, NewParseError(src, t.Value.Span, "field "+quote(name)+" takes a single true or false, not a list", nil)
		}
		switch strings.ToLower(atoms[0].Text) {
		case "true":
		case "false":
			negated = !negated
		default:
			return nil, NewParseError(src, atoms[0].Span, "field "+quote(name)+" expects true or false, got "+quote(atoms[0].Text), nil)
		}
		return func(env Env) (query.Predicate, error) {
			pred := build(env)
			if negated {
				pred = negate(pred)
			}
			return pred, nil
		}, nil
	}
}

// countField lowers a link-count comparison onto the Min/Max builder pair.
//
// `>` and `<` are strict, so backlinks>3 becomes MinBacklinks(g, 4) and
// backlinks<3 becomes MaxBacklinks(g, 2). Bounds that fall outside the useful
// range collapse to a constant rather than reaching the builder: query.Flags
// reads -1 as "filter disabled" (internal/query/parse.go:14-19), and while the
// builders themselves have no sentinel, keeping -1 out of them means the two
// lowering paths can never disagree about what a bound of -1 means.
func countField(name string, min, max func(*query.Graph, int) query.Predicate) func(*Term, string) (predicateBuilder, error) {
	return func(t *Term, src string) (predicateBuilder, error) {
		op, want, err := comparison(t, src, name)
		if err != nil {
			return nil, err
		}
		atLeast := func(g *query.Graph, n int) query.Predicate {
			if n <= 0 {
				return query.And()
			}
			return min(g, n)
		}
		atMost := func(g *query.Graph, n int) query.Predicate {
			if n < 0 {
				return matchNothing
			}
			return max(g, n)
		}
		var build func(*query.Graph) query.Predicate
		switch op {
		case OpGt:
			build = func(g *query.Graph) query.Predicate { return atLeast(g, want+1) }
		case OpGe:
			build = func(g *query.Graph) query.Predicate { return atLeast(g, want) }
		case OpLt:
			build = func(g *query.Graph) query.Predicate { return atMost(g, want-1) }
		case OpLe:
			build = func(g *query.Graph) query.Predicate { return atMost(g, want) }
		case OpEq, OpMatch:
			build = func(g *query.Graph) query.Predicate { return query.And(atLeast(g, want), atMost(g, want)) }
		case OpNe:
			build = func(g *query.Graph) query.Predicate { return negate(query.And(atLeast(g, want), atMost(g, want))) }
		}
		return func(env Env) (query.Predicate, error) { return build(env.Graph), nil }, nil
	}
}

// lowerAge lowers age onto query.Age through query.ParseAge, which wants the
// operator glued to the value as one string and accepts only >, <, and =. The
// optional argument names the frontmatter property to read, defaulting to
// `last` — the same property `bn find --age` uses, with `age:created>90d` the
// equivalent of --created.
func lowerAge(t *Term, src string) (predicateBuilder, error) {
	const name = "age"
	prop := "last"
	if t.HasArg {
		if strings.TrimSpace(t.Arg.Text) == "" {
			return nil, NewParseError(src, t.Arg.Span, "field "+quote(name)+" needs a property name in its argument, e.g. age:created>90d", nil)
		}
		prop = t.Arg.Text
	}
	switch t.Op {
	case OpGt, OpLt, OpEq:
	default:
		return nil, NewParseError(src, t.Span, "operator "+quote(string(t.Op))+" is not defined for field "+quote(name)+"; use \">\", \"<\", or \"=\"", nil)
	}
	if t.Value.Kind != ValueAtom || len(t.Value.Atoms) != 1 {
		return nil, NewParseError(src, t.Value.Span, "field "+quote(name)+" takes a single duration, e.g. age>90d", nil)
	}
	op, dur, err := query.ParseAge(string(t.Op) + t.Value.Atoms[0].Text)
	if err != nil {
		return nil, NewParseError(src, t.Value.Atoms[0].Span, err.Error(), nil)
	}
	pred := query.Age(op, dur, prop)
	return func(Env) (query.Predicate, error) { return pred, nil }, nil
}

// matchAtoms validates a term whose value is compared for equality — `:`, `=`,
// or `!=` — and returns its atoms plus whether the result must be negated.
func matchAtoms(t *Term, src, name string) ([]Atom, bool, error) {
	if t.HasArg {
		return nil, false, NewParseError(src, t.Arg.Span, "field "+quote(name)+" takes no argument", nil)
	}
	switch t.Op {
	case OpMatch, OpEq, OpNe:
	default:
		return nil, false, operatorError(t, src, name, "\":\", \"=\", or \"!=\"")
	}
	if t.Value.Kind == ValueNone || len(t.Value.Atoms) == 0 {
		return nil, false, NewParseError(src, t.Span, "field "+quote(name)+" needs a value, e.g. "+name+":something", nil)
	}
	return t.Value.Atoms, t.Op == OpNe, nil
}

// comparison validates a numeric term and returns its operator and value.
func comparison(t *Term, src, name string) (Op, int, error) {
	if t.HasArg {
		return OpNone, 0, NewParseError(src, t.Arg.Span, "field "+quote(name)+" takes no argument", nil)
	}
	switch t.Op {
	case OpMatch, OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
	default:
		return OpNone, 0, operatorError(t, src, name, "a comparison such as \">=\" or \"<\"")
	}
	n, err := singleInt(t, src, name)
	if err != nil {
		return OpNone, 0, err
	}
	return t.Op, n, nil
}

// singleInt reads a term's value as one non-negative whole number.
func singleInt(t *Term, src, name string) (int, error) {
	if t.Value.Kind != ValueAtom || len(t.Value.Atoms) != 1 {
		return 0, NewParseError(src, valueSpan(t), "field "+quote(name)+" takes a single whole number", nil)
	}
	atom := t.Value.Atoms[0]
	n, err := strconv.Atoi(atom.Text)
	if err != nil {
		return 0, NewParseError(src, atom.Span, "field "+quote(name)+" expects a whole number, got "+quote(atom.Text), nil)
	}
	if n < 0 {
		return 0, NewParseError(src, atom.Span, "field "+quote(name)+" expects a number that is not negative, got "+quote(atom.Text), nil)
	}
	return n, nil
}

func operatorError(t *Term, src, name, allowed string) *ParseError {
	if t.Op == OpNone {
		return NewParseError(src, t.Span, "field "+quote(name)+" needs a value, e.g. "+name+":something", nil)
	}
	return NewParseError(src, t.Span, "operator "+quote(string(t.Op))+" is not defined for field "+quote(name)+"; use "+allowed, nil)
}

// valueSpan reports the span to point at for a value-shaped complaint, falling
// back to the whole term when the term carried no value at all.
func valueSpan(t *Term) Span {
	if t.Value.Kind == ValueNone {
		return t.Span
	}
	return t.Value.Span
}

// anyOf combines the per-atom predicates of a list value, optionally negated.
// One atom is the common case and stays a bare predicate rather than a
// single-element Or.
func anyOf(ps []query.Predicate, negated bool) query.Predicate {
	var pred query.Predicate
	switch len(ps) {
	case 0:
		pred = matchNothing
	case 1:
		pred = ps[0]
	default:
		pred = query.Or(ps...)
	}
	if negated {
		pred = negate(pred)
	}
	return pred
}

// negate inverts a predicate, keeping query's convention that a nil note
// matches nothing — so `not tag:foo` does not start matching nil notes.
func negate(p query.Predicate) query.Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		return !p(n)
	}
}

// matchNothing is the empty result, the dual of query.And() with no arguments.
func matchNothing(*vault.Note) bool { return false }
