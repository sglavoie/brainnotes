package web

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

const (
	queryValidateAPIRoutePath    = "/api/query/validate"
	queryCompletionsAPIRoutePath = "/api/query/completions"

	// queryCompletionLimit caps one completion response. A query bar shows a
	// handful of rows; a vault with a thousand distinct tags would otherwise
	// serialize all of them on every keystroke.
	queryCompletionLimit = 50
)

// Both routes are read-only and take no CSRF token, per
// docs/internals/web-write-classes.md: they touch no file, mutate no state, and
// answer from the current snapshot only.

type queryValidateResponse struct {
	OK bool `json:"ok"`
	// Query echoes the expression exactly as received, since every offset in
	// Error is an index into it.
	Query string `json:"query"`
	// Normalized is Expr.String() for a valid query — the canonical spelling
	// the CLI and saved views use. Empty when the query is invalid.
	Normalized string `json:"normalized,omitempty"`
	// FieldsUsed lists every field the query names, lower-cased and sorted. It
	// is populated whenever the query *parses*, even if it then fails to
	// compile, because an unknown-field error is exactly when knowing which
	// other fields were understood helps.
	FieldsUsed []string            `json:"fields_used"`
	NeedsGraph bool                `json:"needs_graph"`
	Error      *queryValidateError `json:"error,omitempty"`
}

// queryValidateError locates a rejection inside the query. Start, End, and
// Column are UTF-16 code-unit offsets, not bytes: the browser indexes the input
// it feeds setSelectionRange in code units, so converting here is what keeps a
// caret from drifting on an accented title. Line is 1-based.
type queryValidateError struct {
	Message string `json:"message"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	// Positioned reports whether the offsets were derived from the query. A
	// failure with no span (missing vault state, say) still carries the four
	// fields so a client needs one decoder, and this says not to point at them.
	Positioned  bool     `json:"positioned"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type queryCompletionsResponse struct {
	OK    bool   `json:"ok"`
	Query string `json:"query"`
	// Kind is "field" or "value": what the caret position expects.
	Kind  string `json:"kind"`
	Field string `json:"field,omitempty"`
	Arg   string `json:"arg,omitempty"`
	// Prefix is the partial token the candidates were filtered by.
	Prefix string `json:"prefix"`
	// Pos is the caret, and Replace the range a chosen Insert overwrites, both
	// in UTF-16 code units for the same reason the error offsets are.
	Pos         int               `json:"pos"`
	Replace     queryReplaceRange `json:"replace"`
	Completions []queryCompletion `json:"completions"`
}

type queryReplaceRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type queryCompletion struct {
	// Value is the bare candidate, for display.
	Value string `json:"value"`
	// Insert is the query text to splice over Replace: the same value quoted,
	// bracketed, or suffixed with ":" as its position requires.
	Insert string `json:"insert"`
	// Kind names the candidate's source ("field", "tag", "folder", ...).
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count,omitempty"`
}

// handleQueryValidate compiles an expression without running it, so a query bar
// can report a rejection while the user types. It deliberately returns 200 with
// ok=false for an invalid query: the request itself is well-formed, and a 400
// would make an ordinary mid-typing state look like a client bug.
func (s *Server) handleQueryValidate(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != queryValidateAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}

	expr := r.URL.Query().Get("expr")
	payload := queryValidateResponse{Query: expr, FieldsUsed: []string{}}

	// Parse first: a query that parses but fails to compile still has fields
	// worth reporting, and Compile would only hand back the first error.
	if parsed, parseErr := queryexpr.Parse(expr); parseErr == nil {
		payload.FieldsUsed = queryexpr.FieldsUsed(parsed)
	}

	cfg := s.config()
	// Compiling through the same door /api/search uses means validate accepts a
	// query exactly when the search endpoint would, rather than approximating.
	if _, err := compileFilterExpr(expr, s.vault.Root, snapshot.graph, &cfg.Frontmatter); err != nil {
		payload.Error = buildQueryValidateError(expr, err)
		writeManagementJSON(w, http.StatusOK, payload)
		return
	}

	plan, err := queryexpr.Compile(expr, queryexpr.Env{
		Root:        s.vault.Root,
		Frontmatter: &cfg.Frontmatter,
		Graph:       snapshot.graph,
	})
	if err != nil {
		payload.Error = buildQueryValidateError(expr, err)
		writeManagementJSON(w, http.StatusOK, payload)
		return
	}

	payload.OK = true
	payload.NeedsGraph = plan.NeedsGraph()
	if plan.Expr != nil {
		payload.Normalized = plan.Expr.String()
	}
	writeManagementJSON(w, http.StatusOK, payload)
}

// buildQueryValidateError converts a compile failure into the flat, UTF-16
// positioned shape the query bar consumes.
func buildQueryValidateError(expr string, err error) *queryValidateError {
	if err == nil {
		return nil
	}

	out := &queryValidateError{Message: err.Error(), Line: 1, Column: 1}

	var exprErr *exprFilterError
	if errors.As(err, &exprErr) {
		out.Message = exprErr.Err.Error()
	}

	var parseErr *queryexpr.ParseError
	if !errors.As(err, &parseErr) {
		return out
	}

	out.Message = parseErr.Msg
	if len(parseErr.Suggest) > 0 {
		out.Suggestions = append([]string(nil), parseErr.Suggest...)
	}
	// ParseError spans index its own Source, which is the text that was
	// compiled; validate compiles the parameter verbatim, so the two agree.
	source := parseErr.Source
	if source == "" {
		source = expr
	}
	position := parseErrorPosition(parseErr)
	if position == nil {
		return out
	}

	lineStart := 0
	if position.Line > 1 {
		lineStart = strings.LastIndex(source[:clampOffset(position.Start, len(source))], "\n") + 1
	}
	out.Start = utf16Offset(source, position.Start)
	out.End = utf16Offset(source, position.End)
	out.Line = position.Line
	out.Column = utf16Offset(source[lineStart:], clampOffset(position.Start-lineStart, len(source)-lineStart)) + 1
	out.Positioned = true
	return out
}

// handleQueryCompletions answers "what can go here", given an expression and a
// caret. It never rejects an incomplete query: `tag:` and `near:[[Deep` are the
// states a completion UI exists to serve, and both are parse errors.
func (s *Server) handleQueryCompletions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != queryCompletionsAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	params := r.URL.Query()
	expr := params.Get("expr")

	pos := utf16Offset(expr, len(expr))
	if raw := strings.TrimSpace(params.Get("pos")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeManagementErrorStatus(w, http.StatusBadRequest, noteops.Errorf(
				noteops.CodeInvalidRequest, "query: pos must be a non-negative integer, got %q", raw))
			return
		}
		pos = parsed
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}

	completion := queryexpr.CompletionAt(expr, byteOffsetFromUTF16(expr, pos))
	source := &queryValueSource{server: s, snapshot: snapshot}

	writeManagementJSON(w, http.StatusOK, queryCompletionsResponse{
		OK:     true,
		Query:  expr,
		Kind:   string(completion.Kind),
		Field:  completion.Field,
		Arg:    completion.Arg,
		Prefix: completion.Prefix,
		Pos:    utf16Offset(expr, byteOffsetFromUTF16(expr, pos)),
		Replace: queryReplaceRange{
			Start: utf16Offset(expr, completion.Span.Start),
			End:   utf16Offset(expr, completion.Span.End),
		},
		Completions: source.completionsFor(completion),
	})
}

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

// rankQueryCompletions filters candidates by the caret's prefix and orders
// them: prefix matches first (a user typing "wi" means it starts with "wi"),
// then by how many notes back the value, then by display spelling.
func rankQueryCompletions(candidates []candidate, c queryexpr.Completion) []queryCompletion {
	prefix := foldQueryValue(c.Prefix)

	type scored struct {
		candidate
		exactPrefix bool
	}
	matches := make([]scored, 0, len(candidates))
	for _, cand := range candidates {
		if prefix == "" {
			matches = append(matches, scored{candidate: cand, exactPrefix: true})
			continue
		}
		folded := foldQueryValue(cand.value)
		switch {
		case strings.HasPrefix(folded, prefix):
			matches = append(matches, scored{candidate: cand, exactPrefix: true})
		case strings.Contains(folded, prefix):
			matches = append(matches, scored{candidate: cand})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].exactPrefix != matches[j].exactPrefix {
			return matches[i].exactPrefix
		}
		if matches[i].count != matches[j].count {
			return matches[i].count > matches[j].count
		}
		return facetDisplayLess(matches[i].value, matches[j].value)
	})

	if len(matches) > queryCompletionLimit {
		matches = matches[:queryCompletionLimit]
	}

	out := make([]queryCompletion, 0, len(matches))
	for _, match := range matches {
		out = append(out, queryCompletion{
			Value:  match.value,
			Insert: queryCompletionInsert(match.candidate, c),
			Kind:   match.kind,
			Detail: match.detail,
			Count:  match.count,
		})
	}
	return out
}

// queryCompletionInsert renders the text that replaces Completion.Span. A field
// name gains the ":" that starts its value; a value is bracketed, quoted, or
// left bare exactly as the lexer requires to read it back as one atom.
func queryCompletionInsert(cand candidate, c queryexpr.Completion) string {
	if c.Kind == queryexpr.CompleteField {
		return cand.value + ":"
	}
	if cand.wikilink || c.Wikilink {
		return queryexpr.Atom{Text: cand.value, Wikilink: true}.String()
	}
	if c.Quoted {
		// The span covers the opening quote the user already typed, so the
		// replacement has to supply both.
		return quoteQueryValue(cand.value)
	}
	return queryexpr.Atom{Text: cand.value}.String()
}

// quoteQueryValue wraps a value in the phrase quoting the lexer understands.
// Only `\"` and `\\` are escapes there, so nothing else may be escaped.
func quoteQueryValue(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// foldQueryValue folds a value for matching the way the compiler compares one:
// NFC first, then case. The vault is NFC-normalized and the lexer normalizes
// atom text, so a decomposed prefix must still match a composed value.
func foldQueryValue(value string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(value)))
}
