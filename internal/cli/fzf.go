package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

// RunFZFLines pipes grep-style records into fzf and returns the selected note
// paths (absolute, deduplicated). Records must be tab-separated as:
//
//	absPath\tlineNum\trelPath\tbodyContent
//
// The displayed row is transformed to "relpath:linenum:\tbodyContent".
//
// Injects the following fzf args unless already present in extraArgs:
//
//	--delimiter \t
//	--with-nth {3}:{2}:\t{4..}  (show path, line number, and body)
//	--nth 2..             (match only body content, not paths)
//	--tabstop 1           (render the visible body delimiter compactly)
//	--no-hscroll          (clip long rows instead of horizontally shifting them)
//	--preview cat {1}     (full note in preview pane)
//	--preview-window right:60%:+{2}/3   (scroll to matched line)
//
// Returns (nil, nil) on fzf exit 1 (no match) and
// (nil, ErrFZFInterrupted) on exit 130 (Ctrl-C/Escape).
func RunFZFLines(lines []string, extraArgs []string) ([]string, error) {
	selected, err := runFZFLinesRaw(lines, extraArgs)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, line := range selected {
		parts := strings.SplitN(line, "\t", 2)
		path := parts[0]
		if _, ok := seen[path]; !ok {
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths, nil
}

type fzfLineSelection struct {
	path string
	line int
}

func runFZFLinesSelections(lines []string, extraArgs []string) ([]fzfLineSelection, error) {
	selected, err := runFZFLinesRaw(lines, extraArgs)
	if err != nil {
		return nil, err
	}
	out := make([]fzfLineSelection, 0, len(selected))
	for _, row := range selected {
		parts := strings.SplitN(row, "\t", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("fzf: invalid search selection %q", row)
		}
		lineNum, err := strconv.Atoi(parts[1])
		if err != nil || lineNum <= 0 {
			return nil, fmt.Errorf("fzf: invalid search selection line %q", parts[1])
		}
		out = append(out, fzfLineSelection{path: parts[0], line: lineNum})
	}
	return out, nil
}

func runFZFLinesRaw(lines []string, extraArgs []string) ([]string, error) {
	if _, err := fzfLookPath("fzf"); err != nil {
		return nil, ErrFZFNotFound
	}

	hasFlag := func(flag string) bool {
		for _, a := range extraArgs {
			if a == flag || strings.HasPrefix(a, flag+"=") {
				return true
			}
		}
		return false
	}

	hasWithNth := hasFlag("--with-nth")
	hasNth := hasFlag("--nth")

	var injected []string
	if !hasFlag("--delimiter") {
		injected = append(injected, "--delimiter", "\t")
	}
	if !hasWithNth {
		injected = append(injected, "--with-nth", "{3}:{2}:\t{4..}")
	}
	if !hasNth && !hasWithNth {
		injected = append(injected, "--nth", "2..")
	}
	if !hasFlag("--tabstop") {
		injected = append(injected, "--tabstop", "1")
	}
	if !hasFlag("--no-hscroll") && !hasFlag("--hscroll-off") {
		injected = append(injected, "--no-hscroll")
	}
	if !hasFlag("--preview") {
		injected = append(injected, "--preview", "cat {1}")
	}
	if !hasFlag("--preview-window") {
		injected = append(injected, "--preview-window", "right:60%:+{2}/3")
	}
	args := append(injected, extraArgs...)

	cmd := fzfCommand("fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
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
