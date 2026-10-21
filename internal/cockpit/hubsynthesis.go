package cockpit

import (
	"fmt"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
)

// hubSynthesisTasks builds the advisory hub-needs-synthesis tasks: one per hub
// note whose configured synthesis headings are present but empty. It is gated on
// [cockpit] hub_type_values (which notes count as hubs) and
// [cockpit] hub_empty_headings (which section titles must carry synthesis); an
// empty list for either disables the rule. Each task carries the single hub note
// as its affected set, mirroring the per-note review-drift tasks, and is
// triage-only — it never mutates a note. Tasks sort below safety and status
// findings via the priority-80 weight from session 1.
func hubSynthesisTasks(notes []loadedNote, cfg *config.Config) []Task {
	hubTypes := cfg.Cockpit.HubTypeValues
	wanted := hubWantedHeadings(cfg.Cockpit.HubEmptyHeadings)
	if len(hubTypes) == 0 || len(wanted) == 0 {
		return nil
	}

	var tasks []Task
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		if !isHubNote(front, hubTypes) {
			continue
		}
		body, err := item.note.Body()
		if err != nil {
			continue
		}
		empty := emptyHubHeadings(body, wanted)
		if len(empty) == 0 {
			continue
		}

		joined := strings.Join(empty, ", ")
		affected := []AffectedNote{affectedForNote(item, "heading: "+joined)}
		title := affectedTitle(affected[0])
		tasks = append(tasks, newTask(
			RuleHubNeedsSynthesis,
			"Hub needs synthesis: "+title,
			fmt.Sprintf("%s has empty %s: %s", title, pluralize(len(empty), "synthesis section", "synthesis sections"), joined),
			affected[0].RelPath,
			affected,
		))
	}
	return tasks
}

// hubWantedHeadings normalizes the configured hub headings for matching against
// note bodies: it strips any leading ATX hashes and surrounding whitespace so a
// config value written as "## Synthesis" matches the heading text emptyHubHeadings
// captures ("Synthesis"). Blank entries are dropped and order is preserved.
func hubWantedHeadings(headings []string) []string {
	out := make([]string, 0, len(headings))
	for _, heading := range headings {
		cleaned := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(heading), "#"))
		if cleaned == "" {
			continue
		}
		out = append(out, cleaned)
	}
	return out
}

// pluralize returns singular when count is 1 and plural otherwise. It keeps the
// hub-synthesis summary readable whether one or several headings are empty.
func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
