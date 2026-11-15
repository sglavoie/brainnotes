package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// todayFixture is a vault with a daily template routed to Daily/. runNew pins
// the clock to 2026-05-18, which is therefore "today"; the second value is
// where that day's note belongs.
func todayFixture(t *testing.T) (root, want string) {
	t.Helper()
	root = buildNewFixtureWithRoutes(t, "daily = \"Daily\"\n")
	template := "---\ncreated: {{today}}\n---\n# {{ .Title }}\n"
	if err := os.WriteFile(filepath.Join(root, "Templates", "daily.md"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "Daily", "2026-05-18.md")
}

// printToday runs `bn today --print` and requires it to report want.
func printToday(t *testing.T, want string) {
	t.Helper()
	out, err := runNew(t, "today", "--print")
	if err != nil || strings.TrimSpace(out) != want {
		t.Fatalf("today printed %q (%v), want %q", out, err, want)
	}
}

func TestTodayCreatesTheDailyNoteFromItsTemplate(t *testing.T) {
	_, want := todayFixture(t)
	printToday(t, want)
	created, err := os.ReadFile(want)
	if err != nil || !strings.Contains(string(created), "# 2026-05-18") {
		t.Fatalf("daily note = %q, %v", created, err)
	}
}

// The second run of the day is the ordinary case: the note is found, left
// exactly as it is, and reported again. Creation is one journalled operation,
// as `bn new` is, and reopening adds none.
func TestTodayReopensAnExistingDailyNoteUntouched(t *testing.T) {
	_, want := todayFixture(t)
	printToday(t, want)
	appendToFile(t, want, "\nwritten since\n")
	printToday(t, want)

	if after, _ := os.ReadFile(want); !strings.Contains(string(after), "written since") {
		t.Fatalf("today overwrote an existing daily note:\n%s", after)
	}
	history, err := runCLI(t, "", "history")
	if err != nil {
		t.Fatal(err)
	}
	if got := nonEmptyLines(history); len(got) != 1 || !strings.Contains(got[0], "bn today") {
		t.Fatalf("history after two runs:\n%s", history)
	}
}

func appendToFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestTodayWithoutATemplateSaysWhatToCreate(t *testing.T) {
	buildNewFixtureWithRoutes(t, "")
	out, err := runNew(t, "today", "--print")
	if err == nil {
		t.Fatalf("today without a daily template succeeded:\n%s", out)
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "today: ") || !strings.Contains(msg, "--template") {
		t.Fatalf("error = %q", msg)
	}
}
