package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
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
