package queryexpr

import (
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/schema"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// Typed frontmatter comparison.
//
// Every decision here is driven by cfg.Frontmatter.Definition(name) — the
// declared PropertyDefinition — and never by the property's name. That is
// CLAUDE.md's config-driven model: a vault that declares `rating` as an int
// gets numeric ordering on it, and a vault that declares it as a string gets a
// compile error for `rating>3`, with nothing in this package knowing what
// "rating" means.
//
// Kind checking and coercion are internal/schema's job: CheckScalar classifies
// a stored value against a kind and TryCoerce salvages one written in another
// spelling (the same rules `bn set` applies). This file adds only the two
// things schema has no opinion about — parsing a *literal* out of query text,
// and applying a comparison *operator* to the pair.
//
// The operator legality table, keyed off config's kind constants so a new
// PropertyKind cannot silently fall through:
//
//	string     : = !=        fold-equal via query.FoldText
//	wikilink   : = !=        query.WikilinkTitleKey normalization
//	bool       : = !=        boolean equality
//	date       all           calendar-day compare; YYYY-MM-DD, today, "+30d", "-7d"
//	datetime   all           instant compare; now, RFC3339, relative
//	int float  all           numeric; a float literal on an int property is an error
//	list       :             contains; elements parsed as def.Items, any-element match
//
// `allowed` lists are deliberately NOT enforced on a query. You have to be able
// to find the notes that violate your own schema, so a query for a value
// outside the allow-list compiles and runs — it is `bn doctor`, not `bn find`,
// that reports the violation.

// comparer answers one comparison against a single stored frontmatter value.
type comparer func(stored any) bool

// comparerFor is a comparer that is not yet pinned to a clock. Splitting it
// this way is what lets a relative literal such as "-7d" be *syntax*-checked at
// compile time and *resolved* once per predicate build, rather than once per
// note scanned.
type comparerFor func(now time.Time) comparer

// constant lifts a clock-independent comparer into a comparerFor.
func constant(c comparer) comparerFor {
	return func(time.Time) comparer { return c }
}

// nowFrom pins the clock for relative literals. This is Env.Now's first
// consumer: query.Age still reads time.Now itself, so only the literals in this
// file honour a caller-supplied clock today.
func nowFrom(env Env) time.Time {
	if env.Now != nil {
		return env.Now()
	}
	return time.Now()
}

// propertyTerm lowers a term naming a frontmatter property onto a comparison
// against that property's stored value.
//
// declared says whether def came from [frontmatter.properties]. A declared
// property gets the typed path — its operator set and its literal grammar both
// follow from def. An undeclared property stays legal (CLAUDE.md requires
// undeclared properties to remain usable) but falls back to the untyped path,
// which compares text the way query.Tag and query.Status already do.
func propertyTerm(t *Term, src, name string, def config.PropertyDefinition, declared bool) (predicateBuilder, error) {
	if t.HasArg {
		return nil, NewParseError(src, t.Arg.Span, "property "+quote(name)+" takes no argument", nil)
	}
	if t.Op == OpNone || t.Value.Kind == ValueNone || len(t.Value.Atoms) == 0 {
		return nil, NewParseError(src, t.Span, "property "+quote(name)+" needs a value, e.g. "+name+":something", nil)
	}

	// `!=` is the negation of `=`, applied to the whole predicate rather than
	// inside the comparer. That keeps it reading the same way tag!=alpha
	// already does: a note that has no value at all for the property matches.
	op := t.Op
	negated := op == OpNe
	if negated {
		op = OpEq
	}

	var factories []comparerFor
	var err error
	if declared {
		factories, err = typedComparers(t, src, name, def, op)
	} else {
		factories, err = untypedComparers(t, src, name, op)
	}
	if err != nil {
		return nil, err
	}

	return func(env Env) (query.Predicate, error) {
		now := nowFrom(env)
		cmps := make([]comparer, 0, len(factories))
		for _, factory := range factories {
			cmps = append(cmps, factory(now))
		}
		pred := propertyPredicate(name, cmps)
		if negated {
			pred = negate(pred)
		}
		return pred, nil
	}, nil
}

// propertyPredicate matches a note whose value at name satisfies any comparer.
// A list value in the query is any-of, and a list value in the note is
// any-element, so `topics:[go, rust]` against `topics: [rust, web]` matches.
//
// A note carrying no value at name never matches, which is what makes `!=`
// match it once the negation wraps this predicate.
func propertyPredicate(name string, cmps []comparer) query.Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		fm, err := n.Front()
		if err != nil {
			return false
		}
		raw, ok := fm.Get(name)
		if !ok {
			return false
		}
		for _, item := range listElements(raw) {
			for _, cmp := range cmps {
				if cmp(item) {
					return true
				}
			}
		}
		return false
	}
}

// listElements flattens a stored value into the elements a comparison runs
// against: a YAML list yields its items and any other value yields itself.
//
// Flattening unconditionally rather than only for a declared list is
// deliberate. A scalar stored where a list was declared — or a list stored
// where a scalar was — is a schema violation `bn doctor` reports, and a query
// that silently matched nothing would hide exactly the note the user is looking
// for.
func listElements(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []string:
		out := make([]any, len(list))
		for i, s := range list {
			out[i] = s
		}
		return out
	}
	return []any{v}
}

// typedComparers builds the per-atom comparers for a declared property, after
// checking that the operator is defined for its declared type.
func typedComparers(t *Term, src, name string, def config.PropertyDefinition, op Op) ([]comparerFor, error) {
	kind := def.ScalarKind()
	if !config.ValidScalarKind(kind) {
		return nil, NewParseError(src, fieldSpan(t),
			"property "+quote(name)+" has an unsupported declared type "+quote(schema.KindLabel(def))+" in brainnotes.toml", nil)
	}
	if !opAllowed(def, t.Op) {
		return nil, typedOpError(t, src, name, def)
	}
	if isOrderingOp(op) && (t.Value.Kind != ValueAtom || len(t.Value.Atoms) != 1) {
		return nil, NewParseError(src, t.Value.Span,
			"property "+quote(name)+" takes a single value with "+quote(string(t.Op))+", not a list", nil)
	}

	out := make([]comparerFor, 0, len(t.Value.Atoms))
	for _, atom := range t.Value.Atoms {
		factory, err := typedComparer(kind, op, atom, src, name)
		if err != nil {
			return nil, err
		}
		out = append(out, factory)
	}
	return out, nil
}

// opAllowed is the operator legality table. It switches on config's kind
// constants rather than string literals, so adding a PropertyKind without
// deciding its operator set fails closed instead of inheriting one.
func opAllowed(def config.PropertyDefinition, op Op) bool {
	switch op {
	case OpMatch:
		// `:` is defined for every kind: contains for a list, equality
		// otherwise.
		return true
	case OpEq, OpNe:
		// A list is contains-only; `tags=go` would read as "the list is
		// exactly [go]", which is not what it would do.
		return !def.IsList()
	case OpLt, OpLe, OpGt, OpGe:
		return !def.IsList() && kindIsOrdered(def.ScalarKind())
	}
	return false
}

// kindIsOrdered reports whether a scalar kind has a total order to compare on.
// string, wikilink, and bool deliberately do not: lexicographic string ordering
// is almost never what someone means by `status>wip`.
func kindIsOrdered(kind config.PropertyKind) bool {
	switch kind {
	case config.KindDate, config.KindDatetime, config.KindInt, config.KindFloat:
		return true
	}
	return false
}

// typedOpError reports an operator that the property's declared type does not
// define, naming the operator, the property, and the declaration to change:
//
//	operator ">" is not defined for string property "rating" (declared type = "string" in brainnotes.toml)
func typedOpError(t *Term, src, name string, def config.PropertyDefinition) *ParseError {
	return NewParseError(src, t.Span,
		"operator "+quote(string(t.Op))+" is not defined for "+string(def.Type)+" property "+quote(name)+
			" (declared type = "+quote(schema.KindLabel(def))+" in brainnotes.toml)", nil)
}

// typedComparer parses one literal against the element kind the property stores
// and returns the comparison it induces under op.
func typedComparer(kind config.PropertyKind, op Op, atom Atom, src, name string) (comparerFor, error) {
	switch kind {
	case config.KindString:
		want := query.FoldText(atom.Text)
		return constant(func(stored any) bool {
			s, ok := storedString(stored)
			return ok && query.FoldText(s) == want
		}), nil

	case config.KindWikilink:
		want := wikilinkKey(atom.Text)
		return constant(func(stored any) bool {
			s, ok := storedString(stored)
			return ok && wikilinkKey(s) == want
		}), nil

	case config.KindBool:
		want, ok := parseBoolLiteral(atom.Text)
		if !ok {
			return nil, NewParseError(src, atom.Span,
				"property "+quote(name)+" is declared bool, so it expects true or false, got "+quote(atom.Text), nil)
		}
		return constant(func(stored any) bool {
			b, ok := storedBool(stored)
			return ok && b == want
		}), nil

	case config.KindInt:
		n, err := parseIntLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		want := float64(n)
		return constant(func(stored any) bool {
			got, ok := storedNumber(stored)
			return ok && compareOrder(op, signFloat(got, want))
		}), nil

	case config.KindFloat:
		want, err := parseFloatLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		return constant(func(stored any) bool {
			got, ok := storedNumber(stored)
			return ok && compareOrder(op, signFloat(got, want))
		}), nil

	case config.KindDate:
		resolve, err := parseDateLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		return func(now time.Time) comparer {
			want := dayOrdinal(resolve(now))
			loc := now.Location()
			return func(stored any) bool {
				ts, ok := storedTime(stored)
				if !ok {
					return false
				}
				return compareOrder(op, signInt(dayOrdinal(query.LocalDate(ts, loc)), want))
			}
		}, nil

	case config.KindDatetime:
		resolve, err := parseDatetimeLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		return func(now time.Time) comparer {
			want := resolve(now)
			return func(stored any) bool {
				ts, ok := storedTime(stored)
				return ok && compareOrder(op, signTime(ts, want))
			}
		}, nil
	}
	return nil, NewParseError(src, atom.Span,
		"property "+quote(name)+" has an unsupported declared type in brainnotes.toml", nil)
}
