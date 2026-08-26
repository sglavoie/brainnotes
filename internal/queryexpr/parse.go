package queryexpr

import (
	"strconv"
	"strings"
)

// Parse turns query source into an Expr. The returned error is always a
// *ParseError. An empty (or whitespace-only) query parses to a zero-operand
// And — the identity that matches everything, matching query.And's no-argument
// convention — rather than an error.
func Parse(src string) (Expr, error) {
	toks, lexErr := lex(src)
	if lexErr != nil {
		return nil, lexErr
	}
	p := &parser{src: src, toks: toks}
	if p.peek().Kind == tokEOF {
		return &And{Span: Span{Start: 0, End: len(src)}}, nil
	}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if tok := p.peek(); tok.Kind != tokEOF {
		return nil, p.errAt(tok, "unexpected "+tok.describe())
	}
	return expr, nil
}

type parser struct {
	src  string
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	tok := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return tok
}

func (p *parser) errAt(tok token, msg string) *ParseError {
	return NewParseError(p.src, tok.Span, msg, nil)
}

// atKeyword reports whether the current token is the bare word kw. Keywords
// are matched case-insensitively; quoting a word makes it a literal, so
// `"or"` searches for the text rather than combining terms.
func (p *parser) atKeyword(kw string) bool {
	tok := p.peek()
	return tok.Kind == tokWord && strings.EqualFold(tok.Text, kw)
}

// startsUnary reports whether the current token can begin a unary — i.e.
// whether juxtaposition should continue the conjunction.
func (p *parser) startsUnary() bool {
	tok := p.peek()
	switch tok.Kind {
	case tokWord:
		return !strings.EqualFold(tok.Text, "and") && !strings.EqualFold(tok.Text, "or")
	case tokString, tokWikilink, tokLParen, tokMinus:
		return true
	}
	return false
}

func (p *parser) parseOr() (Expr, error) {
	first, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	operands := []Expr{first}
	for p.atKeyword("or") {
		p.next()
		if !p.startsUnary() {
			return nil, p.errAt(p.peek(), "expected a term after \"or\", got "+p.peek().describe())
		}
		rhs, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		operands = append(operands, rhs)
	}
	if len(operands) == 1 {
		return operands[0], nil
	}
	return &Or{Operands: operands, Span: join(first.Range(), operands[len(operands)-1].Range())}, nil
}

func (p *parser) parseAnd() (Expr, error) {
	first, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	operands := []Expr{first}
	for {
		if p.atKeyword("and") {
			p.next()
			if !p.startsUnary() {
				return nil, p.errAt(p.peek(), "expected a term after \"and\", got "+p.peek().describe())
			}
		} else if !p.startsUnary() {
			break
		}
		rhs, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		operands = append(operands, rhs)
	}
	if len(operands) == 1 {
		return operands[0], nil
	}
	return &And{Operands: operands, Span: join(first.Range(), operands[len(operands)-1].Range())}, nil
}

func (p *parser) parseUnary() (Expr, error) {
	tok := p.peek()
	if tok.Kind == tokMinus || (tok.Kind == tokWord && strings.EqualFold(tok.Text, "not")) {
		p.next()
		if !p.startsUnary() {
			return nil, p.errAt(p.peek(), "expected a term after "+quote(tok.Text)+", got "+p.peek().describe())
		}
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Not{Operand: operand, Span: join(tok.Span, operand.Range())}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	if p.peek().Kind == tokLParen {
		open := p.next()
		if p.peek().Kind == tokRParen {
			return nil, p.errAt(p.peek(), "empty group: expected a term between \"(\" and \")\"")
		}
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().Kind != tokRParen {
			return nil, p.errAt(p.peek(), "unclosed group opened at column "+columnOf(p.src, open.Span)+": expected \")\"")
		}
		p.next()
		return inner, nil
	}
	return p.parseTerm()
}

// parseTerm parses the one leaf production,
// `ident arg? (op value)? | phrase | word`.
//
// Whether the atom after a `:` is the term's argument or its value takes one
// token of lookahead: it is the argument exactly when a comparison operator
// follows it. That is what lets `tag:foo`, `age:modified>90d`, and
// `near:"Deep Work"<=2` share a single production.
func (p *parser) parseTerm() (Expr, error) {
	tok := p.peek()
	switch tok.Kind {
	case tokString, tokWikilink:
		p.next()
		return &Term{Value: atomValue(atomOf(tok)), Span: tok.Span}, nil
	case tokWord:
	default:
		return nil, p.errAt(tok, "expected a term, got "+tok.describe())
	}

	word := p.next()
	term := &Term{Span: word.Span}

	switch {
	case p.peek().Kind == tokColon:
		p.next()
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if value.Kind == ValueAtom && p.peek().Kind == tokOp {
			// The atom was an argument, not the value: `age:modified>90d`.
			term.Field = word.Text
			term.Arg = value.Atoms[0]
			term.HasArg = true
			if err := p.parseOpValue(term); err != nil {
				return nil, err
			}
			return term, nil
		}
		term.Field = word.Text
		term.Op = OpMatch
		term.Value = value
		term.Span = join(term.Span, value.Span)
		return term, nil

	case p.peek().Kind == tokLParen && p.peek().Span.Start == word.Span.End:
		// Adjacency matters: `links-to("Deep Work")` is an argument, while
		// `orphan (a or b)` is a bare word conjoined with a group.
		p.next()
		arg, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		if p.peek().Kind != tokRParen {
			return nil, p.errAt(p.peek(), "expected \")\" after the "+quote(word.Text)+" argument, got "+p.peek().describe())
		}
		closing := p.next()
		term.Field = word.Text
		term.Arg = arg
		term.HasArg = true
		term.Span = join(term.Span, closing.Span)
		if p.peek().Kind == tokOp {
			if err := p.parseOpValue(term); err != nil {
				return nil, err
			}
		}
		return term, nil

	case p.peek().Kind == tokOp:
		term.Field = word.Text
		if err := p.parseOpValue(term); err != nil {
			return nil, err
		}
		return term, nil
	}

	// A bare word is a text term, not a field.
	term.Value = atomValue(atomOf(word))
	return term, nil
}

// parseOpValue consumes `op value` into term, extending its span.
func (p *parser) parseOpValue(term *Term) error {
	opTok := p.next()
	value, err := p.parseValue()
	if err != nil {
		return err
	}
	term.Op = Op(opTok.Text)
	term.Value = value
	term.Span = join(term.Span, value.Span)
	return nil
}

func (p *parser) parseValue() (Value, error) {
	if p.peek().Kind != tokLBracket {
		atom, err := p.parseAtom()
		if err != nil {
			return Value{}, err
		}
		return atomValue(atom), nil
	}
	open := p.next()
	if p.peek().Kind == tokRBracket {
		return Value{}, p.errAt(p.peek(), "empty list: expected at least one value between \"[\" and \"]\"")
	}
	var atoms []Atom
	for {
		atom, err := p.parseAtom()
		if err != nil {
			return Value{}, err
		}
		atoms = append(atoms, atom)
		if p.peek().Kind == tokComma {
			p.next()
			continue
		}
		break
	}
	if p.peek().Kind != tokRBracket {
		return Value{}, p.errAt(p.peek(), "unclosed list opened at column "+columnOf(p.src, open.Span)+": expected \",\" or \"]\"")
	}
	closing := p.next()
	return Value{Kind: ValueList, Atoms: atoms, Span: join(open.Span, closing.Span)}, nil
}

func (p *parser) parseAtom() (Atom, error) {
	tok := p.peek()
	switch tok.Kind {
	case tokWord, tokString, tokWikilink:
		p.next()
		return atomOf(tok), nil
	}
	return Atom{}, p.errAt(tok, "expected a value, got "+tok.describe())
}

func atomOf(tok token) Atom {
	return Atom{
		Text:     tok.Text,
		Quoted:   tok.Kind == tokString,
		Wikilink: tok.Kind == tokWikilink,
		Span:     tok.Span,
	}
}

func atomValue(a Atom) Value {
	return Value{Kind: ValueAtom, Atoms: []Atom{a}, Span: a.Span}
}

// columnOf renders the 1-based rune column a span starts at, for use inside an
// error message that points somewhere else.
func columnOf(src string, span Span) string {
	return strconv.Itoa(NewParseError(src, span, "", nil).Column)
}
