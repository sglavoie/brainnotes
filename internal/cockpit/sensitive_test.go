package cockpit

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// secretFixture is a fake AWS access key ID used only in tests. It is
// secret-shaped so the scanner flags it, but it is not a real credential. Tests
// assert this literal never appears in any generated task field.
const secretFixture = "AKIAIOSFODNN7EXAMPLE"

// sensitiveConfig returns a config with the sensitive-content scanner enabled.
func sensitiveConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Cockpit.SensitiveScan = true
	return cfg
}

// assertNoRawSecret fails if any task field that reaches JSON, the signature, or
// rendered web output echoes the raw secret fixture. This is the load-bearing
// redaction guarantee: findings expose only line numbers and detector labels.
func assertNoRawSecret(t *testing.T, q Queue, secret string) {
	t.Helper()
	for _, task := range q.Tasks {
		fields := []string{task.ID, task.Signature, task.Title, task.Summary, task.Detail}
		for _, note := range task.Affected {
			fields = append(fields, note.Title, note.RelPath, note.Folder, note.Location)
		}
		for _, action := range task.Actions {
			fields = append(fields, string(action.Kind), action.Label)
		}
		for _, field := range fields {
			if strings.Contains(field, secret) {
				t.Fatalf("raw secret leaked into task field %q (kind=%s)", field, task.Kind)
			}
		}
	}
}

func TestSensitiveContentScanRedacts(t *testing.T) {
	q := buildQueueAt(t, sensitiveConfig(), fixedNow, map[string]string{
		"Notes.md": "---\ntitle: Notes\n---\nHere is a key " + secretFixture + " to worry about.\n",
	})

	task := findTask(t, q, RuleSensitiveContent, "Notes.md")
	if task.Count != 1 {
		t.Fatalf("sensitive task = %#v, want one finding", task)
	}
	if task.Title != "Possible sensitive content: Notes" {
		t.Fatalf("title = %q", task.Title)
	}
	if !task.TriageOnly {
		t.Fatalf("sensitive task must be triage-only: %#v", task)
	}
	// Only triage actions — never a mechanical fix affordance.
	for _, action := range task.Actions {
		switch action.Kind {
		case ActionOpen, ActionSnooze, ActionMarkDone:
		default:
			t.Fatalf("unexpected fix action on sensitive task: %#v", action)
		}
	}
	// The location categorizes the finding without echoing the value.
	if loc := task.Affected[0].Location; !strings.Contains(loc, "AWS access key ID") {
		t.Fatalf("location = %q, want the detector category", loc)
	}

	assertNoRawSecret(t, q, secretFixture)
}

func TestSensitiveContentScanDisabledByDefault(t *testing.T) {
	cfg := &config.Config{} // SensitiveScan defaults to false.
	q := buildQueueAt(t, cfg, fixedNow, map[string]string{
		"Notes.md": "---\ntitle: Notes\n---\nHere is a key " + secretFixture + " to worry about.\n",
	})
	if _, ok := taskByKind(q, RuleSensitiveContent); ok {
		t.Fatalf("scanner must not run unless sensitive_scan is enabled")
	}
}

func TestSensitiveContentScanCountsMultipleDetectors(t *testing.T) {
	q := buildQueueAt(t, sensitiveConfig(), fixedNow, map[string]string{
		"Many.md": "---\ntitle: Many\n---\n" +
			"aws " + secretFixture + "\n" +
			"password: hunter2secret\n" +
			"-----BEGIN RSA PRIVATE KEY-----\n",
	})

	task := findTask(t, q, RuleSensitiveContent, "Many.md")
	if task.Count != 3 {
		t.Fatalf("want three findings, got %#v", task)
	}
	assertNoRawSecret(t, q, secretFixture)
}

func TestSensitiveContentScanHonorsIgnorePaths(t *testing.T) {
	cfg := sensitiveConfig()
	cfg.Cockpit.SensitiveIgnorePaths = []string{"Private"}
	q := buildQueueAt(t, cfg, fixedNow, map[string]string{
		"Private/Vault.md": "---\ntitle: Vault\n---\nkey " + secretFixture + "\n",
		"Public/Note.md":   "---\ntitle: Note\n---\nkey " + secretFixture + "\n",
	})

	if _, ok := taskForRel(q, RuleSensitiveContent, "Private/Vault.md"); ok {
		t.Fatalf("note under an ignored path prefix must be skipped")
	}
	if _, ok := taskForRel(q, RuleSensitiveContent, "Public/Note.md"); !ok {
		t.Fatalf("note outside the ignored prefix must still be scanned")
	}
}

func TestSensitiveContentScanHonorsIgnorePatterns(t *testing.T) {
	cfg := sensitiveConfig()
	// Allow-list the example key and add a deliberately invalid pattern to prove
	// a bad regexp is skipped rather than aborting the scan.
	cfg.Cockpit.SensitiveIgnorePatterns = []string{secretFixture, "("}
	q := buildQueueAt(t, cfg, fixedNow, map[string]string{
		"Notes.md": "---\ntitle: Notes\n---\nkey " + secretFixture + "\n",
	})
	if _, ok := taskByKind(q, RuleSensitiveContent); ok {
		t.Fatalf("allow-listed value must not be flagged")
	}
}

// taskForRel returns the first task of the given kind whose detail is rel.
func taskForRel(q Queue, kind RuleKind, rel string) (Task, bool) {
	for _, task := range q.Tasks {
		if task.Kind == kind && task.Detail == rel {
			return task, true
		}
	}
	return Task{}, false
}
