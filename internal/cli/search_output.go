package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func buildMatcher(pattern string, isRegex, caseSensitive bool) (func(string) bool, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("search: pattern must not be empty")
	}
	if isRegex {
		rePattern := pattern
		if !caseSensitive {
			rePattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(rePattern)
		if err != nil {
			return nil, fmt.Errorf("search: invalid regex %q: %w", pattern, err)
		}
		return re.MatchString, nil
	}
	if caseSensitive {
		return func(s string) bool {
			return strings.Contains(s, pattern)
		}, nil
	}
	lower := strings.ToLower(pattern)
	return func(s string) bool {
		return strings.Contains(strings.ToLower(s), lower)
	}, nil
}

type searchMatch struct {
	path string
	line int
}

func firstMatchingLine(path string, matcher func(string) bool) (int, bool, error) {
	if matcher == nil {
		return 0, false, nil
	}
	fileLines, bodyStartIdx, err := readBodyLines(path)
	if err != nil {
		return 0, false, err
	}
	if len(fileLines) == 0 || bodyStartIdx >= len(fileLines) {
		return 0, false, nil
	}
	for i := bodyStartIdx; i < len(fileLines); i++ {
		if matcher(fileLines[i]) {
			return i + 1, true, nil
		}
	}
	return 0, false, nil
}

// buildHighlightPreview returns a grep --preview command. 'PATTERN|$' shows
// every line while coloring matches, preserving line count for +{2}/3 scroll.
func buildHighlightPreview(pattern string, isRegex, caseSensitive bool) string {
	p := pattern
	if !isRegex {
		p = regexp.QuoteMeta(p)
	}
	p = strings.ReplaceAll(p, "'", `'\''`)
	flags := "--color=always -n -E"
	if !caseSensitive {
		flags = "--color=always -n -iE"
	}
	return fmt.Sprintf("grep %s -- '%s|$' {1}", flags, p)
}

func buildLiveQueryHighlightPreview() string {
	// Keep {q} as its own shell argument. fzf shell-quotes placeholders, so
	// embedding {q} inside another quoted string makes grep search for literal
	// quote characters instead of the live query. Do not put a bare "{}" in
	// this command: fzf expands it as the current item before the shell runs.
	return `sh -c 'q=$(printf "%s\n" "$1" | sed '"'"'s/[][()}.^$?+*|\\{]/\\&/g'"'"'); grep --color=always -n -iE -- "$q|$" "$2" 2>/dev/null || cat "$2"' sh {q} {1}`
}

// window is an inclusive 0-based file-line index range.
type window struct{ lo, hi int }

// mergeWindows sorts windows by lo and merges overlapping or adjacent ones.
func mergeWindows(wins []window) []window {
	if len(wins) == 0 {
		return nil
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].lo < wins[j].lo })
	merged := []window{wins[0]}
	for _, w := range wins[1:] {
		last := &merged[len(merged)-1]
		if w.lo <= last.hi+1 {
			if w.hi > last.hi {
				last.hi = w.hi
			}
		} else {
			merged = append(merged, w)
		}
	}
	return merged
}

// readBodyLines reads the file at path, splits it into lines (dropping a
// trailing empty string), and returns the 0-based index of the first body
// line (the line after the closing "---" of the frontmatter, or 0 if there
// is no frontmatter).
func readBodyLines(path string) (lines []string, bodyStart int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	lines = strings.Split(string(raw), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil, 0, nil
	}
	if strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				bodyStart = i + 1
				break
			}
		}
	}
	return lines, bodyStart, nil
}

// contextOutput writes grep-style context output for a single note to out.
// It reads the raw file, skips frontmatter, finds matches, builds context
// windows (clamped to body), merges overlapping windows, and emits them
// with "--" separators between non-adjacent blocks.
func contextOutput(out io.Writer, n *vault.Note, vaultRoot string, match func(string) bool, contextN int) error {
	fileLines, bodyStartIdx, err := readBodyLines(n.Path)
	if err != nil {
		return err
	}
	if len(fileLines) == 0 || bodyStartIdx >= len(fileLines) {
		return nil
	}

	// Collect indices of matching lines.
	var matchIdxs []int
	for i := bodyStartIdx; i < len(fileLines); i++ {
		if match(fileLines[i]) {
			matchIdxs = append(matchIdxs, i)
		}
	}
	if len(matchIdxs) == 0 {
		return nil
	}

	// Build context windows clamped to body range.
	last := len(fileLines) - 1
	wins := make([]window, len(matchIdxs))
	for k, idx := range matchIdxs {
		lo := idx - contextN
		if lo < bodyStartIdx {
			lo = bodyStartIdx
		}
		hi := idx + contextN
		if hi > last {
			hi = last
		}
		wins[k] = window{lo, hi}
	}
	wins = mergeWindows(wins)

	relPath := n.Path
	if rel, err2 := filepath.Rel(vaultRoot, n.Path); err2 == nil {
		relPath = filepath.ToSlash(rel)
	}

	for i, w := range wins {
		if i > 0 {
			fmt.Fprintln(out, "--")
		}
		for lineIdx := w.lo; lineIdx <= w.hi; lineIdx++ {
			lineNum := lineIdx + 1
			text := truncateSnippet(fileLines[lineIdx])
			if match(fileLines[lineIdx]) {
				fmt.Fprintf(out, "%s:%d:%s\n", relPath, lineNum, text)
			} else {
				fmt.Fprintf(out, "%s-%d-%s\n", relPath, lineNum, text)
			}
		}
	}
	return nil
}

// buildInteractiveLines builds the fzf input corpus for interactive search.
// Each element is a tab-separated record:
//
//	absPath\tfileLineNum\trelpath\tbodyLine
//
// where fileLineNum is 1-based. If matcher is nil, all body lines are included;
// otherwise only lines for which matcher returns true are included. Notes with
// empty bodies are skipped.
func buildInteractiveLines(
	notes []*vault.Note,
	vaultRoot string,
	matcher func(string) bool,
) ([]string, error) {
	var result []string
	for _, n := range notes {
		lines, bodyStart, err := readBodyLines(n.Path)
		if err != nil {
			return nil, err
		}
		if bodyStart >= len(lines) {
			continue
		}
		relPath := n.Path
		if rel, err2 := filepath.Rel(vaultRoot, n.Path); err2 == nil {
			relPath = filepath.ToSlash(rel)
		}
		for i := bodyStart; i < len(lines); i++ {
			line := lines[i]
			if matcher != nil && !matcher(line) {
				continue
			}
			fileLineNum := i + 1
			displayPath := strings.ReplaceAll(relPath, "\t", "    ")
			result = append(result, fmt.Sprintf("%s\t%d\t%s\t%s", n.Path, fileLineNum, displayPath, line))
		}
	}
	return result, nil
}
