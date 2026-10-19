package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirm asks question on errOut and accepts only a complete y/yes answer.
// The prompt goes to stderr so it never lands in captured stdout. EOF is a
// refusal: a non-interactive invocation must not confirm by accident.
func confirm(in io.Reader, errOut io.Writer, label, question string) (bool, error) {
	if _, err := fmt.Fprintf(errOut, "%s [y/N] ", question); err != nil {
		return false, err
	}
	if in == nil {
		return false, nil
	}
	response, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("%s: read confirmation: %w", label, err)
	}
	response = strings.TrimSpace(response)
	return strings.EqualFold(response, "y") || strings.EqualFold(response, "yes"), nil
}

// isTerminal reports whether r is an interactive terminal. Only an *os.File
// can be one; a buffer a test supplies never is, and neither is /dev/null,
// which a character-device check would accept.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
