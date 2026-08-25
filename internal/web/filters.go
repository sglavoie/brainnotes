package web

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type noteFilters struct {
	Query       string
	foldedQuery string
	flags       query.Flags
	predicate   query.Predicate
}

func (f noteFilters) hasNonTextFilters() bool {
	flags := f.flags
	return hasNonEmptyValue(flags.Tags) ||
		hasNonEmptyValue(flags.Categories) ||
		strings.TrimSpace(flags.Status) != "" ||
		strings.TrimSpace(flags.Type) != "" ||
		strings.TrimSpace(flags.Has) != "" ||
		strings.TrimSpace(flags.Missing) != "" ||
		strings.TrimSpace(flags.Age) != "" ||
		strings.TrimSpace(flags.Created) != "" ||
		strings.TrimSpace(flags.Folder) != "" ||
		flags.Orphan ||
		flags.BrokenLinks ||
		flags.DeadEnd ||
		flags.MinBacklinks >= 0 ||
		flags.MaxBacklinks >= 0 ||
		flags.MinLinks >= 0 ||
		flags.MaxLinks >= 0
}

// withoutQuery returns a copy with the free-text query cleared. The predicate
// and flags are built from the structured filters only, so they stay valid.
func (f noteFilters) withoutQuery() noteFilters {
	f.Query = ""
	f.foldedQuery = ""
	return f
}

func parseNoteFilters(params url.Values, root string, graph *query.Graph) (noteFilters, error) {
	flags := query.Flags{
		Tags:         repeatedParam(params, "tag"),
		Categories:   repeatedParam(params, "category"),
		Status:       params.Get("status"),
		Type:         params.Get("type"),
		Has:          params.Get("has"),
		Missing:      params.Get("missing"),
		Age:          params.Get("age"),
		Created:      params.Get("created"),
		Folder:       params.Get("folder"),
		MinBacklinks: -1,
		MaxBacklinks: -1,
		MinLinks:     -1,
		MaxLinks:     -1,
		Root:         root,
		Graph:        graph,
	}

	var err error
	if flags.Orphan, err = boolParam(params, "orphan"); err != nil {
		return noteFilters{}, err
	}
	if flags.BrokenLinks, err = boolParam(params, "broken"); err != nil {
		return noteFilters{}, err
	}
	if flags.DeadEnd, err = boolParam(params, "dead_end"); err != nil {
		return noteFilters{}, err
	}
	if flags.MinBacklinks, err = intParam(params, "min_backlinks", flags.MinBacklinks); err != nil {
		return noteFilters{}, err
	}
	if flags.MaxBacklinks, err = intParam(params, "max_backlinks", flags.MaxBacklinks); err != nil {
		return noteFilters{}, err
	}
	if flags.MinLinks, err = intParam(params, "min_links", flags.MinLinks); err != nil {
		return noteFilters{}, err
	}
	if flags.MaxLinks, err = intParam(params, "max_links", flags.MaxLinks); err != nil {
		return noteFilters{}, err
	}

	predicate, err := query.FromFlags(flags)
	if err != nil {
		return noteFilters{}, errorsf("filters: %w", err)
	}

	queryText := params.Get("q")
	return noteFilters{
		Query:       queryText,
		foldedQuery: foldSearchQuery(queryText),
		flags:       flags,
		predicate:   predicate,
	}, nil
}

func matchNoteWithFilters(note *vault.Note, filters noteFilters) (noteTextMatch, bool, error) {
	if filters.predicate == nil {
		return noteTextMatch{}, false, errorsf("nil filter predicate")
	}
	if !filters.predicate(note) {
		return noteTextMatch{}, false, nil
	}
	if filters.foldedQuery == "" {
		return noteTextMatch{}, true, nil
	}
	return matchNoteText(note, filters.foldedQuery)
}

func repeatedParam(params url.Values, key string) []string {
	values := params[key]
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func hasNonEmptyValue(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func boolParam(params url.Values, key string) (bool, error) {
	value, ok := firstParam(params, key)
	if !ok {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "t", "true", "y", "yes", "on":
		return true, nil
	case "0", "f", "false", "n", "no", "off":
		return false, nil
	default:
		return false, errorsf("filter %q must be boolean, got %q", key, value)
	}
}

func intParam(params url.Values, key string, fallback int) (int, error) {
	value, ok := firstParam(params, key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, errorsf("filter %q must be an integer, got %q", key, value)
	}
	return n, nil
}

func firstParam(params url.Values, key string) (string, bool) {
	values, ok := params[key]
	if !ok {
		return "", false
	}
	if len(values) == 0 {
		return "", true
	}
	return values[0], true
}
