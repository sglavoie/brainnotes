package noteops

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
)

func synthesisTestVault(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	writeCreateTestFile(t, root, "brainnotes.toml", "ignore = [\"Archive\"]\n")
	writeCreateTestFile(t, root, "Anchor.md", "---\ntitle: Anchor\n---\nanchor body\n")
	writeCreateTestFile(t, root, "Refs/Supporter.md", "---\ntitle: Supporter\n---\nsupporter body\n")
	cfg := &config.Config{Vault: root, Ignore: []string{"Archive"}}
	cfg.Templates.Dir = "Templates"
	return root, cfg
}

func synthesisRequest(title string) SynthesisRequest {
	return SynthesisRequest{
		Title: title,
		Draft: "First paragraph.\n\nSecond paragraph.",
		Sources: []SynthesisSource{
			{RelPath: "Anchor.md", Title: "Anchor"},
			{RelPath: "Refs/Supporter.md", Title: "Supporter"},
		},
	}
}

func assertNoJournalEntries(t *testing.T, root string) {
	t.Helper()
	entries, warnings, err := journal.ListEntries(JournalRoot(root))
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("journal warnings = %v, want none", warnings)
	}
	if len(entries) != 0 {
		t.Fatalf("journal entries = %+v, want none", entries)
	}
}
