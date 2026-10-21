package query

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// FoldText returns the canonical comparison form of s: surrounding whitespace
// trimmed, the result normalized to NFC, then lowercased.
//
// The NFC step is load-bearing and is why this helper exists rather than a bare
// strings.ToLower. macOS stores filenames in NFD while editors write wikilinks
// and note bodies in NFC, so "Café" typed by a user and "Café" read off disk can
// be byte-different renderings of the same text. Folding both sides through this
// function makes accented text compare equal regardless of on-disk form.
//
// Order matters: NFC before ToLower, because composition can change which runes
// exist before case mapping is applied. TrimSpace runs first so the trimmed
// bytes never participate in normalization.
//
// Callers that need byte offsets into the *original* string must not use
// FoldText, because trimming shifts every index; fold only the needle and
// normalize the haystack separately.
func FoldText(s string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(s)))
}

// Text matches notes whose title or body contains folded, which the caller must
// have already passed through FoldText. Taking a pre-folded needle keeps the
// per-note cost to one fold of the note text instead of re-folding the query
// once per note.
//
// An empty needle matches every non-nil note, so a text term contributes nothing
// to a conjunction rather than emptying it — the same identity convention And
// uses with no arguments. A note whose body cannot be read is matched on its
// title alone.
func Text(folded string) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		if folded == "" {
			return true
		}
		if strings.Contains(FoldText(n.Title()), folded) {
			return true
		}
		body, err := n.Body()
		if err != nil {
			return false
		}
		return strings.Contains(FoldText(body), folded)
	}
}
