package cockpit

import (
	"fmt"
	"path/filepath"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// captureBacklogTasks builds the single advisory capture-backlog task when the
// configured capture inbox holds more captures than [cockpit] max_captures
// allows. It is gated on max_captures > 0, so the rule is off unless the vault
// opts in, and it returns nil when the inbox note does not exist — an inbox that
// was never created cannot have a backlog. The count comes from
// noteops.CountSectionBullets, the same scanner `bn capture` writes through, so
// the rule can never count a section the writer would not have written to. The
// task is triage-only: processing captures is human judgement, not a mechanical
// fix.
func captureBacklogTasks(notes []loadedNote, cfg *config.Config) []Task {
	maxCaptures := cfg.Cockpit.MaxCaptures
	inbox := cfg.Capture.InboxNote
	heading := cfg.Capture.Heading
	if maxCaptures <= 0 || inbox == "" || heading == "" {
		return nil
	}

	item, ok := findCaptureInbox(notes, inbox)
	if !ok {
		return nil
	}
	body, err := item.note.Body()
	if err != nil {
		return nil
	}
	count := noteops.CountSectionBullets(body, heading)
	if count <= maxCaptures {
		return nil
	}

	affected := []AffectedNote{affectedForNote(item, "heading: "+heading)}
	title := affectedTitle(affected[0])
	return []Task{newTask(
		RuleCaptureBacklog,
		"Capture backlog: "+title,
		fmt.Sprintf(
			"%s holds %d captures under %s, over the limit of %d; the task re-signs on every capture, so a snooze lasts only until the next one",
			title, count, heading, maxCaptures,
		),
		"capture backlog: "+affected[0].RelPath,
		affected,
	)}
}

// findCaptureInbox locates the loaded note at the configured vault-relative
// inbox path. The configured path is already normalized to a clean,
// slash-separated, in-vault path by config, so a plain string comparison against
// each note's slash relative path is enough.
func findCaptureInbox(notes []loadedNote, inbox string) (loadedNote, bool) {
	for _, item := range notes {
		if filepath.ToSlash(item.rel) == inbox {
			return item, true
		}
	}
	return loadedNote{}, false
}
