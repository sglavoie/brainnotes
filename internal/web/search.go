package web

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	searchResultLimit  = 50
	searchSnippetRunes = 80
)

type searchResult struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Wikilink is the target text a link to this note carries, without the
	// brackets. It is published because a caller that wants to *write* a link
	// cannot derive it from the other fields: Key is folded to lower case and
	// Title is whatever the frontmatter says, while the graph resolves the
	// exact-case path. The raw-Markdown editor's completion is the first caller.
	Wikilink      string   `json:"wikilink"`
	Folder        string   `json:"folder"`
	Path          string   `json:"path"`
	Snippet       string   `json:"snippet"`
	Pinned        bool     `json:"pinned"`
	MatchedFields []string `json:"matched_fields,omitempty"`
	Issues        []string `json:"issues,omitempty"`
}

type searchCandidate struct {
	result searchResult
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

func buildSearchResponse(snapshot vaultSnapshot, root string, filters noteFilters, shape resultShape, rank searchRelevanceFunc) ([]searchResult, error) {
	if snapshot.graph == nil {
		return nil, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errorsf("empty vault root")
	}

	if filters.foldedQuery == "" && !filters.hasNonTextFilters() {
		return []searchResult{}, nil
	}

	candidates, err := collectSearchCandidates(snapshot, root, filters)
	if err != nil {
		return nil, err
	}

	// Best-match-first is the ranking a *search* wants, so it stays the default.
	// An explicit ordering — typed into the query bar or carried by a saved
	// view's sort= — replaces it wholesale rather than tie-breaking against it,
	// which is what makes `sort=-modified` here mean the same as `bn find --sort
	// -modified`.
	rankSearchCandidates(candidates)
	if shape.ordered() {
		candidates, err := orderSearchCandidates(candidates, shape, query.OrderInput{
			Root:  root,
			Notes: snapshot.notes,
			Graph: snapshot.graph,
		}, rank)
		if err != nil {
			return nil, err
		}
		return searchResults(limitSearchCandidates(candidates, shape)), nil
	}
	candidates = limitSearchCandidates(candidates, shape)

	return searchResults(candidates), nil
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
			note: note,
			result: searchResult{
				Key:           key,
				Title:         title,
				Wikilink:      wikilinkTargetForNote(root, note),
				Folder:        folder,
				Path:          path,
				Snippet:       html.EscapeString(makeSearchSnippet(match.Body, match.BodyMatchIndex)),
				Pinned:        pinned(note),
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
func searchResults(candidates []searchCandidate) []searchResult {
	results := make([]searchResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.result)
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
	if limit := shape.effectiveLimit(); len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
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

func makeSearchSnippet(body string, bodyMatchIndex int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	runes := []rune(body)
	if len(runes) == 0 {
		return ""
	}

	start := 0
	if bodyMatchIndex > 0 {
		if bodyMatchIndex > len(body) {
			bodyMatchIndex = len(body)
		}
		matchRuneIndex := utf8.RuneCountInString(body[:bodyMatchIndex])
		start = matchRuneIndex - (searchSnippetRunes / 2)
		if start < 0 {
			start = 0
		}
	}

	end := len(runes)
	if len(runes) > searchSnippetRunes {
		if start+searchSnippetRunes > len(runes) {
			start = len(runes) - searchSnippetRunes
		}
		end = start + searchSnippetRunes
	}

	snippet := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if snippet == "" {
		return ""
	}

	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}

	return snippet
}
