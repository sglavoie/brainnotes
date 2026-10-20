package queryexpr

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

// FromFlags lowers the flag-shaped filter set behind `bn find` onto an
// expression, so a query typed at the prompt and a query assembled from flags
// meet in one AST rather than in two independently-maintained predicate
// builders.
//
// It is deliberately a *second* implementation of query.FromFlags
// (internal/query/parse.go:49) rather than a replacement for it. internal/query
// cannot import internal/queryexpr — queryexpr imports query, so the reverse
// edge is a genuine cycle — and query.FromFlags has eight callers (six
// flag-scoped commands through findFlags.toQueryFlags in
// internal/cli/findflags.go, plus lint/graph and internal/web/filters.go) with a
// test surface that predates this package. The accepted cost is two lowerings
// that can drift; flags_equivalence_test.go is the guard, asserting that both
// select byte-identical sorted path lists across the flag matrix. If that test
// ever becomes hard to keep green, that is the signal to merge the two packages
// and accept the config import, not to loosen the test.
//
// The returned expression is always an *And, mirroring query.And(ps...): a
// Flags with nothing active yields a zero-operand And, which is the identity
// that matches every note and renders as the empty string.
//
// Two divergences from query.FromFlags are known and deliberate:
//
//   - Error text differs (the "queryexpr:" prefix, and the flag-name spellings
//     are the same but the wrapping is this package's). Selection is what the
//     equivalence guard pins; both paths reject exactly the same Flags values.
//   - Graph and root requirements surface later. query.FromFlags refuses a
//     graph-reading flag with Flags.Graph nil; here the expression compiles and
//     the failure is a *MissingStateError from Plan.Predicate, because the
//     graph arrives through Env rather than through Flags. Both paths still
//     fail — just at a different call.
//
// One further divergence is inherent to going through text: an atom is
// NFC-normalized by the lexer, while query.Tag and friends compare with
// strings.ToLower alone. A flag value typed in decomposed form therefore matches
// composed frontmatter through this path and not through query.FromFlags. The
// expression path is the more correct of the two; the equivalence table stays
// clear of decomposed values rather than pinning the discrepancy.
func FromFlags(f query.Flags) (Expr, error) {
	if err := validateFlagBounds(f); err != nil {
		return nil, err
	}

	var ops []Expr

	// The emission order mirrors query.FromFlags's own table order. Order is
	// immaterial to an And, but keeping the two sequences aligned is what makes
	// a drift between them visible in a side-by-side review.
	for _, spec := range []struct {
		field  string
		values []string
	}{
		{field: "tag", values: f.Tags},
		{field: "category", values: f.Categories},
		{field: "has", values: f.HasAll},
		{field: "missing", values: f.MissingAll},
	} {
		for _, value := range spec.values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			ops = append(ops, matchTerm(spec.field, value))
		}
	}

	for _, spec := range []struct {
		field string
		value string
	}{
		{field: "status", value: f.Status},
		{field: "type", value: f.Type},
		{field: "has", value: f.Has},
		{field: "missing", value: f.Missing},
	} {
		value := strings.TrimSpace(spec.value)
		if value == "" {
			continue
		}
		ops = append(ops, matchTerm(spec.field, value))
	}

	for _, spec := range []struct {
		flag string
		prop string
		expr string
	}{
		// The empty prop is age's own default of "last", so --age renders as
		// `age>90d` rather than `age:last>90d` — the spelling a user types.
		{flag: "--age", prop: "", expr: f.Age},
		{flag: "--created", prop: "created", expr: f.Created},
	} {
		value := strings.TrimSpace(spec.expr)
		if value == "" {
			continue
		}
		term, err := ageTerm(spec.flag, spec.prop, value)
		if err != nil {
			return nil, err
		}
		ops = append(ops, term)
	}

	if f.Pinned {
		ops = append(ops, matchTerm("pinned", "true"))
	}

	if folder := strings.TrimSpace(f.Folder); folder != "" {
		ops = append(ops, matchTerm("folder", folder))
	}

	// The activation rules below are asymmetric and load-bearing: a minimum of
	// 0 filters nothing, so it stays inactive, while a maximum of 0 is the
	// meaningful "no links at all" filter and must activate. -1 is the
	// "not set" sentinel for both (internal/query/parse.go:14-19), and it falls
	// out of the same two predicates.
	for _, spec := range []struct {
		active bool
		field  string
		op     Op
		bound  int
	}{
		{active: f.Orphan, field: "orphan"},
		{active: f.BrokenLinks, field: "broken-links"},
		{active: f.MinBacklinks > 0, field: "backlinks", op: OpGe, bound: f.MinBacklinks},
		{active: f.MaxBacklinks >= 0, field: "backlinks", op: OpLe, bound: f.MaxBacklinks},
		{active: f.MinLinks > 0, field: "links", op: OpGe, bound: f.MinLinks},
		{active: f.MaxLinks >= 0, field: "links", op: OpLe, bound: f.MaxLinks},
		{active: f.DeadEnd, field: "dead-end"},
	} {
		if !spec.active {
			continue
		}
		if spec.op == OpNone {
			ops = append(ops, matchTerm(spec.field, "true"))
			continue
		}
		ops = append(ops, countTerm(spec.field, spec.op, spec.bound))
	}

	return &And{Operands: ops}, nil
}

// validateFlagBounds rejects the same Flags values query.FromFlags rejects
// before it builds anything: a link bound below the -1 sentinel, and a minimum
// above its own maximum.
func validateFlagBounds(f query.Flags) error {
	for _, spec := range []struct {
		flag  string
		value int
	}{
		{flag: "--min-backlinks", value: f.MinBacklinks},
		{flag: "--max-backlinks", value: f.MaxBacklinks},
		{flag: "--min-links", value: f.MinLinks},
		{flag: "--max-links", value: f.MaxLinks},
	} {
		if spec.value < -1 {
			return fmt.Errorf("%s must be 0 or more, or -1 to disable (got %d)", spec.flag, spec.value)
		}
	}
	for _, spec := range []struct {
		minFlag string
		min     int
		maxFlag string
		max     int
	}{
		{minFlag: "--min-backlinks", min: f.MinBacklinks, maxFlag: "--max-backlinks", max: f.MaxBacklinks},
		{minFlag: "--min-links", min: f.MinLinks, maxFlag: "--max-links", max: f.MaxLinks},
	} {
		if spec.min >= 0 && spec.max >= 0 && spec.min > spec.max {
			return fmt.Errorf("%s (%d) cannot exceed %s (%d)", spec.minFlag, spec.min, spec.maxFlag, spec.max)
		}
	}
	return nil
}

// ageTerm splits a glued age expression like ">90d" into the operator and value
// halves a Term carries. query.ParseAge validates it first, so a bad expression
// is rejected here with the same wording query.FromFlags rejects it with — and
// so the split below can trust that the first byte is one of >, <, or =.
func ageTerm(flag, prop, expr string) (*Term, error) {
	if _, _, err := query.ParseAge(expr); err != nil {
		return nil, fmt.Errorf("%s: %w%s", flag, err, ageOperatorHint(expr))
	}
	var op Op
	switch expr[0] {
	case '>':
		op = OpGt
	case '<':
		op = OpLt
	case '=':
		op = OpEq
	default:
		return nil, fmt.Errorf("%s: unsupported age operator %q", flag, string(expr[0]))
	}
	t := &Term{Field: "age", Op: op, Value: literalValue(expr[1:])}
	if prop != "" {
		t.HasArg = true
		t.Arg = Atom{Text: prop}
	}
	return t, nil
}

// ageOperatorHint suggests the commonest fix for an age expression that is
// only missing its operator: `30d` reads as "older than 30 days" far more often
// than as anything else, so the hint offers `>30d` when that would parse.
func ageOperatorHint(expr string) string {
	if expr == "" || strings.ContainsAny(expr[:1], "><=") {
		return ""
	}
	if _, _, err := query.ParseAge(">" + expr); err != nil {
		return ""
	}
	return "; did you mean >" + expr + "?"
}

// matchTerm builds `field:value`, the `:` form every string and flag-shaped
// field accepts.
func matchTerm(field, value string) *Term {
	return &Term{Field: field, Op: OpMatch, Value: literalValue(value)}
}

// countTerm builds a link-count comparison such as `backlinks>=3`. The bound is
// emitted as an inclusive >= or <= precisely because query.Flags's bounds are
// inclusive; going through the strict > or < would need an off-by-one that
// countField would then have to undo.
func countTerm(field string, op Op, bound int) *Term {
	return &Term{Field: field, Op: op, Value: literalValue(strconv.Itoa(bound))}
}

// literalValue wraps one flag string as a single-atom value. Quoting is
// Atom.String's job, so a flag value carrying spaces, a keyword, or punctuation
// round-trips through the lexer without the caller thinking about it.
func literalValue(text string) Value {
	return atomValue(Atom{Text: text})
}
