package web

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	searchResultLimit  = 50
	searchSnippetRunes = 80
)

type searchResult struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Wikilink is the target text a link to this note carries, without the
	// brackets. It is published so there is one derivation of the target rather
	// than three: Key is folded to lower case, and Title is the *filename stem*
	// (vault.Note.Title, not the frontmatter), so reassembling Folder + "/" +
	// Title is both a reassembly and a subtly different answer — Title applies
	// textnorm.NFC while noteVaultPath does not, so the two disagree on a
	// decomposed filename. The raw-Markdown editor's completion is the first
	// caller.
	Wikilink      string   `json:"wikilink"`
	Folder        string   `json:"folder"`
	Path          string   `json:"path"`
	Snippet       string   `json:"snippet"`
	Pinned        bool     `json:"pinned"`
	Position      int      `json:"position,omitempty"` // One-based position when rechecking a selection.
	Modified      string   `json:"modified,omitempty"`
	MatchedFields []string `json:"matched_fields,omitempty"`
	Issues        []string `json:"issues,omitempty"`
}

type searchCandidate struct {
	snippetBody string
	result      searchResult
	// note is the note this row was built from, kept so an ordering can run
	// over notes and the rows can be permuted to match.
	note        *vault.Note
	titleMatch  bool
	lowerTitle  string
	lowerFolder string
}

type searchIssuePredicate struct {
	name  string
	match query.Predicate
}

type searchResponse struct {
	Results []searchResult
	Total   int
}

func buildSearchResponse(snapshot vaultSnapshot, root string, filters noteFilters, shape resultShape, rank searchRelevanceFunc) (searchResponse, error) {
	if snapshot.graph == nil {
		return searchResponse{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return searchResponse{}, errorsf("empty vault root")
	}

	if filters.foldedQuery == "" && !filters.hasNonTextFilters() {
		return searchResponse{Results: []searchResult{}}, nil
	}

	candidates, err := collectSearchCandidates(snapshot, root, filters)
	if err != nil {
		return searchResponse{}, err
	}

	// Best-match-first is the ranking a *search* wants, so it stays the default.
	// An explicit ordering — typed into the query bar or carried by a saved
	// view's sort= — replaces it wholesale rather than tie-breaking against it,
	// which is what makes `sort=-modified` here mean the same as `bn find --sort
	// -modified`.
	rankSearchCandidates(candidates)
	if shape.ordered() {
		candidates, err = orderSearchCandidates(candidates, shape, query.OrderInput{
			Root:  root,
			Notes: snapshot.notes,
			Graph: snapshot.graph,
		}, rank)
		if err != nil {
			return searchResponse{}, err
		}
	}
	return searchResponse{
		Results: searchResults(limitSearchCandidates(candidates, shape), filters.foldedQuery),
		Total:   len(candidates),
	}, nil
}

// collectSearchCandidates builds one row per note the filters admit, paired
// with the note it came from so an ordering can run over notes and permute the
// rows to match.
func collectSearchCandidates(snapshot vaultSnapshot, root string, filters noteFilters) ([]searchCandidate, error) {
	issuePredicates := []searchIssuePredicate{
		{name: "broken", match: query.BrokenLinks(snapshot.graph)},
		{name: "dead_end", match: query.DeadEnd(snapshot.graph)},
		{name: "orphan", match: query.Orphan(snapshot.graph)},
	}

	pinned := query.Pinned()

	candidates := make([]searchCandidate, 0, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}

		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}

		match, matched, err := matchNoteWithFilters(note, filters)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}

		title := note.Title()
		folder, err := note.Folder(root)
		if err != nil {
			return nil, fmt.Errorf("folder for %q: %w", key, err)
		}
		path, err := notePathForKey(key)
		if err != nil {
			return nil, fmt.Errorf("path for %q: %w", key, err)
		}

		candidates = append(candidates, searchCandidate{
			snippetBody: match.Body,
			note:        note,
			result: searchResult{
				Key:           key,
				Title:         title,
				Wikilink:      wikilinkTargetForNote(root, note),
				Folder:        folder,
				Path:          path,
				Pinned:        pinned(note),
				Modified:      searchModified(note),
				MatchedFields: searchMatchedFields(match, filters.foldedQuery != ""),
				Issues:        searchIssueFlags(note, issuePredicates),
			},
			titleMatch:  match.TitleMatch,
			lowerTitle:  strings.ToLower(title),
			lowerFolder: strings.ToLower(folder),
		})
	}

	return candidates, nil
}

// searchResults strips the rows out of the candidates, in order.
func searchResults(candidates []searchCandidate, query string) []searchResult {
	results := make([]searchResult, 0, len(candidates))
	for _, candidate := range candidates {
		result := candidate.result
		// Render excerpts only for this page, never for thousands of omitted matches.
		result.Snippet = makeSearchSnippet(candidate.snippetBody, query)
		results = append(results, result)
	}
	return results
}

// rankSearchCandidates orders the matches best-first: title matches, then title,
// folder, and key alphabetically as the deterministic tie-break.
func rankSearchCandidates(candidates []searchCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.titleMatch != right.titleMatch {
			return left.titleMatch
		}
		if left.lowerTitle != right.lowerTitle {
			return left.lowerTitle < right.lowerTitle
		}
		if left.lowerFolder != right.lowerFolder {
			return left.lowerFolder < right.lowerFolder
		}
		return left.result.Key < right.result.Key
	})
}

// limitSearchCandidates caps the payload. It must run after the ordering:
// truncating a merely title-sorted list would keep the alphabetically first 50
// rather than the 50 best matches, and truncating before a view's sort would
// order 50 arbitrary matches instead of returning the top N the view saved.
func limitSearchCandidates(candidates []searchCandidate, shape resultShape) []searchCandidate {
	end := len(candidates)
	if shape.Limit > 0 && shape.Limit < end {
		end = shape.Limit
	}
	if len(shape.Selected) > 0 {
		return selectedSearchCandidates(candidates[:end], shape.Selected)
	}
	start := min(shape.Offset, end)
	// Subtract first so even a maximal integer offset cannot overflow.
	return candidates[start : start+min(searchResultLimit, end-start)]
}

// Revalidation respects the query limit before looking up keys, and never
// expands the search. Its payload stays bounded by the selected-key limit.
func selectedSearchCandidates(candidates []searchCandidate, keys []string) []searchCandidate {
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	selected := make([]searchCandidate, 0, len(keys))
	for index, candidate := range candidates {
		if wanted[candidate.result.Key] {
			candidate.result.Position = index + 1
			selected = append(selected, candidate)
		}
	}
	return selected
}

func searchMatchedFields(match noteTextMatch, hasQuery bool) []string {
	if !hasQuery {
		return nil
	}
	var fields []string
	if match.TitleMatch {
		fields = append(fields, "title")
	}
	if match.BodyMatchIndex >= 0 {
		fields = append(fields, "body")
	}
	return fields
}

func searchIssueFlags(note *vault.Note, issuePredicates []searchIssuePredicate) []string {
	var issues []string
	for _, issue := range issuePredicates {
		if issue.match(note) {
			issues = append(issues, issue.name)
		}
	}
	return issues
}
