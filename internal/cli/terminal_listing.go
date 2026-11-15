package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// Output for a person and output for a program are two different things, and
// this file is the one place the first is drawn. Every function here is
// reached only when stdout is a terminal and no machine form (--json, -0) was
// asked for; a pipeline keeps reading exactly the records it always has.

// maxListedMatches is how many matching lines of one note a terminal search
// shows before summarizing the rest.
const maxListedMatches = 3

// termStyle decorates text for a terminal. Colour is off under NO_COLOR and
// for TERM=dumb, where an escape sequence is noise rather than emphasis.
type termStyle struct{ color bool }

func newTermStyle() termStyle {
	_, noColor := os.LookupEnv("NO_COLOR")
	return termStyle{color: !noColor && os.Getenv("TERM") != "dumb"}
}

func (s termStyle) wrap(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s termStyle) dim(text string) string    { return s.wrap("2", text) }
func (s termStyle) bold(text string) string   { return s.wrap("1", text) }
func (s termStyle) accent(text string) string { return s.wrap("1;36", text) }

// notePath renders a vault-relative path with its folder quiet and its file
// name plain, so a long listing reads by title.
func (s termStyle) notePath(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return s.dim(rel[:i+1]) + rel[i+1:]
	}
	return rel
}

// listsForPerson reports whether a path listing should be drawn for a person:
// stdout is a terminal and the records are not NUL-separated.
func listsForPerson(out io.Writer, nullSep bool) bool {
	return !nullSep && stdoutIsTerminal(out)
}

// writeNoteList is the default listing of find, backlinks and links. A
// pipeline gets writePathList's absolute paths; a terminal gets the same notes
// vault-relative, which every bn command accepts back as an argument, followed
// by how many there were.
func writeNoteList(out io.Writer, root string, paths []string, nullSep bool) error {
	if !listsForPerson(out, nullSep) {
		return writePathList(out, paths, nullSep)
	}
	style := newTermStyle()
	for _, path := range paths {
		if _, err := fmt.Fprintln(out, style.notePath(relOrAbsPath(root, path))); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, style.dim(countNoun(len(paths), "note")))
	return err
}

// countNoun renders "1 note" / "3 notes" / "no notes".
func countNoun(n int, noun string) string {
	switch n {
	case 0:
		return "no " + noun + "s"
	case 1:
		return "1 " + noun
	default:
		return fmt.Sprintf("%d %ss", n, noun)
	}
}

// writeSearchListing is search's default mode on a terminal: each matching
// note, then the first few lines that matched, because the line is what says
// whether this is the note being looked for.
func writeSearchListing(out io.Writer, r *searchRun, notes []*vault.Note) error {
	style := newTermStyle()
	for _, n := range notes {
		lines, bodyStart, err := readBodyLines(n.Path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, style.notePath(relOrAbsPath(r.root, n.Path))); err != nil {
			return err
		}
		if bodyStart >= len(lines) {
			continue
		}
		if err := writeListedMatches(out, style, r, lines, matchingBodyLines(lines, bodyStart, r.matcher)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, style.dim(countNoun(len(notes), "note")))
	return err
}

func writeListedMatches(out io.Writer, style termStyle, r *searchRun, lines []string, matched []int) error {
	for shown, index := range matched {
		if shown == maxListedMatches {
			_, err := fmt.Fprintln(out, style.dim(fmt.Sprintf("  … %d more", len(matched)-shown)))
			return err
		}
		text := truncateSnippet(strings.TrimSpace(lines[index]))
		if !r.opts.regex {
			text = highlightLiteral(style, text, r.pattern, r.opts.caseSensitive)
		}
		if _, err := fmt.Fprintf(out, "  %s %s\n", style.dim(fmt.Sprintf("%d:", index+1)), text); err != nil {
			return err
		}
	}
	return nil
}

// highlightLiteral emphasizes each occurrence of a literal pattern. It gives
// up, leaving the line plain, when case folding changes the line's byte
// length, since the offsets found in the folded copy would then be wrong.
func highlightLiteral(style termStyle, line, pattern string, caseSensitive bool) string {
	haystack, needle := line, pattern
	if !caseSensitive {
		haystack, needle = strings.ToLower(line), strings.ToLower(pattern)
	}
	if !style.color || needle == "" || len(haystack) != len(line) {
		return line
	}
	var b strings.Builder
	for {
		i := strings.Index(haystack, needle)
		if i < 0 {
			break
		}
		b.WriteString(line[:i])
		b.WriteString(style.accent(line[i : i+len(needle)]))
		line, haystack = line[i+len(needle):], haystack[i+len(needle):]
	}
	b.WriteString(line)
	return b.String()
}
