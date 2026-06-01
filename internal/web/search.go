package web

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	searchResultLimit  = 50
	searchSnippetRunes = 80
)

type searchResult struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Folder  string `json:"folder"`
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

type searchCandidate struct {
	result      searchResult
	titleMatch  bool
	lowerTitle  string
	lowerFolder string
}

func buildSearchResponse(snapshot vaultSnapshot, root, queryText string) ([]searchResult, error) {
	if snapshot.graph == nil {
		return nil, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errorsf("empty vault root")
	}

	queryFolded := foldSearchQuery(queryText)
	if queryFolded == "" {
		return []searchResult{}, nil
	}

	candidates := make([]searchCandidate, 0, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}

		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}

		match, matched, err := matchNoteText(note, queryFolded)
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
			result: searchResult{
				Key:     key,
				Title:   title,
				Folder:  folder,
				Path:    path,
				Snippet: html.EscapeString(makeSearchSnippet(match.Body, match.BodyMatchIndex)),
			},
			titleMatch:  match.TitleMatch,
			lowerTitle:  strings.ToLower(title),
			lowerFolder: strings.ToLower(folder),
		})
	}

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

	if len(candidates) > searchResultLimit {
		candidates = candidates[:searchResultLimit]
	}

	results := make([]searchResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.result)
	}
	return results, nil
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
