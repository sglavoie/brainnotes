package cockpit

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
)

// fixedNow is the deterministic clock the status-drift tests measure staleness
// against so age computations never depend on the real wall clock.
var fixedNow = time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

// statusDriftConfig returns a config that enables all three status-drift rules.
func statusDriftConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Cockpit.ReviewStatuses = []string{"active", "review"}
	cfg.Cockpit.ReviewDateProperty = "reviewed"
	cfg.Cockpit.StaleStatusDays = 30
	cfg.Cockpit.WIPStatuses = []string{"wip", "doing"}
	cfg.Cockpit.MaxWIP = 2
	return cfg
}

// buildQueueAt loads the vault and assembles the queue against a fixed clock.
func buildQueueAt(t *testing.T, cfg *config.Config, now time.Time, files map[string]string) Queue {
	t.Helper()
	v := newTestVault(t, files)
	data, err := loadVaultData(v)
	if err != nil {
		t.Fatalf("loadVaultData: %v", err)
	}
	return buildQueueFromData(data, cfg, v.Root, now)
}

func TestReviewDriftRules(t *testing.T) {
	q := buildQueueAt(t, statusDriftConfig(), fixedNow, map[string]string{
		// Reviewed recently: has a date within the window, so no task.
		"Fresh.md": "---\nstatus: active\nreviewed: 2026-07-01\n---\nBody\n",
		// Reviewed long ago: past the 30-day window, so stale_status.
		"Stale.md": "---\nstatus: active\nreviewed: 2026-01-01\n---\nBody\n",
		// Review status but no reviewed property: missing_review_date.
		"NoDate.md": "---\nstatus: review\n---\nBody\n",
		// Not a review status: neither review rule applies.
		"Done.md": "---\nstatus: done\nreviewed: 2020-01-01\n---\nBody\n",
	})

	stale := findTask(t, q, RuleStaleStatus, "Stale.md")
	if stale.Count != 1 || stale.TriageOnly != true {
		t.Fatalf("stale task = %#v, want single triage-only affected", stale)
	}
	if stale.Title != "Stale status: Stale" {
		t.Fatalf("stale title = %q", stale.Title)
	}
	if stale.Affected[0].Location != "frontmatter.reviewed" {
		t.Fatalf("stale affected = %#v", stale.Affected)
	}

	missing := findTask(t, q, RuleMissingReviewDate, "NoDate.md")
	if missing.Count != 1 || missing.Title != "Missing review date: NoDate" {
		t.Fatalf("missing task = %#v", missing)
	}

	for _, task := range q.Tasks {
		if task.Kind == RuleStaleStatus && task.Detail != "Stale.md" {
			t.Fatalf("unexpected stale task for %q", task.Detail)
		}
		if task.Kind == RuleMissingReviewDate && task.Detail != "NoDate.md" {
			t.Fatalf("unexpected missing-review-date task for %q", task.Detail)
		}
	}
}

// TestMissingReviewDateNotGuessedFromCreated locks in the session requirement:
// a review-status note without the configured review-date property is reported
// as missing_review_date even when it carries an imported created date, rather
// than treating that created date as the review date and flagging it stale.
func TestMissingReviewDateNotGuessedFromCreated(t *testing.T) {
	q := buildQueueAt(t, statusDriftConfig(), fixedNow, map[string]string{
		"Imported.md": "---\nstatus: active\ncreated: 2000-01-01\n---\nBody\n",
	})

	if _, ok := taskByKind(q, RuleStaleStatus); ok {
		t.Fatalf("created date must not be treated as a review date for staleness")
	}
	missing := findTask(t, q, RuleMissingReviewDate, "Imported.md")
	if missing.Count != 1 {
		t.Fatalf("missing task = %#v", missing)
	}
}

func TestWIPLimitRule(t *testing.T) {
	cfg := statusDriftConfig()
	q := buildQueueAt(t, cfg, fixedNow, map[string]string{
		"W1.md": "---\nstatus: wip\n---\nBody\n",
		"W2.md": "---\nstatus: doing\n---\nBody\n",
		"W3.md": "---\nstatus: wip\n---\nBody\n",
		"Ok.md": "---\nstatus: done\n---\nBody\n",
	})

	wip := findTask(t, q, RuleWIPLimit, "work-in-progress limit exceeded")
	if wip.Count != 3 || wip.Title != "WIP limit exceeded" {
		t.Fatalf("wip task = %#v, want three affected", wip)
	}
	if !wip.TriageOnly {
		t.Fatalf("wip task should be triage-only: %#v", wip)
	}
	if rels := affectedRels(wip.Affected); !reflect.DeepEqual(rels, []string{"W1.md", "W2.md", "W3.md"}) {
		t.Fatalf("wip affected rels = %#v", rels)
	}

	// At or under the limit, no task fires.
	q = buildQueueAt(t, cfg, fixedNow, map[string]string{
		"W1.md": "---\nstatus: wip\n---\nBody\n",
		"W2.md": "---\nstatus: doing\n---\nBody\n",
	})
	if _, ok := taskByKind(q, RuleWIPLimit); ok {
		t.Fatalf("wip limit should not fire at or under the limit")
	}
}

// TestStatusDriftRulesDisabledByEmptyLists confirms the empty-list "off" switch:
// with no review or wip statuses configured, no status-drift task is generated
// even when notes carry those statuses and dates.
func TestStatusDriftRulesDisabledByEmptyLists(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cockpit.ReviewDateProperty = "reviewed"
	cfg.Cockpit.StaleStatusDays = 30
	cfg.Cockpit.MaxWIP = 1
	// ReviewStatuses and WIPStatuses left empty on purpose.

	q := buildQueueAt(t, cfg, fixedNow, map[string]string{
		"A.md": "---\nstatus: active\nreviewed: 2000-01-01\n---\nBody\n",
		"B.md": "---\nstatus: wip\n---\nBody\n",
		"C.md": "---\nstatus: wip\n---\nBody\n",
	})

	for _, task := range q.Tasks {
		switch task.Kind {
		case RuleStaleStatus, RuleMissingReviewDate, RuleWIPLimit:
			t.Fatalf("status-drift rule fired while disabled: %#v", task)
		}
	}
}

// taskByKind returns the first task of the given kind, if any.
func taskByKind(q Queue, kind RuleKind) (Task, bool) {
	for _, task := range q.Tasks {
		if task.Kind == kind {
			return task, true
		}
	}
	return Task{}, false
}
