package queryexpr

import (
	"strconv"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

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
