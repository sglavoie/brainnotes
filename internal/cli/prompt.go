package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
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

// stdinIsTerminal is the terminal test confirmTerminal applies to stdin. Tests
// swap it to answer a prompt from a buffer.
var stdinIsTerminal = isTerminal

// confirmTerminal is the one rule for a destructive command's confirmation:
// ask on a terminal, and refuse without one. A script cannot answer a prompt,
// and a piped "y" is too easy to supply by accident (`yes | bn ...`), so the
// refusal names the flag that skips the question instead of reading stdin.
// A declined prompt is canceled(name), exit 1; a refusal is an error, exit 2.
func confirmTerminal(cmd *cobra.Command, name, question, refusal string) error {
	in := cmd.InOrStdin()
	if !stdinIsTerminal(in) {
		return fmt.Errorf("%s: %s", name, refusal)
	}
	ok, err := confirm(in, cmd.ErrOrStderr(), name, question)
	if err != nil {
		return err
	}
	if !ok {
		return canceled(name)
	}
	return nil
}

// isTerminal reports whether r is an interactive terminal. Only an *os.File
// can be one; a buffer a test supplies never is, and neither is /dev/null,
// which a character-device check would accept.
func isTerminal(r io.Reader) bool {
	return fileIsTerminal(r)
}

// stdoutIsTerminal is isTerminal for an output stream: decoration meant for a
// person (a header row) is printed only when w is a terminal, so a pipeline
// reads records and nothing else. A variable so tests can force a terminal.
var stdoutIsTerminal = func(w io.Writer) bool {
	return fileIsTerminal(w)
}

// fileIsTerminal is the one terminal test both streams share.
func fileIsTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
