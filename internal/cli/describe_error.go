package cli

import (
	"errors"
	"strings"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

// describeError spells err for the terminal. It is err.Error() except that a
// *noteops.Error's cause is restored: noteops keeps its Message short and
// public for web responses ("frontmatter: load note"), which on a terminal
// drops the path and the reason. The cause is spliced in after the message, or
// replaces it when the cause already repeats the message.
func describeError(err error) string {
	text := err.Error()
	var opErr *noteops.Error
	if !errors.As(err, &opErr) || opErr.Err == nil || opErr.Message == "" {
		return text
	}
	cause := describeError(opErr.Err)
	if strings.Contains(text, cause) {
		return text
	}
	full := opErr.Message + ": " + cause
	if strings.Contains(cause, opErr.Message) {
		full = cause
	}
	idx := strings.LastIndex(text, opErr.Message)
	if idx < 0 {
		return text + ": " + cause
	}
	return text[:idx] + full + text[idx+len(opErr.Message):]
}
