package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	searchSuggestAPIRoutePath = "/api/search/suggest"
	searchSuggestLimit        = 5
)

// handleSearchSuggest answers "which titles did you probably mean" for a search
// that found nothing. It is the /api/facets shape: GET only, one snapshot, no
// token, no write.
//
// It is a separate route rather than a fallback inside /api/search on purpose.
// A search result set is something a bulk selection and a saved view are built
// from, so it must stay exactly the notes that match; an approximate row mixed
// into it would be an approximate edit. Suggestions are only ever offered.
func (s *Server) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != searchSuggestAPIRoutePath {
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
		writeInternalError(w, "could not load the vault", err)
		return
	}
	results, err := suggestSearchTitles(snapshot, s.vault.Root, foldSearchQuery(r.URL.Query().Get("q")))
	if err != nil {
		writeInternalError(w, "search failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		logEncodeError(searchSuggestAPIRoutePath, err)
	}
}

// titleSuggestion is one candidate title and how far it is from the search.
type titleSuggestion struct {
	result searchResult
	cost   int
}

// suggestSearchTitles returns the notes whose titles are closest to foldedQuery,
// closest first, with title, folder and key as the deterministic tie-break.
func suggestSearchTitles(snapshot vaultSnapshot, root, foldedQuery string) ([]searchResult, error) {
	results := []searchResult{}
	if foldedQuery == "" || snapshot.graph == nil {
		return results, nil
	}
	var hits []titleSuggestion
	for _, note := range snapshot.notes {
		hit, ok, err := suggestTitle(snapshot, root, foldedQuery, note)
		if err != nil {
			return nil, err
		}
		if ok {
			hits = append(hits, hit)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return closerSuggestion(hits[i], hits[j]) })
	for _, hit := range hits[:min(len(hits), searchSuggestLimit)] {
		results = append(results, hit.result)
	}
	return results, nil
}

// suggestTitle is the suggestion note makes for foldedQuery, if its title is
// close enough to be one.
func suggestTitle(snapshot vaultSnapshot, root, foldedQuery string, note *vault.Note) (titleSuggestion, bool, error) {
	if note == nil {
		return titleSuggestion{}, false, nil
	}
	key := snapshot.graph.KeyForNote(note)
	cost, ok := query.FuzzyTitleCost(foldedQuery, query.FoldText(note.Title()))
	if key == "" || !ok {
		return titleSuggestion{}, false, nil
	}
	folder, err := note.Folder(root)
	if err != nil {
		return titleSuggestion{}, false, fmt.Errorf("folder for %q: %w", key, err)
	}
	path, err := notePathForKey(key)
	if err != nil {
		return titleSuggestion{}, false, fmt.Errorf("path for %q: %w", key, err)
	}
	return titleSuggestion{cost: cost, result: searchResult{
		Key:      key,
		Title:    note.Title(),
		Wikilink: wikilinkTargetForNote(root, note),
		Folder:   folder,
		Path:     path,
	}}, true, nil
}

func closerSuggestion(left, right titleSuggestion) bool {
	if left.cost != right.cost {
		return left.cost < right.cost
	}
	if lt, rt := strings.ToLower(left.result.Title), strings.ToLower(right.result.Title); lt != rt {
		return lt < rt
	}
	if left.result.Folder != right.result.Folder {
		return left.result.Folder < right.result.Folder
	}
	return left.result.Key < right.result.Key
}
