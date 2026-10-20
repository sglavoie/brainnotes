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

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// buildMatcher returns the line predicate for a search pattern. Pattern and line
// are both compared in NFC, the form queryexpr normalizes query text to, so a
// pattern typed in decomposed form (or a note saved that way, as macOS file
// names and some editors do) still matches the same visible text.
func buildMatcher(pattern string, isRegex, caseSensitive bool) (func(string) bool, error) {
	match, err := buildNFCMatcher(textnorm.NFC(pattern), isRegex, caseSensitive)
	if err != nil {
		return nil, err
	}
	return func(s string) bool { return match(textnorm.NFC(s)) }, nil
}

func buildNFCMatcher(pattern string, isRegex, caseSensitive bool) (func(string) bool, error) {
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

// searchFzfArgs returns the fzf argument list for interactive search. A
// highlight preview is injected ahead of the user's own [fzf].args unless those
// already carry a --preview, which stays authoritative.
func searchFzfArgs(cfg *config.Config, pattern string, opts searchOptions) []string {
	args := cfg.Fzf.Args
	for _, a := range args {
		if a == "--preview" || strings.HasPrefix(a, "--preview=") {
			return args
		}
	}
	preview := buildLiveQueryHighlightPreview()
	if pattern != "" {
		preview = buildHighlightPreview(pattern, opts.regex, opts.caseSensitive)
	}
	return append([]string{"--preview", preview}, args...)
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
//
// Where the body starts is frontmatter.Parse's decision, not a second guess at
// the delimiters: the parser strips a leading BOM, ignores an indented "---",
// and treats an unterminated block as body, so a line-level heuristic would
// show or hide lines the note's own body does not.
func readBodyLines(path string) (lines []string, bodyStart int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	doc, err := frontmatter.Parse(raw)
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
	// The body is always a suffix of the file, so what precedes it is the
	// frontmatter block. A closing delimiter at end of file has no newline after
	// it, and the body then starts one line past the last counted one.
	prefix := string(raw[:len(raw)-len(doc.Body)])
	bodyStart = strings.Count(prefix, "\n")
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		bodyStart++
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
	matchIdxs := matchingBodyLines(fileLines, bodyStartIdx, match)
	if len(matchIdxs) == 0 {
		return nil
	}
	wins := mergeWindows(contextWindows(matchIdxs, bodyStartIdx, len(fileLines)-1, contextN))
	writeContextWindows(out, wins, fileLines, relOrAbsPath(vaultRoot, n.Path), match)
	return nil
}

// matchingBodyLines returns the 0-based indices of the body lines that match.
func matchingBodyLines(fileLines []string, bodyStartIdx int, match func(string) bool) []int {
	var matchIdxs []int
	for i := bodyStartIdx; i < len(fileLines); i++ {
		if match(fileLines[i]) {
			matchIdxs = append(matchIdxs, i)
		}
	}
	return matchIdxs
}

// contextWindows builds one window per match, each clamped to the body range so
// context never reaches back into the frontmatter or past the last line.
func contextWindows(matchIdxs []int, bodyStartIdx, lastIdx, contextN int) []window {
	wins := make([]window, len(matchIdxs))
	for k, idx := range matchIdxs {
		lo := idx - contextN
		if lo < bodyStartIdx {
			lo = bodyStartIdx
		}
		hi := idx + contextN
		if hi > lastIdx {
			hi = lastIdx
		}
		wins[k] = window{lo, hi}
	}
	return wins
}

// writeContextWindows emits the merged windows in grep's own shape: "--"
// between non-adjacent blocks, ":" before a matching line's number and "-"
// before a context line's. Write errors are dropped here exactly as they were
// when this was inline; `--context` is a display mode with no consumer that
// reads its exit status differently.
func writeContextWindows(out io.Writer, wins []window, fileLines []string, relPath string, match func(string) bool) {
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
		relPath := relOrAbsPath(vaultRoot, n.Path)
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

// relOrAbsPath renders path relative to vaultRoot with forward slashes, falling
// back to path itself when no relative form exists.
func relOrAbsPath(vaultRoot, path string) string {
	rel, err := filepath.Rel(vaultRoot, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// bodyHasMatch reports whether any body line matches. An empty body is one
// empty line here, so a regex that matches nothing still gets asked — which is
// deliberately not what collectSearchCounts does.
func bodyHasMatch(body string, matcher func(string) bool) bool {
	for _, line := range strings.Split(body, "\n") {
		if matcher(line) {
			return true
		}
	}
	return false
}

func countMatchingLines(body string, matcher func(string) bool) int {
	c := 0
	for _, line := range strings.Split(body, "\n") {
		if matcher(line) {
			c++
		}
	}
	return c
}

// searchCount is one row of `search --count` output.
type searchCount struct {
	count   int
	relpath string
}

// collectSearchCounts counts matching body lines per note and orders the rows
// by count descending, then relative path. Notes with an empty body are skipped
// before matching, so an empty note is never a zero-length match.
func collectSearchCounts(notes []*vault.Note, vaultRoot string, matcher func(string) bool) ([]searchCount, error) {
	var results []searchCount
	for _, n := range notes {
		body, err := n.Body()
		if err != nil {
			return nil, err
		}
		if body == "" {
			continue
		}
		c := countMatchingLines(body, matcher)
		if c == 0 {
			continue
		}
		results = append(results, searchCount{count: c, relpath: relOrAbsPath(vaultRoot, n.Path)})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].count != results[j].count {
			return results[i].count > results[j].count
		}
		return results[i].relpath < results[j].relpath
	})
	return results, nil
}

// writeSearchCounts writes the tab-separated "<count>\t<relpath>" rows.
func writeSearchCounts(out io.Writer, results []searchCount) error {
	for _, r := range results {
		if _, err := fmt.Fprintf(out, "%d\t%s\n", r.count, r.relpath); err != nil {
			return err
		}
	}
	return nil
}

// writePathList writes one path per record, NUL-separated when nullSep is set
// and newline-separated otherwise.
//
// It is the CLI's single definition of `-0`. `bn find -0 | bn set --null -` is a
// documented pipeline, and `bn search -0` feeds the same consumers, so both
// commands' path output — including an interactive selection — is written here
// rather than each deciding a separator for itself.
func writePathList(out io.Writer, paths []string, nullSep bool) error {
	sep := byte('\n')
	if nullSep {
		sep = 0
	}
	for _, p := range paths {
		if _, err := fmt.Fprintf(out, "%s%c", p, sep); err != nil {
			return err
		}
	}
	return nil
}
