package web

import (
	"net/url"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
)

// Freeze the complete browser selection into the shared query language before
// issuing a receipt. Apply only redeems this normalized, immutable definition.
func (s *Server) normalizeViewSearch(view *viewDefinition) error {
	if view.FilterParams == "" {
		return nil
	}
	params, err := url.ParseQuery(view.FilterParams)
	if err != nil {
		return noteops.WrapError(noteops.CodeInvalidRequest, "invalid search parameters", err)
	}
	flags, err := parseFilterFlags(params, s.vault.Root, nil)
	if err != nil {
		return noteops.WrapError(noteops.CodeInvalidRequest, "invalid search filters", err)
	}
	facets, err := queryexpr.FromFlags(flags)
	if err != nil {
		return noteops.WrapError(noteops.CodeInvalidRequest, "invalid search filters", err)
	}
	parts := []queryexpr.Expr{}
	if facets.String() != "" {
		parts = append(parts, facets)
	}
	if raw := strings.TrimSpace(params.Get("expr")); raw != "" {
		expr, err := queryexpr.Parse(raw)
		if err != nil {
			return noteops.WrapError(noteops.CodeInvalidRequest, "invalid query expression", err)
		}
		parts = append(parts, expr)
	}
	view.Query = (&queryexpr.And{Operands: parts}).String()
	if view.Query == "" {
		view.Query = `""`
	}
	view.Search = strings.TrimSpace(params.Get("q"))
	if view.Sort == "" {
		view.Sort = queryexpr.SortMatch
	}
	view.FilterParams = ""
	return nil
}
