package vaultgit

import (
	"errors"
	"strings"
	"time"
)

// autosaveMessage names an external-change snapshot. The timestamp is the only
// variable part and it is UTC to the second, so the message is identical on
// every machine that observes the same moment and reveals nothing about what
// changed.
func autosaveMessage(now time.Time) string {
	return "brainnotes: autosave " + now.UTC().Format("2006-01-02T15:04:05Z")
}

// journalMessage names a `bn` mutation snapshot.
//
// The operation token is supplied by the caller and is scrubbed to a short
// lowercase identifier here, never derived from the journal entry. That is not
// defensive habit: journal.Entry.Command holds the full `bn` invocation, which
// contains note names, property values, and search text. Putting it in a commit
// message would publish note content to every clone of the repository.
func journalMessage(operation, entryID string) string {
	return "brainnotes: " + sanitizeOperation(operation) + " [" + entryID + "]"
}

// sanitizeOperation reduces a caller-supplied token to lowercase letters,
// digits, and single dashes, falling back to a fixed word. It is an allow-list
// rather than an escape: anything unexpected is dropped, not encoded.
func sanitizeOperation(operation string) string {
	const fallback = "mutation"
	const maxLen = 32
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(operation)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ', r == '\t', r == '\n', r == '\r':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		if b.Len() >= maxLen {
			break
		}
	}
	token := strings.Trim(b.String(), "-")
	if token == "" {
		return fallback
	}
	return token
}

// validEntryID checks that an entry ID is a plain identifier before it reaches
// a commit message or a path. Journal IDs are generated as timestamp-plus-hex,
// so anything else means the caller is passing something it should not.
func validEntryID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("a journal entry id is required")
	}
	if len(id) > 128 {
		return errors.New("the journal entry id is too long to be one")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return errors.New("the journal entry id contains characters a journal id never has")
		}
	}
	if strings.Contains(id, "..") {
		return errors.New("the journal entry id is not a plain identifier")
	}
	return nil
}
