package web

import (
	"fmt"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type noteTextMatch struct {
	TitleMatch     bool
	Body           string
	BodyMatchIndex int
}

func foldSearchQuery(queryText string) string {
	return strings.ToLower(strings.TrimSpace(queryText))
}

func matchNoteText(note *vault.Note, foldedQuery string) (noteTextMatch, bool, error) {
	if note == nil || foldedQuery == "" {
		return noteTextMatch{}, false, nil
	}

	titleMatch := strings.Contains(strings.ToLower(note.Title()), foldedQuery)

	body, err := note.Body()
	if err != nil {
		return noteTextMatch{}, false, fmt.Errorf("body for %q: %w", note.Path, err)
	}
	bodyMatchIndex := strings.Index(strings.ToLower(body), foldedQuery)

	match := noteTextMatch{
		TitleMatch:     titleMatch,
		Body:           body,
		BodyMatchIndex: bodyMatchIndex,
	}
	return match, titleMatch || bodyMatchIndex >= 0, nil
}
