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
// vaultRoot. The check uses filepath.Rel so symlinks pointing outside are not
// detected; that is a deliberate trade-off matching the rest of the codebase.
func absInVault(vaultRoot, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", p, err)
	}
	rel, err := filepath.Rel(vaultRoot, abs)
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
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
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

// containsString reports whether x is in s.
func containsString(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
