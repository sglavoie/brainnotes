package queryexpr

import "strings"

// Span is a half-open byte range into the source string a node was parsed
// from. Byte offsets — not rune offsets — because that is what slicing the
// source needs; rune conversion happens once, in ParseError, where columns are
// reported to a human.
type Span struct {
	Start int
	End   int
}

// join returns the smallest span covering both operands.
func join(a, b Span) Span {
	s := a
	if b.Start < s.Start {
		s.Start = b.Start
	}
	if b.End > s.End {
		s.End = b.End
	}
	return s
}

// Expr is one node of a parsed query. The concrete types are *And, *Or, *Not,
// and *Term; the unexported marker method keeps the set closed so a compiler
// can exhaustively switch over it.
type Expr interface {
	// String re-renders canonical source for this node, parenthesised only
	// where precedence requires it. Parse(e.String()) yields an equal tree.
	String() string
	// Range reports the source span this node was parsed from. It is the
	// zero Span for a node built by hand rather than parsed.
	Range() Span
	exprNode()
}

// And is a conjunction. A zero-operand And is the identity that matches
// everything, mirroring query.And's no-argument convention, and renders as the
// empty string.
type And struct {
	Operands []Expr
	Span     Span
}

// Or is a disjunction. It never has fewer than two operands when produced by
// the parser, which collapses a one-operand group to the operand itself.
type Or struct {
	Operands []Expr
	Span     Span
}

// Not negates its operand.
type Not struct {
	Operand Expr
	Span    Span
}

// Term is the single leaf production. A bare word or quoted phrase has an
// empty Field, OpNone, and its text in Value; every other form names a Field.
// Arg is the optional `:atom` / `(atom)` qualifier that comes before the
// operator, as in age:modified>90d (Field "age", Arg "modified", OpGt, "90d").
type Term struct {
	Field  string
	Arg    Atom
	HasArg bool
	Op     Op
	Value  Value
	Span   Span
}

func (*And) exprNode()  {}
func (*Or) exprNode()   {}
func (*Not) exprNode()  {}
func (*Term) exprNode() {}

// Range reports the source span of the node.
func (e *And) Range() Span { return e.Span }

// Range reports the source span of the node.
func (e *Or) Range() Span { return e.Span }

// Range reports the source span of the node.
func (e *Not) Range() Span { return e.Span }

// Range reports the source span of the node.
func (e *Term) Range() Span { return e.Span }

// Op is a comparison operator. OpNone means the term carried no operator at
// all, which is only legal for a bare word or phrase.
type Op string

// The operator set. OpMatch is the `:` form — "contains" or "is one of",
// depending on the field — while OpEq is exact equality. `>` and `<` are
// strict.
const (
	OpNone  Op = ""
	OpMatch Op = ":"
	OpEq    Op = "="
	OpNe    Op = "!="
	OpGt    Op = ">"
	OpGe    Op = ">="
	OpLt    Op = "<"
	OpLe    Op = "<="
)

// ValueKind discriminates the shape of a Value.
type ValueKind int

// Value shapes. ValueList is the any-of form, `[a, b]`.
const (
	ValueNone ValueKind = iota
	ValueAtom
	ValueList
)

// Atom is one scalar literal. Text is always the decoded, NFC-normalized
// content: quotes are stripped, escapes resolved, and the `[[` / `]]` of a
// wikilink removed. Quoted and Wikilink record how it was written so String()
// can reproduce a form that re-lexes identically.
type Atom struct {
	Text     string
	Quoted   bool
	Wikilink bool
	Span     Span
}

// String renders the atom as canonical source, quoting only when the lexer
// would otherwise re-split or reinterpret the text.
func (a Atom) String() string {
	if a.Wikilink {
		return "[[" + a.Text + "]]"
	}
	if needsQuote(a.Text) {
		return quote(a.Text)
	}
	return a.Text
}

// Value is a term's right-hand side: absent, one atom, or a list meaning
// any-of.
type Value struct {
	Kind  ValueKind
	Atoms []Atom
	Span  Span
}

// String renders the value as canonical source.
func (v Value) String() string {
	switch v.Kind {
	case ValueAtom:
		if len(v.Atoms) == 0 {
			return ""
		}
		return v.Atoms[0].String()
	case ValueList:
		var b strings.Builder
		b.WriteString("[")
		// A list whose first element is a wikilink must not open with `[[`,
		// which the lexer would read as the start of a wikilink.
		if len(v.Atoms) > 0 && v.Atoms[0].Wikilink {
			b.WriteString(" ")
		}
		for i, a := range v.Atoms {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.String())
		}
		b.WriteString("]")
		return b.String()
	default:
		return ""
	}
}

// String renders the conjunction, parenthesising any disjunct operand because
// or binds looser than and.
func (e *And) String() string {
	return joinOperands(e.Operands, " and ", func(child Expr) bool {
		_, isOr := child.(*Or)
		return isOr
	})
}

// String renders the disjunction. or is the loosest operator, so no operand
// ever needs parentheses.
func (e *Or) String() string {
	return joinOperands(e.Operands, " or ", func(Expr) bool { return false })
}

// String renders the negation, parenthesising a compound operand.
func (e *Not) String() string {
	if e.Operand == nil {
		return "not"
	}
	switch e.Operand.(type) {
	case *And, *Or:
		return "not (" + e.Operand.String() + ")"
	}
	return "not " + e.Operand.String()
}

// String renders the term. The argument uses the `:` form when an operator
// follows it and the parenthesised form when it does not, which is the only
// way `links-to("Deep Work")` stays distinguishable from `links-to:"Deep
// Work"`.
func (e *Term) String() string {
	if e.Field == "" {
		return e.Value.String()
	}
	var b strings.Builder
	b.WriteString(e.Field)
	if e.HasArg {
		if e.Op == OpNone {
			b.WriteString("(")
			b.WriteString(e.Arg.String())
			b.WriteString(")")
		} else {
			b.WriteString(":")
			b.WriteString(e.Arg.String())
		}
	}
	if e.Op != OpNone {
		b.WriteString(string(e.Op))
		b.WriteString(e.Value.String())
	}
	return b.String()
}

func joinOperands(operands []Expr, sep string, parenthesise func(Expr) bool) string {
	parts := make([]string, 0, len(operands))
	for _, child := range operands {
		if child == nil {
			continue
		}
		s := child.String()
		if parenthesise(child) {
			s = "(" + s + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sep)
}

// needsQuote reports whether text must be written as a quoted phrase to
// survive a round-trip through the lexer.
func needsQuote(text string) bool {
	if text == "" {
		return true
	}
	if strings.HasPrefix(text, "-") {
		return true
	}
	if isKeyword(text) {
		return true
	}
	for _, r := range text {
		if !isWordRune(r) {
			return true
		}
	}
	return false
}

// quote renders text as a double-quoted phrase, escaping backslashes and
// quotes. strconv.Quote is deliberately not used: it also escapes non-ASCII
// runes, which would turn an accented title into an unreadable query.
func quote(text string) string {
	var b strings.Builder
	b.WriteString(`"`)
	for _, r := range text {
		switch r {
		case '\\', '"':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	b.WriteString(`"`)
	return b.String()
}

func isKeyword(text string) bool {
	switch strings.ToLower(text) {
	case "and", "or", "not":
		return true
	}
	return false
}
