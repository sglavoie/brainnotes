package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// noteInputSet collapses note arguments that name the same file. A raw string
// comparison misses `Note.md` and `note.md` on a case-insensitive volume, and a
// note listed under both spellings would be planned — and locked — twice.
// Candidates are bucketed by a case-folded, NFC key so the os.SameFile check
// only runs between spellings that could be one file; a path that does not
// exist yet is only ever equal to its exact spelling.
type noteInputSet struct {
	buckets map[string][]string
}

func newNoteInputSet() *noteInputSet {
	return &noteInputSet{buckets: make(map[string][]string)}
}

// add reports whether abs is new to the set, recording it when it is.
func (s *noteInputSet) add(abs string) bool {
	key := textnorm.NFC(textnorm.Fold(textnorm.NFC(abs)))
	for _, seen := range s.buckets[key] {
		if seen == abs || sameExistingFile(seen, abs) {
			return false
		}
	}
	s.buckets[key] = append(s.buckets[key], abs)
	return true
}

func sameExistingFile(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	return err == nil && os.SameFile(infoA, infoB)
}

// resolveNoteInputs turns positional note arguments into a deduplicated list of
// absolute paths within vaultRoot. If args is exactly ["-"], paths are read
// from in instead (one per line, or NUL-separated when nullSep is true).
// Globs are expanded with filepath.Glob; relative patterns are resolved
// against vaultRoot before expansion. An empty match for a glob is an error.
// Out-of-vault paths are rejected, and so is a literal path that names no
// file: it is reported under name as a *noteTargetNotFoundError, in the form
// the user typed it, rather than surfacing later as a failed read of an
// absolute path. Every other refusal is prefixed with name too.
func resolveNoteInputs(name, vaultRoot string, args []string, in io.Reader, nullSep bool) ([]string, error) {
	paths, err := collectNoteInputs(name, vaultRoot, args, in, nullSep)
	if err == nil {
		return paths, nil
	}
	var notFound *noteTargetNotFoundError
	if errors.As(err, &notFound) || name == "" {
		return nil, err
	}
	return nil, fmt.Errorf("%s: %w", name, err)
}

func collectNoteInputs(name, vaultRoot string, args []string, in io.Reader, nullSep bool) ([]string, error) {
	if len(args) == 1 && args[0] == "-" {
		return collectStdinNoteInputs(name, vaultRoot, in, nullSep)
	}
	var paths []string
	seen := newNoteInputSet()
	for _, a := range args {
		matches, err := resolveNoteArg(name, vaultRoot, a)
		if err != nil {
			return nil, err
		}
		for _, abs := range matches {
			if seen.add(abs) {
				paths = append(paths, abs)
			}
		}
	}
	return paths, nil
}

// collectStdinNoteInputs reads the `-` form and refuses a path naming no file.
func collectStdinNoteInputs(name, vaultRoot string, in io.Reader, nullSep bool) ([]string, error) {
	hintTerminalStdin(in)
	paths, err := readStdinPaths(in, vaultRoot, nullSep)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := requireNoteFile(name, displayVaultPath(vaultRoot, p), p); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// resolveNoteArg expands one positional note argument to in-vault absolute
// paths. A literal path (no glob meta-characters) must name an existing file.
func resolveNoteArg(name, vaultRoot, a string) ([]string, error) {
	if a == "-" {
		return nil, errors.New("'-' may only be used as the sole note argument")
	}
	if strings.TrimSpace(a) == "" {
		return nil, errors.New("empty note path argument")
	}
	matches, err := expandGlob(vaultRoot, a)
	if err != nil {
		return nil, err
	}
	literal := !strings.ContainsAny(a, "*?[")
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		abs, err := absInVault(vaultRoot, m)
		if err != nil {
			return nil, err
		}
		if literal {
			if err := requireNoteFile(name, a, abs); err != nil {
				return nil, err
			}
		}
		out = append(out, abs)
	}
	return out, nil
}

// requireNoteFile refuses a note path that does not exist, naming it as arg.
// Any other stat failure is left for the read that follows to report.
func requireNoteFile(name, arg, abs string) error {
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		return &noteTargetNotFoundError{name: name, arg: arg, pathOnly: true}
	}
	return nil
}

// displayVaultPath renders abs vault-relative with forward slashes when it
// lies under vaultRoot, and as given otherwise.
func displayVaultPath(vaultRoot, abs string) string {
	rel, err := filepath.Rel(vaultRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return filepath.ToSlash(rel)
}

// expandGlob returns the list of files matching pattern. A pattern without
// glob meta-characters is returned as a single-element slice so callers can
// treat globs and literal paths uniformly.
func expandGlob(vaultRoot, pattern string) ([]string, error) {
	p := pattern
	if !filepath.IsAbs(p) {
		p = filepath.Join(vaultRoot, filepath.FromSlash(p))
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{p}, nil
	}
	matches, err := filepath.Glob(p)
	if err != nil {
		return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no matches for glob %q", pattern)
	}
	return matches, nil
}

// absInVault returns the absolute form of p and verifies it lives inside
// vaultRoot. When the path already exists on disk, the full path (including
// the file itself) is resolved through symlinks so that a symlinked file
// inside the vault that targets a location outside is rejected. When the path
// does not yet exist (new note), the deepest existing ancestor is resolved and
// the lexical remainder is appended, which catches symlinked directories while
// still permitting new-file creation. The returned path retains the original
// (unresolved) form so it stays consistent with the vault root callers hold.
func absInVault(vaultRoot, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", p, err)
	}
	checkRoot := vaultRoot
	if resolvedRoot, evalErr := filepath.EvalSymlinks(vaultRoot); evalErr == nil {
		checkRoot = resolvedRoot
	}
	// If the path exists, resolve it fully (catches symlinked files pointing
	// outside the vault). If it doesn't exist yet (new note), fall back to
	// resolving the deepest existing ancestor so symlinked directories anywhere
	// in the destination path are still caught.
	checkPath := abs
	if resolved, evalErr := resolveExistingPath(abs); evalErr == nil {
		checkPath = resolved
	}
	rel, err := filepath.Rel(checkRoot, checkPath)
	if err != nil {
		return "", fmt.Errorf("rel %q: %w", abs, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the vault", abs)
	}
	return abs, nil
}

func resolveExistingPath(abs string) (string, error) {
	current := abs
	var suffix []string

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if len(suffix) == 0 {
				return resolved, nil
			}
			parts := make([]string, 0, 1+len(suffix))
			parts = append(parts, resolved)
			parts = append(parts, suffix...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			if len(suffix) == 0 {
				return current, nil
			}
			parts := make([]string, 0, 1+len(suffix))
			parts = append(parts, current)
			parts = append(parts, suffix...)
			return filepath.Join(parts...), nil
		}

		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

// readStdinPaths reads paths from in, one per line (or NUL-separated when
// nullSep is true), and resolves each to an absolute in-vault path. Blank
// lines are skipped; duplicates are collapsed in input order.
func readStdinPaths(in io.Reader, vaultRoot string, nullSep bool) ([]string, error) {
	if in == nil {
		return nil, errors.New("no stdin reader available")
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	if nullSep {
		scanner.Split(splitNull)
	}
	var paths []string
	seen := newNoteInputSet()
	for scanner.Scan() {
		// NUL-separated input is meant to preserve paths byte-for-byte (the
		// whole point of -0 is to support paths with spaces/newlines), so we
		// must not strip whitespace from the token. Line-separated input is
		// trimmed so CRLF and stray indentation don't make valid paths look
		// out-of-vault.
		var line string
		if nullSep {
			line = scanner.Text()
		} else {
			line = strings.TrimSpace(scanner.Text())
		}
		if line == "" {
			continue
		}
		// Resolve relative paths against vaultRoot, not CWD, so stdin
		// paths behave identically to positional-arg paths (which go
		// through expandGlob and are joined to vaultRoot).
		if !filepath.IsAbs(line) {
			line = filepath.Join(vaultRoot, filepath.FromSlash(line))
		}
		abs, err := absInVault(vaultRoot, line)
		if err != nil {
			return nil, err
		}
		if seen.add(abs) {
			paths = append(paths, abs)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return paths, nil
}

func usesStdinNoteInput(args []string) bool {
	return len(args) == 1 && args[0] == "-"
}

func splitNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// hintTerminalStdin says why `-` is waiting when stdin is a terminal rather
// than a pipe. Only a person at a terminal sees it.
func hintTerminalStdin(in io.Reader) {
	if isTerminal(in) {
		_, _ = fmt.Fprintln(os.Stderr, "reading note paths from stdin, one per line; press Ctrl-D to finish")
	}
}
