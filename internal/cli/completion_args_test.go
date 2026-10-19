package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCompletionFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// completionValues strips cobra's "\t<description>" suffix from candidates.
func completionValues(candidates []string) []string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i], _, _ = strings.Cut(c, "\t")
	}
	return out
}

func TestCompletion_PositionalArgumentsFromVault(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "[[views]]\nname = \"reading\"\nquery = \"tag:alpha\"\n")
	writeCompletionFile(t, filepath.Join(root, "Projects", "Deep", "Idea.md"), "---\nstatus: open\n---\nx\n")
	writeCompletionFile(t, filepath.Join(root, "Inbox.md"), "---\ntags: [a]\n---\ny\n")
	writeCompletionFile(t, filepath.Join(root, "Templates", "Book Template.md"), "# {{.Title}}\n")
	writeCompletionFile(t, filepath.Join(root, "Templates", "daily.md"), "# day\n")
	writeCompletionFile(t, filepath.Join(root, "backups", "backup-20260101T000000.000000000Z.tar.gz"), "")
	writeCompletionFile(t, filepath.Join(root, "backups", "backup-20260201T000000.000000000Z.tar.gz"), "")
	writeCompletionFile(t, filepath.Join(root, "backups", ".backup-partial.tar.gz"), "")

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"views", "show", ""}, []string{"reading"}},
		{[]string{"views", "show", "reading", ""}, nil},
		{[]string{"new", ""}, []string{"Book", "daily"}},
		{[]string{"new", "Book", ""}, nil},
		{[]string{"restore", ""}, []string{
			"backups/backup-20260201T000000.000000000Z.tar.gz",
			"backups/backup-20260101T000000.000000000Z.tar.gz",
		}},
		{[]string{"move", ""}, []string{"Idea", "Inbox"}},
		{[]string{"move", "Idea", ""}, []string{"Projects", "Projects/Deep"}},
		{[]string{"related", "feedback", ""}, []string{"Idea", "Inbox"}},
		{[]string{"related", "feedback", "Idea", ""}, []string{"Idea", "Inbox"}},
		{[]string{"rename-key", ""}, []string{"status", "tags"}},
		{[]string{"rename-key", "status", ""}, nil},
		{[]string{"replace", ""}, []string{"status", "tags"}},
		{[]string{"find", "--relative-to", ""}, []string{"Idea", "Inbox"}},
		{[]string{"capture", "--note", ""}, []string{"Inbox.md", "Projects/Deep/Idea.md"}},
		{[]string{"config", "get", ""}, []string{configScriptableKey}},
		{[]string{"config", "set", configScriptableKey, ""}, nil},
		{[]string{"config", "unset", ""}, []string{configScriptableKey}},
	} {
		got := completeArgs(t, root, tc.args...)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("complete %v = %v, want %v", tc.args, got, tc.want)
		}
	}

	explain := completeArgs(t, root, "config", "explain", "")
	for _, key := range []string{"autosave.min_interval", "open.editor", "views"} {
		if !strings.Contains(","+strings.Join(explain, ",")+",", ","+key+",") {
			t.Errorf("config explain candidates %v lack %q", explain, key)
		}
	}
}

// undo completes journal IDs newest first, the order `bn history` prints.
func TestCompletion_UndoOffersJournalEntriesNewestFirst(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	note := filepath.Join(root, "a.md")
	writeCompletionFile(t, note, "before\n")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	makeJournalEntry(t, root, note, "20260102T030405.000000000Z-older", base, "bn set", "mid\n")
	makeJournalEntry(t, root, note, "20260102T030406.000000000Z-newer", base.Add(time.Second), "bn tag", "after\n")

	got := completeArgs(t, root, "undo", "")
	want := []string{"20260102T030406.000000000Z-newer", "20260102T030405.000000000Z-older"}
	if strings.Join(completionValues(got), ",") != strings.Join(want, ",") {
		t.Fatalf("undo candidates = %v, want %v", got, want)
	}
	if !strings.HasSuffix(got[0], "\tbn tag") {
		t.Errorf("undo candidate %q is not described by its command", got[0])
	}
	if extra := completeArgs(t, root, "undo", want[0], ""); len(extra) != 0 {
		t.Errorf("undo second argument candidates = %v, want none", extra)
	}
}

// Every new vault-backed completer fails quiet when the vault does not load.
func TestCompletion_PositionalUnloadableVaultCompletesNothing(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "[open\n")
	for _, args := range [][]string{
		{"views", "show", ""}, {"new", ""}, {"undo", ""}, {"restore", ""},
		{"move", "x", ""}, {"rename-key", ""}, {"capture", "--note", ""},
	} {
		if got := completeArgs(t, root, args...); len(got) != 0 {
			t.Errorf("complete %v = %v, want none", args, got)
		}
	}
}
