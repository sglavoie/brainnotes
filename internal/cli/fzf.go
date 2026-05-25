package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrFZFNotFound is returned when the fzf binary cannot be located in PATH.
// It wraps exec.ErrNotFound so that callers (including the main exit-code
// mapper) can detect it with errors.Is and map it to exit code 127.
var ErrFZFNotFound = fmt.Errorf("fzf not found; install via 'brew install fzf' or provide arguments explicitly: %w", exec.ErrNotFound)

// ErrFZFInterrupted is returned when the user cancels fzf with Ctrl-C or
// Escape (exit code 130). Callers should exit with code 130 without printing
// an error message, preserving the standard shell interrupt convention.
var ErrFZFInterrupted = errors.New("fzf: interrupted by user")

var fzfLookPath = exec.LookPath

var fzfCommand = exec.Command

// RunFZF pipes paths into fzf, one per line, and returns the user's selection.
// extraArgs is forwarded verbatim — typically from cfg.Fzf.Args (preview,
// height, etc.). An empty fzf result (exit code 1 — no match) returns
// (nil, nil). A user-cancelled fzf (exit code 130 — Ctrl-C/Escape) returns
// (nil, ErrFZFInterrupted) so callers can exit with code 130. fzf's stderr is
// forwarded to the parent so the TUI renders in the user's terminal.
func RunFZF(paths []string, extraArgs []string) ([]string, error) {
	if _, err := fzfLookPath("fzf"); err != nil {
		return nil, ErrFZFNotFound
	}
	args := append([]string(nil), extraArgs...)
	cmd := fzfCommand("fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n"))
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case 1:
				return nil, nil
			case 130:
				return nil, ErrFZFInterrupted
			}
		}
		return nil, fmt.Errorf("fzf: %w", err)
	}
	return splitSelection(out.String()), nil
}

func splitSelection(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
