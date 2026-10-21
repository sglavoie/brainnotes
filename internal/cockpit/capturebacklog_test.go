package cockpit

import (
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// captureBacklogConfig returns a config that opts the capture-backlog rule in
// with a limit of two captures under the default inbox and heading.
func captureBacklogConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Capture.InboxNote = "Inbox.md"
	cfg.Capture.Heading = "## Captures"
	cfg.Cockpit.MaxCaptures = 2
	return cfg
}

func inboxWithCaptures(bullets ...string) map[string]string {
	body := "---\ntype: note\n---\n## Captures\n"
	for _, bullet := range bullets {
		body += "- " + bullet + "\n"
	}
	return map[string]string{"Inbox.md": body}
}

func TestCaptureBacklogRuleFiresAboveLimit(t *testing.T) {
	q := buildQueueAt(t, captureBacklogConfig(), fixedNow, inboxWithCaptures(
		"2026-08-17 09:30 one",
		"2026-08-17 09:31 two",
		"2026-08-17 09:32 three",
	))

	task := findTask(t, q, RuleCaptureBacklog, "capture backlog: Inbox.md")
	if task.Count != 1 {
		t.Fatalf("capture-backlog task = %#v, want one affected note", task)
	}
	if task.Priority != 42 {
		t.Fatalf("capture-backlog priority = %d, want 42", task.Priority)
	}
	if task.Title != "Capture backlog: Inbox" {
		t.Fatalf("capture-backlog title = %q", task.Title)
	}
	if task.Affected[0].Location != "heading: ## Captures" {
		t.Fatalf("capture-backlog affected = %#v", task.Affected)
	}
	if !strings.Contains(task.Summary, "3 captures") {
		t.Fatalf("summary should report the count: %q", task.Summary)
	}
	// The signature folds the inbox's file hash, so a snooze cannot outlive the
	// next capture. The summary must say so, since it is surprising.
	if !strings.Contains(task.Summary, "snooze lasts only until the next") {
		t.Fatalf("summary should explain the snooze lifetime: %q", task.Summary)
	}
}

func TestCaptureBacklogRuleQuietAtOrBelowLimit(t *testing.T) {
	cfg := captureBacklogConfig()

	for name, files := range map[string]map[string]string{
		"at the limit":    inboxWithCaptures("one", "two"),
		"below the limit": inboxWithCaptures("one"),
		"empty section":   inboxWithCaptures(),
	} {
		t.Run(name, func(t *testing.T) {
			q := buildQueueAt(t, cfg, fixedNow, files)
			if _, ok := taskByKind(q, RuleCaptureBacklog); ok {
				t.Fatalf("capture-backlog fired %s", name)
			}
		})
	}
}

// TestCaptureBacklogRuleOffByDefault confirms the opt-in gate: a zero
// max_captures leaves the rule disabled no matter how many captures pile up.
func TestCaptureBacklogRuleOffByDefault(t *testing.T) {
	cfg := &config.Config{}
	cfg.Capture.InboxNote = "Inbox.md"
	cfg.Capture.Heading = "## Captures"
	// MaxCaptures left at zero on purpose.

	q := buildQueueAt(t, cfg, fixedNow, inboxWithCaptures("one", "two", "three"))
	if _, ok := taskByKind(q, RuleCaptureBacklog); ok {
		t.Fatalf("capture-backlog fired with max_captures unset")
	}
}

// TestCaptureBacklogRuleNoInboxNote confirms that a vault whose inbox note has
// never been created produces no task, rather than a task against a phantom path.
func TestCaptureBacklogRuleNoInboxNote(t *testing.T) {
	q := buildQueueAt(t, captureBacklogConfig(), fixedNow, map[string]string{
		"Other.md": "---\ntype: note\n---\n## Captures\n- one\n- two\n- three\n",
	})
	if _, ok := taskByKind(q, RuleCaptureBacklog); ok {
		t.Fatalf("capture-backlog fired without the configured inbox note")
	}
}

// TestCaptureBacklogSignatureChangesWithInbox pins the intentional signature
// churn: each new capture rewrites the inbox, which changes the affected note's
// file hash and therefore the task signature, so stored snooze state expires.
func TestCaptureBacklogSignatureChangesWithInbox(t *testing.T) {
	cfg := captureBacklogConfig()

	three := buildQueueAt(t, cfg, fixedNow, inboxWithCaptures("one", "two", "three"))
	four := buildQueueAt(t, cfg, fixedNow, inboxWithCaptures("one", "two", "three", "four"))

	first := findTask(t, three, RuleCaptureBacklog, "capture backlog: Inbox.md")
	second := findTask(t, four, RuleCaptureBacklog, "capture backlog: Inbox.md")
	if first.Signature == second.Signature {
		t.Fatalf("signature %q must change when the inbox changes", first.Signature)
	}
}

// TestCaptureBacklogTaskIsTriageOnly confirms the rule offers no mechanical fix:
// clearing a backlog is human work, so only the triage affordances appear.
func TestCaptureBacklogTaskIsTriageOnly(t *testing.T) {
	q := buildQueueAt(t, captureBacklogConfig(), fixedNow, inboxWithCaptures("one", "two", "three"))
	task := findTask(t, q, RuleCaptureBacklog, "capture backlog: Inbox.md")

	if !task.TriageOnly {
		t.Fatalf("capture-backlog task must be triage-only: %#v", task)
	}
	for _, action := range task.Actions {
		switch action.Kind {
		case ActionOpen, ActionSnooze, ActionMarkDone:
		default:
			t.Fatalf("capture-backlog task offers a fix action %q", action.Kind)
		}
	}
}
