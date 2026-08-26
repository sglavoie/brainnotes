package web

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
)

// exprFilterError reports a rejected expr= parameter. It keeps the expression
// text alongside the compile error so the handler can render a payload the
// query bar can point a caret with, instead of a bare 400.
type exprFilterError struct {
	Expr string
	Err  error
}

func (e *exprFilterError) Error() string {
	return "filters: expr: " + e.Err.Error()
}

func (e *exprFilterError) Unwrap() error {
	return e.Err
}

// compileFilterExpr compiles one query expression into a predicate. Compilation
// and lowering share the Env, so a query naming a graph field is validated
// against the same graph the scan will read.
func compileFilterExpr(
	expr string,
	root string,
	graph *query.Graph,
	frontmatter *config.FrontmatterConfig,
) (query.Predicate, error) {
	env := queryexpr.Env{
		Root:        root,
		Frontmatter: frontmatter,
		Graph:       graph,
	}
	plan, err := queryexpr.Compile(expr, env)
	if err != nil {
		return nil, &exprFilterError{Expr: expr, Err: err}
	}
	predicate, err := plan.Predicate(env)
	if err != nil {
		return nil, &exprFilterError{Expr: expr, Err: err}
	}
	return predicate, nil
}

// filterErrorResponse is the 400 body for a rejected filter parameter.
type filterErrorResponse struct {
	Error filterErrorDetail `json:"error"`
}

type filterErrorDetail struct {
	Message     string               `json:"message"`
	Param       string               `json:"param,omitempty"`
	Query       string               `json:"query,omitempty"`
	Position    *filterErrorPosition `json:"position,omitempty"`
	Suggestions []string             `json:"suggestions,omitempty"`
}

// filterErrorPosition locates the offending text inside Query. Start and End
// are byte offsets; Line and Column are 1-based and counted in runes, so an
// accented expression still points at the character the user typed.
type filterErrorPosition struct {
	Start  int `json:"start"`
	End    int `json:"end"`
	Line   int `json:"line"`
	Column int `json:"column"`
}

// writeFilterError renders a filter rejection as a structured 400.
func writeFilterError(w http.ResponseWriter, err error) {
	writeManagementJSON(w, http.StatusBadRequest, buildFilterErrorResponse(err))
}

func buildFilterErrorResponse(err error) filterErrorResponse {
	if err == nil {
		return filterErrorResponse{Error: filterErrorDetail{Message: "bad filters"}}
	}

	var exprErr *exprFilterError
	if !errors.As(err, &exprErr) {
		return filterErrorResponse{Error: filterErrorDetail{Message: err.Error()}}
	}

	detail := filterErrorDetail{
		Message: exprErr.Err.Error(),
		Param:   "expr",
		Query:   exprErr.Expr,
	}
	var parseErr *queryexpr.ParseError
	if errors.As(exprErr.Err, &parseErr) {
		detail.Message = parseErr.Msg
		detail.Position = parseErrorPosition(parseErr)
		if len(parseErr.Suggest) > 0 {
			detail.Suggestions = append([]string(nil), parseErr.Suggest...)
		}
	}
	return filterErrorResponse{Error: detail}
}

// parseErrorPosition converts a ParseError span into a line/column position.
// ParseError.Column is already a rune column, but it counts from the start of
// the whole source; a multi-line expression needs it relative to its own line.
func parseErrorPosition(err *queryexpr.ParseError) *filterErrorPosition {
	start := clampOffset(err.Span.Start, len(err.Source))
	end := err.Span.End
	if end < start {
		end = start
	}
	end = clampOffset(end, len(err.Source))

	before := err.Source[:start]
	line := 1 + strings.Count(before, "\n")
	column := err.Column
	if line > 1 {
		column = utf8.RuneCountInString(before[strings.LastIndex(before, "\n")+1:]) + 1
	}
	return &filterErrorPosition{Start: start, End: end, Line: line, Column: column}
}

func clampOffset(offset, length int) int {
	if offset < 0 {
		return 0
	}
	if offset > length {
		return length
	}
	return offset
}
