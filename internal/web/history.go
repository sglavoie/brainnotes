package web

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

const (
	// historyEntryLimit is the page a client gets when it asks for no particular
	// size. It is what the single history panel has always shown, kept as the
	// default so a client that sends no query reads exactly what it read before
	// this route learned to paginate.
	historyEntryLimit = 20
	// historyEntryMaxLimit bounds what a client may ask for. Every entry in a
	// page is read from its own manifest and classified, so an unbounded limit is
	// an unbounded read of the whole journal on one request; a client that wants
	// more than this asks for the next page.
	historyEntryMaxLimit = 200
	// historyWarningLimit caps the unreadable-entry list. It is deliberately not
	// the page size: warnings are not paginated and not filtered, for the reason
	// historyResponse.WarningCount gives.
	historyWarningLimit = 20
)

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != historyListAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	query, err := parseHistoryQuery(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	// Either configuration file can itself be restored by undo — `bn config
	// migrate` journals both, and undoing it can remove an overlay that did not
	// exist before — so history and undo both re-read the pair before answering;
	// otherwise the UI would report a configuration generation that no longer
	// matches disk.
	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err))
		return
	}

	// An unreadable journal root is still an error — the history is unknown, and
	// answering "no entries" would be a lie the UI cannot see through. An
	// individual unreadable entry is reported alongside the entries that are fine,
	// so one corrupt manifest costs one undo record instead of the whole panel.
	entries, warnings, err := journal.ListEntries(noteops.JournalRoot(s.vault.Root))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeJournal, "list history", err))
		return
	}
	classifier := newHistoryPathClassifier(s.config())
	matched := filterHistoryEntries(entries, query, classifier)
	total := len(matched)
	page := pageHistoryEntries(matched, query)

	warningCount := len(warnings)
	if len(warnings) > historyWarningLimit {
		warnings = warnings[:historyWarningLimit]
	}

	out := make([]historyEntryResponse, 0, len(page))
	for _, entry := range page {
		out = append(out, historyEntryForJournalEntry(entry, classifier))
	}
	writeManagementJSON(w, http.StatusOK, historyResponse{
		OK:           true,
		Entries:      out,
		Total:        total,
		Offset:       query.Offset,
		Limit:        query.Limit,
		HasMore:      query.Offset+len(page) < total,
		Origins:      historyOriginOptions(),
		Classes:      historyPathClassOptions(),
		Warnings:     historyWarningsForJournalWarnings(warnings),
		WarningCount: warningCount,
	})
}

// historyQuery is one client's request for a window onto the journal.
type historyQuery struct {
	Limit  int
	Offset int
	// Origins and Classes are OR within themselves and AND with each other, which
	// is the reading somebody filtering expects: "imports or resolutions, that
	// touched an attachment".
	Origins []historyOrigin
	Classes []historyPathClass
	// Path is case-folded for contains searches and preserved for exact matches
	// against the recorded vault-relative filename.
	Path      string
	PathMatch string
}

// parseHistoryQuery reads the window and the filters, refusing anything it does
// not understand.
//
// An unparseable limit, a negative offset, and an unknown origin are all errors
// rather than defaults. The alternative — quietly dropping a filter — answers a
// narrower question with the whole journal, and the reply looks identical to a
// filter that matched everything. That is the one failure this surface must not
// have: somebody looking for the version they want must never be shown a list
// that hides it, or a list they believe is narrower than it is.
func parseHistoryQuery(r *http.Request) (historyQuery, error) {
	values := r.URL.Query()
	query, err := parseHistoryWindow(values)
	if err != nil {
		return historyQuery{}, err
	}
	if query.Origins, err = parseHistoryFilter(values["origin"], parseHistoryOrigin, "origin"); err != nil {
		return historyQuery{}, err
	}
	if query.Classes, err = parseHistoryFilter(values["class"], parseHistoryPathClass, "file class"); err != nil {
		return historyQuery{}, err
	}
	query.PathMatch = values.Get("path_match")
	switch query.PathMatch {
	case "", "contains":
		query.Path = strings.ToLower(strings.TrimSpace(values.Get("path")))
	case "exact":
		query.Path = values.Get("path")
	default:
		return historyQuery{}, noteops.Errorf(noteops.CodeInvalidRequest, "history: unknown path_match %q", query.PathMatch)
	}
	return query, nil
}

// parseHistoryWindow reads the slice of the journal being asked for. The bound
// is checked here rather than clamped, for the reason parseHistoryQuery gives:
// a client that asked for a page the route will not serve is told so, instead of
// reading a different page than it believes it read.
func parseHistoryWindow(values url.Values) (historyQuery, error) {
	limit, err := parseHistoryInt(values.Get("limit"), historyEntryLimit)
	if err != nil {
		return historyQuery{}, err
	}
	if limit < 1 || limit > historyEntryMaxLimit {
		return historyQuery{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"history: limit must be between 1 and %d", historyEntryMaxLimit)
	}
	offset, err := parseHistoryInt(values.Get("offset"), 0)
	if err != nil {
		return historyQuery{}, err
	}
	if offset < 0 {
		return historyQuery{}, noteops.Errorf(noteops.CodeInvalidRequest, "history: offset must not be negative")
	}
	return historyQuery{Limit: limit, Offset: offset}, nil
}

// parseHistoryFilter reads one repeated filter parameter into its vocabulary,
// de-duplicated and in the order it was sent. Origins and classes are two
// vocabularies with one rule — an unknown value is refused by name and never
// dropped — so they are read by one function rather than by two that could come
// to disagree about what an unrecognized filter means.
func parseHistoryFilter[T comparable](raws []string, parse func(string) (T, bool), kind string) ([]T, error) {
	var out []T
	for _, raw := range raws {
		value, ok := parse(raw)
		if !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "history: unknown %s %q", kind, raw)
		}
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out, nil
}

func parseHistoryInt(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, noteops.WrapError(noteops.CodeInvalidRequest, "history: invalid number", err)
	}
	return value, nil
}

// filterHistoryEntries keeps the entries a query asks for, in the order
// ListEntries returned them — newest first.
func filterHistoryEntries(entries []journal.Entry, query historyQuery, classifier historyPathClassifier) []journal.Entry {
	if len(query.Origins) == 0 && len(query.Classes) == 0 && query.Path == "" {
		return entries
	}
	matched := make([]journal.Entry, 0, len(entries))
	for _, entry := range entries {
		if historyEntryMatches(entry, query, classifier) {
			matched = append(matched, entry)
		}
	}
	return matched
}

func historyEntryMatches(entry journal.Entry, query historyQuery, classifier historyPathClassifier) bool {
	if len(query.Origins) > 0 && !slices.Contains(query.Origins, historyOriginForCommand(entry.Command)) {
		return false
	}
	if len(query.Classes) > 0 && !historyEntryHasClass(entry, query.Classes, classifier) {
		return false
	}
	if query.Path != "" && !historyEntryHasPath(entry, query.Path, query.PathMatch) {
		return false
	}
	return true
}

func historyEntryHasClass(entry journal.Entry, classes []historyPathClass, classifier historyPathClassifier) bool {
	for _, file := range entry.Files {
		if slices.Contains(classes, classifier.classify(file.Path)) {
			return true
		}
	}
	return false
}

func historyEntryHasPath(entry journal.Entry, needle, mode string) bool {
	for _, file := range entry.Files {
		if mode == "exact" {
			if file.Path == needle {
				return true
			}
		} else if strings.Contains(strings.ToLower(file.Path), needle) {
			return true
		}
	}
	return false
}

// pageHistoryEntries takes the requested window. An offset past the end is an
// empty page rather than an error, because a list that shrank between two
// requests — an entry pruned, a filter narrowed — is ordinary, and refusing the
// request would turn it into a failure somebody has to understand.
func pageHistoryEntries(entries []journal.Entry, query historyQuery) []journal.Entry {
	if query.Offset >= len(entries) {
		return nil
	}
	end := min(query.Offset+query.Limit, len(entries))
	return entries[query.Offset:end]
}
