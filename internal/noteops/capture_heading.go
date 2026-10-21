package noteops

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// captureATXHeading matches an ATX markdown heading line per CommonMark and
// captures the hash run and the title with any closing hash sequence trimmed, so
// `## Captures ##` still anchors `## Captures`. A closing sequence counts only
// when a space or tab precedes it, so `## Notes on C#` keeps its `#`. Indentation
// is up to three spaces and never a tab: four spaces or a tab make indented
// code, which needs no separate handling because such a line can never match. A
// bare `##` or `## ##` is an empty heading: it ends a section, but has no title
// to anchor on. The lazy `??` tries the empty title first, which is what makes
// `### ###` empty rather than titled `###`. The shape mirrors
// internal/cockpit/review.go's atxHeading and the web synthesis draft check;
// cockpit imports noteops and not the reverse, so the matcher is duplicated
// rather than exported from there.
var captureATXHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))??(?:[ \t]+#+)?[ \t]*\r?$`)

// captureTopLevelBullet matches a top-level list item. Up to three leading
// spaces still count as top level, matching the heading rule, so a two-space
// nested item is counted; capture itself only ever writes flat `- ` items and
// four-space continuations are excluded.
var captureTopLevelBullet = regexp.MustCompile(`^\s{0,3}[-*+]\s`)

// CountSectionBullets returns the number of top-level list items inside the
// section that heading anchors in body, ignoring fenced code. An unparseable or
// absent heading counts zero. The cockpit capture-backlog rule shares this
// counter with the capture writer so the rule can never count a section the
// writer would not have written to.
func CountSectionBullets(body, heading string) int {
	level, titleKey, err := parseCaptureHeading(heading)
	if err != nil {
		return 0
	}
	b := splitCaptureBody(body)
	anchor := findCaptureAnchor(b.lines, level, titleKey)
	if anchor < 0 {
		return 0
	}

	count := 0
	var fence wikilink.FenceScanner
	end := captureSectionEnd(b.lines, anchor, level)
	for _, line := range b.lines[anchor+1 : end] {
		if fence.Line(line) {
			continue
		}
		if captureTopLevelBullet.MatchString(line) {
			count++
		}
	}
	return count
}

// planCaptureInsert returns body with line added to the section anchored by
// heading, the 1-based body line number the new line occupies, and whether the
// heading had to be created. Existing lines are never rewritten: the insert goes
// before the section's trailing blank lines, so the blank separator ahead of the
// next heading survives and an empty section receives the line directly under
// its heading. The body's line terminator is preserved, as is its
// trailing-newline state on the anchored path; the heading-absent path appends a
// terminated section, so its result always ends with a newline.
func planCaptureInsert(body, heading, line string) (string, int, bool, error) {
	level, titleKey, err := parseCaptureHeading(heading)
	if err != nil {
		return "", 0, false, err
	}
	if strings.ContainsAny(line, "\n\r") {
		return "", 0, false, Errorf(CodeInvalidRequest, "capture line must be a single line")
	}
	if strings.TrimSpace(line) == "" {
		return "", 0, false, Errorf(CodeInvalidRequest, "capture line is empty")
	}

	b := splitCaptureBody(body)
	anchor := findCaptureAnchor(b.lines, level, titleKey)
	if anchor < 0 {
		return appendCaptureSection(b, strings.TrimSpace(heading), line)
	}

	insert := captureSectionEnd(b.lines, anchor, level)
	for insert > anchor+1 && captureLineIsBlank(b.lines[insert-1]) {
		insert--
	}
	next := make([]string, 0, len(b.lines)+1)
	next = append(next, b.lines[:insert]...)
	next = append(next, b.terminate(line))
	next = append(next, b.lines[insert:]...)
	b.lines = next
	return b.render(), insert + 1, false, nil
}

// appendCaptureSection adds heading and line after the existing body, which is
// left byte-identical: the result is the original bytes plus a terminator for an
// unterminated last line, a blank separator only when the body does not already
// end in one, the heading, the bullet, and a final terminator. Collapsing
// existing trailing blank lines here would delete bytes, which the strictly
// additive capture contract forbids and the dry-run preview would not disclose.
func appendCaptureSection(b captureBody, heading, line string) (string, int, bool, error) {
	next := make([]string, 0, len(b.lines)+3)
	next = append(next, b.lines...)
	if len(next) > 0 && !captureLineIsBlank(next[len(next)-1]) {
		next = append(next, b.terminate(""))
	}
	next = append(next, b.terminate(heading), b.terminate(line))
	b.lines = next
	b.trailingNewline = true
	return b.render(), len(next), true, nil
}

// captureBody is a body split for line scanning. Lines are kept verbatim,
// carriage return included, so rejoining them reproduces the original bytes even
// when terminators are mixed.
type captureBody struct {
	lines           []string
	carriageReturn  string
	trailingNewline bool
}

func splitCaptureBody(body string) captureBody {
	b := captureBody{}
	if body == "" {
		return b
	}
	b.trailingNewline = strings.HasSuffix(body, "\n")
	b.lines = strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if strings.HasSuffix(b.lines[0], "\r") {
		b.carriageReturn = "\r"
	}
	return b
}

// terminate renders text as a line for this body, matching its terminator.
func (b captureBody) terminate(text string) string { return text + b.carriageReturn }

func (b captureBody) render() string {
	out := strings.Join(b.lines, "\n")
	if b.trailingNewline {
		out += "\n"
	}
	return out
}

func captureLineIsBlank(line string) bool { return strings.TrimSpace(line) == "" }

// findCaptureAnchor returns the index of the first non-fenced ATX heading whose
// level and title match, or -1. First match wins, so duplicate headings resolve
// deterministically.
func findCaptureAnchor(lines []string, level int, titleKey string) int {
	var fence wikilink.FenceScanner
	for i, line := range lines {
		if fence.Line(line) {
			continue
		}
		match := captureATXHeading.FindStringSubmatch(line)
		if match == nil || len(match[1]) != level {
			continue
		}
		if captureHeadingKey(match[2]) == titleKey {
			return i
		}
	}
	return -1
}

// containsATXHeading reports whether body holds an ATX heading of the given
// level and title outside fenced code. It shares the capture scanner so a
// heading a writer would recognize and a heading a validator rejects can never
// diverge.
func containsATXHeading(body string, level int, title string) bool {
	key := captureHeadingKey(title)
	if key == "" {
		return false
	}
	return findCaptureAnchor(splitCaptureBody(body).lines, level, key) >= 0
}

// captureSectionEnd returns the index one past the last line of the section
// anchored at index anchor: the first later non-fenced heading of the same or a
// lower level, else len(lines). Deeper subheadings stay inside the section.
func captureSectionEnd(lines []string, anchor, level int) int {
	var fence wikilink.FenceScanner
	for i := anchor + 1; i < len(lines); i++ {
		line := lines[i]
		if fence.Line(line) {
			continue
		}
		if match := captureATXHeading.FindStringSubmatch(line); match != nil && len(match[1]) <= level {
			return i
		}
	}
	return len(lines)
}

// parseCaptureHeading splits a configured heading into its level and title key.
func parseCaptureHeading(heading string) (int, string, error) {
	match := captureATXHeading.FindStringSubmatch(strings.TrimSpace(heading))
	if match == nil {
		return 0, "", Errorf(CodeInvalidRequest, "capture heading %q is not an ATX heading", heading)
	}
	key := captureHeadingKey(match[2])
	if key == "" {
		return 0, "", Errorf(CodeInvalidRequest, "capture heading %q has no title", heading)
	}
	return len(match[1]), key, nil
}

// captureHeadingKey is the comparison key for a heading title: NFC-normalized,
// trimmed, and lowercased. Level is compared separately, so `### Captures` never
// anchors a configured `## Captures` while `## captures` does.
func captureHeadingKey(title string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(title)))
}

// normalizeCaptureText prepares user text for a single capture bullet: NFC
// normalized, trimmed, with every interior whitespace run — including the
// newlines of pasted multi-line text — folded to one space. Empty text and any
// surviving control character are rejected so a capture can never break the
// one-line-per-capture format.
func normalizeCaptureText(raw string) (string, error) {
	text := strings.Join(strings.Fields(textnorm.NFC(raw)), " ")
	if text == "" {
		return "", Errorf(CodeInvalidRequest, "capture text is empty")
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return "", Errorf(CodeInvalidRequest, "capture text contains a control character")
		}
	}
	return text, nil
}
