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
	return runFZFProcess(append([]string(nil), extraArgs...), strings.Join(paths, "\n"))
}

// runFZFProcess is the single place fzf is spawned and its exit code mapped, so
// the no-match and interrupt conventions cannot drift between the two entry
// points. stderr is forwarded to the parent so the TUI renders in the user's
// terminal.
func runFZFProcess(args []string, input string) ([]string, error) {
	cmd := fzfCommand("fzf", args...)
	cmd.Stdin = strings.NewReader(input)
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

// fzfDefault is one fzf flag this CLI injects unless the user's own
// `[fzf].args` already covers it. suppressedBy lists every flag whose presence
// makes the injection unnecessary — usually just the flag being injected, but
// `--nth` is also covered by `--with-nth` and `--no-hscroll` by
// `--hscroll-off`.
type fzfDefault struct {
	suppressedBy []string
	args         []string
}

// injectFZFDefaults prepends the defaults the user did not already set and
// returns the full argv. The user's own args come last so they win on any flag
// fzf resolves last-one-wins.
func injectFZFDefaults(extraArgs []string, defaults []fzfDefault) []string {
	var injected []string
	for _, d := range defaults {
		if anyFZFFlagSet(extraArgs, d.suppressedBy) {
			continue
		}
		injected = append(injected, d.args...)
	}
	return append(injected, extraArgs...)
}

func anyFZFFlagSet(extraArgs []string, flags []string) bool {
	for _, flag := range flags {
		if fzfHasFlag(extraArgs, flag) {
			return true
		}
	}
	return false
}

// fzfHasFlag reports whether extraArgs sets flag, in either the `--flag value`
// or the `--flag=value` form.
func fzfHasFlag(extraArgs []string, flag string) bool {
	for _, a := range extraArgs {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
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
	return runFZFProcess(fzfLineArgs(extraArgs), strings.Join(lines, "\n"))
}

// fzfLineArgs is the grep-style record layout documented on RunFZFLines. The
// order of the table is the order the flags are injected in.
func fzfLineArgs(extraArgs []string) []string {
	return injectFZFDefaults(extraArgs, []fzfDefault{
		{suppressedBy: []string{"--delimiter"}, args: []string{"--delimiter", "\t"}},
		{suppressedBy: []string{"--with-nth"}, args: []string{"--with-nth", "{3}:{2}:\t{4..}"}},
		{suppressedBy: []string{"--nth", "--with-nth"}, args: []string{"--nth", "2.."}},
		{suppressedBy: []string{"--tabstop"}, args: []string{"--tabstop", "1"}},
		{suppressedBy: []string{"--no-hscroll", "--hscroll-off"}, args: []string{"--no-hscroll"}},
		{suppressedBy: []string{"--preview"}, args: []string{"--preview", "cat {1}"}},
		{suppressedBy: []string{"--preview-window"}, args: []string{"--preview-window", "right:60%:+{2}/3"}},
	})
}
