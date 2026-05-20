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
var ErrFZFNotFound = errors.New("fzf not found; install via 'brew install fzf' or provide arguments explicitly")

// RunFZF pipes paths into fzf, one per line, and returns the user's selection.
// extraArgs is forwarded verbatim — typically from cfg.Fzf.Args (preview,
// height, etc.). A user-aborted fzf (exit code 130) returns (nil, nil) so the
// command can exit cleanly. fzf's stderr is forwarded to the parent so the
// TUI renders in the user's terminal.
func RunFZF(paths []string, extraArgs []string) ([]string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return nil, ErrFZFNotFound
	}
	args := append([]string(nil), extraArgs...)
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n"))
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 130 {
			return nil, nil
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
