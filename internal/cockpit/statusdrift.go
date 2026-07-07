package cockpit

import (
	"fmt"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// reviewDriftTasks builds the missing-review-date and stale-status advisory
// tasks for notes whose status expects a review date. It is gated on
// [cockpit] review_statuses: an empty list disables both rules (the "off"
// switch flagged in session 1). For each note in a review status it reads the
// review date only from [cockpit] review_date_property — never guessing from an
// imported created date — so:
//
//   - a note with no parseable review-date property yields a missing_review_date
//     task, regardless of the staleness window; and
//   - a note whose review date is older than [cockpit] stale_status_days yields a
//     stale_status task (skipped when stale_status_days is non-positive).
//
// Each task carries the single implicated note as its affected set, mirroring
// the per-note issue tasks (orphan/dead-end). Tasks are triage-only: they never
// mutate the note.
func reviewDriftTasks(notes []loadedNote, cfg *config.Config, now time.Time) []Task {
	reviewStatuses := cfg.Cockpit.ReviewStatuses
	if len(reviewStatuses) == 0 {
		return nil
	}
	reviewProp := cfg.Cockpit.ReviewDateProperty
	staleDays := cfg.Cockpit.StaleStatusDays

	var tasks []Task
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		status := statusValue(front)
		if !statusInSet(status, reviewStatuses) {
			continue
		}

		reviewed, ok := noteTimestamp(front, reviewProp)
		if !ok {
			affected := []AffectedNote{affectedForNote(item, "frontmatter."+reviewProp)}
			title := affectedTitle(affected[0])
			tasks = append(tasks, newTask(
				RuleMissingReviewDate,
				"Missing review date: "+title,
				fmt.Sprintf("%s has status %q but no %s date", title, status, reviewProp),
				affected[0].RelPath,
				affected,
			))
			continue
		}

		if staleDays > 0 && daysSince(now, reviewed) > staleDays {
			affected := []AffectedNote{affectedForNote(item, "frontmatter."+reviewProp)}
			title := affectedTitle(affected[0])
			tasks = append(tasks, newTask(
				RuleStaleStatus,
				"Stale status: "+title,
				fmt.Sprintf("%s was last reviewed %s, over %d days ago", title, frontmatter.FormatTimestamp(reviewed), staleDays),
				affected[0].RelPath,
				affected,
			))
		}
	}
	return tasks
}

// wipLimitTasks builds the single work-in-progress overflow task when more notes
// are in a work-in-progress status than [cockpit] max_wip allows. It is gated on
// [cockpit] wip_statuses: an empty list disables the rule. The affected set lists
// every work-in-progress note (sorted by newTask) so a human can see the full
// backlog; the task is triage-only and never mutates a note. Returns nil when the
// rule is disabled, the limit is non-positive, or the count is within the limit.
func wipLimitTasks(notes []loadedNote, cfg *config.Config) []Task {
	wipStatuses := cfg.Cockpit.WIPStatuses
	if len(wipStatuses) == 0 {
		return nil
	}
	maxWIP := cfg.Cockpit.MaxWIP

	var affected []AffectedNote
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		if statusInSet(statusValue(front), wipStatuses) {
			affected = append(affected, affectedForNote(item, "frontmatter."+statusProperty))
		}
	}
	if maxWIP <= 0 || len(affected) <= maxWIP {
		return nil
	}

	task := newTask(
		RuleWIPLimit,
		"WIP limit exceeded",
		fmt.Sprintf("%d notes are work-in-progress, over the limit of %d", len(affected), maxWIP),
		"work-in-progress limit exceeded",
		affected,
	)
	return []Task{task}
}

// affectedTitle returns a note's display title for a task summary, falling back
// to its relative path when the note has no title.
func affectedTitle(note AffectedNote) string {
	if note.Title != "" {
		return note.Title
	}
	return note.RelPath
}
