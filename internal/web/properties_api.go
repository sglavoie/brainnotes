package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
)

const (
	propertiesAPIRoutePath       = "/api/properties"
	propertiesDetailAPIRoutePath = "/api/properties/detail"
	propertiesRoutePath          = "/properties"
	propertiesRoutePrefix        = "/properties/"

	// propertyRowLimitDefault and propertyRowLimitMax bound one page of note
	// rows. The maximum is hard: a larger request is clamped rather than
	// refused, so a client can ask for "as many as allowed" without guessing.
	propertyRowLimitDefault = 50
	propertyRowLimitMax     = 200
)

type propertiesResponse struct {
	OK         bool                  `json:"ok"`
	Generation int64                 `json:"generation"`
	TotalNotes int                   `json:"total_notes"`
	Totals     properties.Totals     `json:"totals"`
	Properties []propertySummary     `json:"properties"`
	Proposals  []properties.Proposal `json:"proposals"`
	Config     ConfigStatus          `json:"config"`
}

type propertyDetailResponse struct {
	OK         bool                    `json:"ok"`
	Generation int64                   `json:"generation"`
	Property   propertySummary         `json:"property"`
	Values     []properties.ValueCount `json:"values"`
	Rows       []propertyRowView       `json:"rows"`
	Pagination propertyPagination      `json:"pagination"`
	Filter     propertyRowFilter       `json:"filter"`
	Config     ConfigStatus            `json:"config"`
}

// handleProperties serves the property catalog: every configured property plus
// every property any note actually stores, with the statistics that say how far
// apart those two sets are.
func (s *Server) handleProperties(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != propertiesAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	index, status, err := s.propertyIntelligence()
	if err != nil {
		writeManagementError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, propertiesResponse{
		OK:         true,
		Generation: status.Generation,
		TotalNotes: index.TotalNotes,
		Totals:     index.Totals,
		Properties: buildPropertySummaries(index),
		Proposals:  buildPropertyProposals(index),
		Config:     status,
	})
}

// handlePropertyDetail serves one property's distinct values and paginated note
// rows.
func (s *Server) handlePropertyDetail(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != propertiesDetailAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	query := r.URL.Query()
	name := strings.TrimSpace(query.Get("property"))
	if name == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "properties: property is required"))
		return
	}
	filter, err := parsePropertyRowFilter(query)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	limit, offset, err := parsePropertyPagination(query)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	index, status, err := s.propertyIntelligence()
	if err != nil {
		writeManagementError(w, err)
		return
	}
	prop, ok := index.Property(name)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "properties: %q is not configured and no note uses it", name))
		return
	}

	rows, pagination := selectPropertyRows(prop, filter, limit, offset)
	values := prop.Values
	if values == nil {
		values = []properties.ValueCount{}
	}

	writeManagementJSON(w, http.StatusOK, propertyDetailResponse{
		OK:         true,
		Generation: status.Generation,
		Property:   buildPropertySummary(prop),
		Values:     values,
		Rows:       rows,
		Pagination: pagination,
		Filter:     filter,
		Config:     status,
	})
}

// propertyIntelligence reloads the configuration, then returns the property
// index for the current notes under it. The index is cached until either the
// notes or the configuration generation changes.
func (s *Server) propertyIntelligence() (*properties.Index, ConfigStatus, error) {
	if err := s.reloadConfig(); err != nil {
		return nil, ConfigStatus{}, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err)
	}
	status := s.configState.Status()
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return nil, status, noteops.WrapError(noteops.CodeInternal, "properties: snapshot failed", err)
	}
	cfg := s.config()
	return s.state.propertyIndexFor(snapshot, &cfg.Frontmatter, status.Generation), status, nil
}

func parsePropertyRowFilter(query url.Values) (propertyRowFilter, error) {
	filter := propertyRowFilter{
		Status: strings.TrimSpace(query.Get("filter")),
		Value:  query.Get("value"),
		Query:  strings.TrimSpace(query.Get("q")),
	}
	if filter.Status == "" {
		filter.Status = propertyFilterAll
	}
	switch filter.Status {
	case propertyFilterAll, propertyFilterViolations, propertyFilterMissing, propertyFilterUnsupported:
	default:
		return propertyRowFilter{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: unknown filter %q", filter.Status)
	}
	return filter, nil
}

func parsePropertyPagination(query url.Values) (limit, offset int, err error) {
	limit = propertyRowLimitDefault
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed < 1 {
			return 0, 0, noteops.Errorf(noteops.CodeInvalidRequest, "properties: invalid limit %q", raw)
		}
		limit = parsed
		if limit > propertyRowLimitMax {
			limit = propertyRowLimitMax
		}
	}
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed < 0 {
			return 0, 0, noteops.Errorf(noteops.CodeInvalidRequest, "properties: invalid offset %q", raw)
		}
		offset = parsed
	}
	return limit, offset, nil
}

// propertyDetailPath is the browser route for one property.
func propertyDetailPath(name string) string {
	if strings.TrimSpace(name) == "" {
		return propertiesRoutePath
	}
	return propertiesRoutePrefix + url.PathEscape(name)
}

// propertyNameFromPath decodes the property named by a /properties/<key> route.
// It must be given the escaped path (url.URL.EscapedPath), not the decoded one:
// a name containing a slash is escaped into a single segment, and the decoded
// path is indistinguishable from a genuine multi-segment route.
func propertyNameFromPath(path string) (string, bool) {
	if !strings.HasPrefix(path, propertiesRoutePrefix) {
		return "", false
	}
	encoded := strings.TrimPrefix(path, propertiesRoutePrefix)
	if encoded == "" || strings.Contains(encoded, "/") {
		return "", false
	}
	name, err := url.PathUnescape(encoded)
	if err != nil {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	return name, true
}
