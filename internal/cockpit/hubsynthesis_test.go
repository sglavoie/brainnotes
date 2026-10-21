package cockpit

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// hubSynthesisConfig returns a config that enables the hub-needs-synthesis rule,
// treating "moc"/"hub" notes and requiring the Overview and Related sections to
// carry synthesis. The configured headings deliberately include an ATX-prefixed
// form to lock in the leading-hash normalization.
func hubSynthesisConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Cockpit.HubTypeValues = []string{"moc", "hub"}
	cfg.Cockpit.HubEmptyHeadings = []string{"## Overview", "Related"}
	return cfg
}

func TestHubSynthesisRule(t *testing.T) {
	q := buildQueueAt(t, hubSynthesisConfig(), fixedNow, map[string]string{
		// Hub note whose Overview heading is present but empty: flagged.
		"EmptyHub.md": "---\ntype: moc\n---\n## Overview\n\n## Related\n- [[Somewhere]]\n",
		// Hub note whose configured sections both carry content: not flagged.
		"FullHub.md": "---\ntype: hub\n---\n## Overview\nA real synthesis paragraph.\n\n## Related\n- [[Somewhere]]\n",
		// Non-hub note with the same empty heading: not a hub, so ignored.
		"Leaf.md": "---\ntype: note\n---\n## Overview\n\n",
	})

	task := findTask(t, q, RuleHubNeedsSynthesis, "EmptyHub.md")
	if task.Count != 1 || !task.TriageOnly {
		t.Fatalf("hub task = %#v, want single triage-only affected", task)
	}
	if task.Title != "Hub needs synthesis: EmptyHub" {
		t.Fatalf("hub title = %q", task.Title)
	}
	if task.Priority != 80 {
		t.Fatalf("hub priority = %d, want 80", task.Priority)
	}
	if task.Affected[0].Location != "heading: Overview" {
		t.Fatalf("hub affected = %#v", task.Affected)
	}

	// Only the empty hub should ever produce a hub-synthesis task.
	for _, other := range q.Tasks {
		if other.Kind == RuleHubNeedsSynthesis && other.Detail != "EmptyHub.md" {
			t.Fatalf("unexpected hub-synthesis task for %q", other.Detail)
		}
	}
}

// TestHubSynthesisLowerPriorityThanSafetyAndStatus locks in the session
// requirement: hub-synthesis findings sort after safety and status findings.
func TestHubSynthesisLowerPriorityThanSafetyAndStatus(t *testing.T) {
	hub := rulePriority(RuleHubNeedsSynthesis)
	for _, higher := range []RuleKind{
		RuleFrontmatterError,
		RuleSensitiveContent,
		RuleSchemaViolation,
		RuleMissingReviewDate,
		RuleStaleStatus,
		RuleWIPLimit,
	} {
		if rulePriority(higher) >= hub {
			t.Fatalf("hub-synthesis priority %d must sort after %q (%d)", hub, higher, rulePriority(higher))
		}
	}
}

// TestHubSynthesisDisabledByEmptyLists confirms the empty-list "off" switch: with
// no hub types or no target headings configured, no hub-synthesis task fires even
// when a note matches structurally.
func TestHubSynthesisDisabledByEmptyLists(t *testing.T) {
	files := map[string]string{
		"Hub.md": "---\ntype: moc\n---\n## Overview\n\n",
	}

	// No hub types: rule disabled.
	noTypes := &config.Config{}
	noTypes.Cockpit.HubEmptyHeadings = []string{"Overview"}
	if q := buildQueueAt(t, noTypes, fixedNow, files); func() bool {
		_, ok := taskByKind(q, RuleHubNeedsSynthesis)
		return ok
	}() {
		t.Fatalf("hub-synthesis fired with no hub types configured")
	}

	// No target headings: rule disabled.
	noHeadings := &config.Config{}
	noHeadings.Cockpit.HubTypeValues = []string{"moc"}
	if q := buildQueueAt(t, noHeadings, fixedNow, files); func() bool {
		_, ok := taskByKind(q, RuleHubNeedsSynthesis)
		return ok
	}() {
		t.Fatalf("hub-synthesis fired with no target headings configured")
	}
}
