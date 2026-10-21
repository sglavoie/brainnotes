package web

import (
	"fmt"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
)

type noteTextMatch struct {
	TitleMatch bool
	// Body is the note body NFC-normalized, not the raw bytes on disk, so that
	// BodyMatchIndex indexes the string the snippet builder is handed.
	Body           string
	BodyMatchIndex int
}

// foldSearchQuery folds a /api/search query into the shared comparison form.
// The NFC step query.FoldText adds is why an accented title matches regardless
// of whether the browser sent it composed and the filename is decomposed (as
// macOS writes it) or the other way around.
func foldSearchQuery(queryText string) string {
	return query.FoldText(queryText)
}

// matchNoteText reports whether note matches an already-folded query, returning
// the body and the byte offset of the body hit so the caller can build a
// snippet. Both sides of every comparison are normalized to NFC; the body is
// normalized rather than folded through query.FoldText because trimming would
// shift BodyMatchIndex off the string the snippet is cut from.
func matchNoteText(note *vault.Note, foldedQuery string) (noteTextMatch, bool, error) {
	if note == nil || foldedQuery == "" {
		return noteTextMatch{}, false, nil
	}

	titleMatch := strings.Contains(query.FoldText(note.Title()), foldedQuery)

	body, err := note.Body()
	if err != nil {
		return noteTextMatch{}, false, fmt.Errorf("body for %q: %w", note.Path, err)
	}
	body = textnorm.NFC(body)
	bodyMatchIndex := strings.Index(strings.ToLower(body), foldedQuery)

	match := noteTextMatch{
		TitleMatch:     titleMatch,
		Body:           body,
		BodyMatchIndex: bodyMatchIndex,
	}
	return match, titleMatch || bodyMatchIndex >= 0, nil
}
