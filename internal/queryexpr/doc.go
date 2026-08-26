// Package queryexpr owns the vault query expression language: the surface
// syntax users type into `bn find`, the library query bar, and the graph
// filter, lowered onto one shared AST. It is deliberately a sibling of
// internal/query rather than files inside it: internal/query is imported by
// recall, noteops, cockpit, web, and brief, and typed comparison needs
// internal/config and internal/schema, so folding the language into query
// would widen that contract for every existing consumer. The dependency
// therefore runs one way — queryexpr may import query, config, schema,
// frontmatter, vault, wikilink, and textnorm, and must never import recall,
// cli, web, noteops, properties, or cockpit. `layering_test.go` pins that.
//
// The grammar is:
//
//	or      := and ( "or" and )*
//	and     := unary ( "and"? unary )*          // juxtaposition = AND
//	unary   := ("not" | "-") unary | primary
//	primary := "(" expr ")" | term
//	term    := ident arg? (op value)? | phrase | word
//	arg     := ":" atom | "(" atom ")"
//	op      := ":" | "=" | "!=" | ">" | ">=" | "<" | "<="
//	value   := atom | "[" atom ("," atom)* "]"   // list = any-of
//
// Precedence is not > and > or. A quoted "a b" is one phrase; a bare a b is
// two terms conjoined. The single `<field>[:arg] [<op> <value>]` production
// covers `age:modified>90d`, `near:"Deep Work"<=2`, and `links-to:"Deep Work"`
// uniformly, so no field needs bespoke syntax: whether the atom after `:` is
// an argument or the value is decided by one token of lookahead — it is an
// argument exactly when a comparison operator follows it.
//
// Expr.String() re-renders canonical source and round-trips through Parse, so
// it is what `bn views show` prints and what the web layer normalizes an
// `expr=` parameter to. Canonicalization is idempotent, not byte-preserving:
// atom text is NFC-normalized through internal/textnorm on the way in (an
// accented title typed in decomposed form must resolve against a composed
// one), quoting is reapplied only where the lexer would otherwise re-split
// the atom, and `==` renders as `=`.
//
// Parse errors are always *ParseError, never a bare error. Column and the
// caret padding in Render are rune-based, not byte-based — accented text in a
// query is exactly what naive column math gets wrong.
//
// Compile lowers a parsed Expr onto internal/query predicates. fields.go holds
// the reserved-field table — the vocabulary a field name is resolved against —
// graph_terms.go holds the six fields that read link structure, and compile.go
// holds Env, Plan, and the tree walk. The two-step shape is
// deliberate: Compile validates a query against nothing but the vault's
// declared property names, Plan.NeedsGraph then reports whether evaluating it
// is worth a query.BuildGraphForRoot, and Plan.Predicate builds the closures
// against whatever Env the caller finally assembles. evaluator.go runs a plan;
// NewMemoryEvaluator is the in-memory scan every caller uses today.
//
// The reserved vocabulary is:
//
//	tag category status type has missing folder     // frontmatter
//	age[:property]                                  // age>90d, age:created>90d
//	pinned orphan dead-end broken-links             // flags, spelled name:true
//	backlinks links                                 // link counts
//	links-to linked-from near path-to               // traversal
//	degree cluster-size                             // structure
//	prop:<name>                                     // any frontmatter key
//
// plus a bare word or quoted phrase, which searches title and body. `>` and
// `<` are strict, so backlinks>3 means at least four. A name that is neither
// reserved nor a declared frontmatter property is a *ParseError with
// did-you-mean candidates drawn from both sets.
//
// A property declared in brainnotes.toml is spelled bare — `rating>3`,
// `due<+30d`, `topics:go` — and typed.go compares it according to its declared
// type, so which operators it accepts and how a literal is read both follow
// from the config rather than from the key's name. An *undeclared* key stays
// queryable through `prop:<name>`, which is also how to reach a key whose name
// a reserved field shadows. The escape hatch exists because a bare undeclared
// name has to stay an unknown-field error: that is what makes did-you-mean
// possible at all. Neither path enforces a property's `allowed` list — finding
// the notes that violate your own schema is the point.
//
// The graph fields are the only ones that name a *note* rather than a value, so
// they are the only ones that can be spelled correctly and still refer to
// nothing. A target that resolves to no note is a *ParseError with did-you-mean
// candidates drawn from the vault's own titles — an empty result set would be
// indistinguishable from a correct empty result set. Resolution needs the graph,
// so it happens at compile time when Env already carries one and at
// Plan.Predicate time otherwise: later, never absent. near and path-to are both
// reachability and share one BFS memo per Plan.Predicate call, so
// `near:[[Hub]]<=2 or path-to:[[Hub]]` costs one traversal for the whole scan.
// The memo is in the call, never on GraphMetrics: that value is built once per
// command run or web snapshot and read by concurrent requests, so it stays
// immutable and lock-free.
//
// flags.go bridges the older surface: FromFlags lowers a query.Flags — the
// flattened `bn find` flag set — onto the same AST, so `bn find --orphan
// --tag alpha` and `bn find -q 'orphan:true and tag:alpha'` are one feature
// rather than two. It is knowingly a second implementation of query.FromFlags,
// because internal/query cannot import this package, and
// flags_equivalence_test.go is what makes that acceptable: it runs the flag
// matrix through both paths and requires identical selections.
//
// sort.go holds SortSpec and ParseSort. Ordering is not expression syntax —
// the grammar has no sort or limit production — so a caller fills Plan.Sort in
// from flags, and queryexpr *never ranks*: a SortSpec records which ordering
// was asked for and nothing about producing it, which keeps the recall
// sidecar's read/write boundary out of a package whose job is compiling text.
// The in-memory evaluator honours the default path ordering and refuses every
// other key rather than silently ignoring it.
package queryexpr
