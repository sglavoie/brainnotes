package cockpit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// statusProperty is the frontmatter key the review-metadata rules read a note's
// workflow status from. It mirrors query.Status so the cockpit and the find
// command agree on where a note's status lives.
const statusProperty = "status"

// scalarString renders a single frontmatter scalar as its comparable string
// form. It mirrors the scalar branches of longTailValueStrings so status/type
// comparisons stringify the same way the long-tail counter does; lists and maps
// are not scalars and yield "". Timestamps use FormatTimestamp for stable,
// date-aware output.
func scalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case time.Time:
		return frontmatter.FormatTimestamp(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// statusValue returns a note's status string from the "status" frontmatter
// property, whitespace-trimmed. Non-string scalars are stringified so a status
// stored as an int/bool/date still compares. Returns "" when the property is
// absent or its value is empty. Callers comparing against configured status
// lists should use statusInSet, which folds case on both sides.
func statusValue(front *frontmatter.Map) string {
	if front == nil {
		return ""
	}
	raw, ok := front.Get(statusProperty)
	if !ok {
		return ""
	}
	return strings.TrimSpace(scalarString(raw))
}

// statusInSet reports whether status matches any entry in allowed, trimming
// whitespace and folding case on both sides. An empty status never matches, and
// an empty allowed list matches nothing — the natural "rule disabled" switch for
// the status-gated review rules (missing_review_date, wip_limit).
func statusInSet(status string, allowed []string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.ToLower(strings.TrimSpace(candidate)) == status {
			return true
		}
	}
	return false
}

// noteTimestamp returns the first frontmatter property in properties that parses
// as a YAML timestamp, along with true. Properties are tried in order, so
// callers pass their preferred date first (for example "last", then "created").
// Blank property names are skipped. Returns the zero time and false when none of
// the named properties hold a time value.
func noteTimestamp(front *frontmatter.Map, properties ...string) (time.Time, bool) {
	if front == nil {
		return time.Time{}, false
	}
	for _, prop := range properties {
		prop = strings.TrimSpace(prop)
		if prop == "" {
			continue
		}
		if t, ok := front.GetTime(prop); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// daysSince returns the whole number of days elapsed from then until now,
// floored at zero: a zero, equal, or future timestamp reports 0. It underlies
// the stale-status age check, which compares the result against
// [cockpit] stale_status_days.
func daysSince(now, then time.Time) int {
	if then.IsZero() || !then.Before(now) {
		return 0
	}
	return int(now.Sub(then).Hours() / 24)
}

// isHubNote reports whether a note's type/kind frontmatter names it a hub/MOC
// note, per [cockpit] hub_type_values. Comparison trims whitespace and folds
// case. An empty hubTypes list matches nothing (the rule's "disabled" switch).
// Both "type" and "kind" are consulted so project-style notes (which use "kind")
// are covered, mirroring query.Type.
func isHubNote(front *frontmatter.Map, hubTypes []string) bool {
	if front == nil || len(hubTypes) == 0 {
		return false
	}
	for _, prop := range []string{"type", "kind"} {
		raw, ok := front.Get(prop)
		if !ok {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(scalarString(raw)))
		if value == "" {
			continue
		}
		for _, want := range hubTypes {
			if strings.ToLower(strings.TrimSpace(want)) == value {
				return true
			}
		}
	}
	return false
}

// atxHeading matches an ATX markdown heading line (one to six leading hashes
// followed by whitespace) and captures the heading text. Trailing hashes and
// surrounding whitespace are trimmed by the caller.
var atxHeading = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)

// codeFence matches an opening or closing fenced-code delimiter (``` or ~~~).
var codeFence = regexp.MustCompile("^\\s{0,3}(```+|~~~+)")

// emptyHubHeadings returns the subset of wanted heading titles that appear in
// body as ATX headings but have no non-blank content between the heading and the
// next heading (or the end of the note). Titles are matched case-insensitively
// after trimming; a wanted title qualifies when at least one of its occurrences
// is empty. Headings inside fenced code blocks are ignored so code samples never
// count. The result preserves the order titles appear in wanted and never
// repeats a title. It is the body-side input to the hub-needs-synthesis rule.
func emptyHubHeadings(body string, wanted []string) []string {
	if strings.TrimSpace(body) == "" || len(wanted) == 0 {
		return nil
	}

	// emptyByTitle records, per lowercased heading title, whether any occurrence
	// of that heading had an empty section.
	emptyByTitle := map[string]bool{}
	lines := strings.Split(body, "\n")
	inFence := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if codeFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		match := atxHeading.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		title := strings.ToLower(strings.TrimSpace(match[2]))
		if title == "" {
			continue
		}
		empty := sectionIsEmpty(lines[i+1:])
		if empty {
			emptyByTitle[title] = true
		} else if _, seen := emptyByTitle[title]; !seen {
			emptyByTitle[title] = false
		}
	}

	var out []string
	seen := map[string]bool{}
	for _, title := range wanted {
		key := strings.ToLower(strings.TrimSpace(title))
		if key == "" || seen[key] {
			continue
		}
		if emptyByTitle[key] {
			out = append(out, strings.TrimSpace(title))
			seen[key] = true
		}
	}
	return out
}

// sectionIsEmpty reports whether the lines following a heading contain no
// non-blank content before the next heading. A fenced-code delimiter counts as
// content (an empty-looking fenced block is still a filled section), so the scan
// stops at the first fence, heading, or non-blank line.
func sectionIsEmpty(lines []string) bool {
	for _, line := range lines {
		if codeFence.MatchString(line) {
			return false
		}
		if atxHeading.MatchString(line) {
			return true
		}
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

// limitDaily returns the first limit tasks, leaving tasks unchanged when limit
// is non-positive (no cap) or the queue already fits. It never reorders: callers
// sort and workflow-state-filter first, then cap for the daily view so done and
// snoozed tasks do not consume daily slots. The returned slice shares backing
// storage with tasks.
func limitDaily(tasks []Task, limit int) []Task {
	if limit <= 0 || len(tasks) <= limit {
		return tasks
	}
	return tasks[:limit]
}

// LimitDaily caps a workflow-filtered queue to the daily cockpit view. It keeps
// only the first limit tasks — the highest-priority findings, since callers sort
// and workflow-state-filter before calling — and leaves Counts untouched so the
// full active/done/snoozed totals stay visible alongside the capped list. A
// non-positive limit means no cap. Apply this after StateStore.FilterQueue so
// done and snoozed tasks never consume a daily slot.
func LimitDaily(queue Queue, limit int) Queue {
	queue.Tasks = limitDaily(queue.Tasks, limit)
	return queue
}
