package web

import (
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Match query.OrderModified: only a parsed frontmatter timestamp counts, not
// the filesystem mtime or an unparseable string. Preserve date-only spelling.
func searchModified(note *vault.Note) string {
	front, err := note.Front()
	if err != nil {
		return ""
	}
	stamp, ok := front.GetTime("last")
	if !ok {
		return ""
	}
	if front.TimeIsDateOnly("last") {
		return stamp.Format("2006-01-02")
	}
	return stamp.Format(time.RFC3339Nano)
}
