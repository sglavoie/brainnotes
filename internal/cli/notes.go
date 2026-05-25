package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// resolveNoteInputs turns positional note arguments into a deduplicated list of
// absolute paths within vaultRoot. If args is exactly ["-"], paths are read
// from in instead (one per line, or NUL-separated when nullSep is true).
// Globs are expanded with filepath.Glob; relative patterns are resolved
// against vaultRoot before expansion. An empty match for a glob is an error.
// Out-of-vault paths are rejected.
func resolveNoteInputs(vaultRoot string, args []string, in io.Reader, nullSep bool) ([]string, error) {
	if len(args) == 1 && args[0] == "-" {
		return readStdinPaths(in, vaultRoot, nullSep)
	}

	var paths []string
	seen := make(map[string]struct{})
	for _, a := range args {
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
		for _, m := range matches {
			abs, err := absInVault(vaultRoot, m)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[abs]; ok {
				continue
			}
			seen[abs] = struct{}{}
			paths = append(paths, abs)
		}
	}
	return paths, nil
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
	// resolving the parent directory so symlinked directories are still caught.
	checkPath := abs
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		checkPath = resolved
	} else if resolvedDir, evalErr := filepath.EvalSymlinks(filepath.Dir(abs)); evalErr == nil {
		checkPath = filepath.Join(resolvedDir, filepath.Base(abs))
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
	seen := make(map[string]struct{})
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
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		paths = append(paths, abs)
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
