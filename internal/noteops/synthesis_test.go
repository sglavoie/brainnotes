package noteops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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

func TestPlanSynthesisNote_ExactBytes(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}

	want := "---\ntitle: Synthesis\n---\n" +
		"First paragraph.\n\nSecond paragraph.\n\n" +
		"## Sources\n" +
		"- [[Anchor|Anchor]]\n" +
		"- [[Refs/Supporter|Supporter]]\n"
	if string(plan.Content) != want {
		t.Fatalf("content = %q, want %q", string(plan.Content), want)
	}
	if plan.Destination.RelPath != "Synthesis.md" {
		t.Fatalf("destination = %q, want %q", plan.Destination.RelPath, "Synthesis.md")
	}
	if plan.Expect.Exists {
		t.Fatalf("plan must expect a missing destination, got %+v", plan.Expect)
	}
	if len(plan.Sources) != 2 || plan.Sources[0].RelPath != "Anchor.md" || plan.Sources[1].RelPath != "Refs/Supporter.md" {
		t.Fatalf("sources = %+v, want anchor then supporter", plan.Sources)
	}
	if len(plan.Requirements) != 2 {
		t.Fatalf("requirements = %+v, want one per source", plan.Requirements)
	}
	if _, err := os.Lstat(plan.Destination.AbsPath); !os.IsNotExist(err) {
		t.Fatalf("planning wrote the destination: %v", err)
	}
}

func TestPlanSynthesisNote_DraftNewlineNormalization(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Draft = "\n  \r\nkeep\r\n\r\nboth\r\n\n   \n"
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if !strings.Contains(string(plan.Content), "---\nkeep\n\nboth\n\n## Sources\n") {
		t.Fatalf("content = %q, want CRLF normalized and blank edge lines trimmed", string(plan.Content))
	}
}

func TestPlanSynthesisNote_TitleQuotingAndUnicode(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Yes: définitivement")
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if !strings.HasPrefix(string(plan.Content), "---\ntitle: 'Yes: définitivement'\n---\n") {
		t.Fatalf("content = %q, want the serializer to quote the colon", string(plan.Content))
	}
	if plan.Destination.RelPath != "Yes: définitivement.md" {
		t.Fatalf("destination = %q", plan.Destination.RelPath)
	}
}

func TestPlanSynthesisNote_UnicodeSourcePath(t *testing.T) {
	root, cfg := synthesisTestVault(t)
	writeCreateTestFile(t, root, "Réf/Étude.md", "body\n")

	req := synthesisRequest("Synthesis")
	req.Sources = []SynthesisSource{{RelPath: "Réf/Étude.md", Title: "Étude"}}
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if !strings.HasSuffix(string(plan.Content), "## Sources\n- [[Réf/Étude|Étude]]\n") {
		t.Fatalf("content = %q", string(plan.Content))
	}
}

func TestPlanSynthesisNote_UnsafeAliasFallsBackToPathOnly(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	for _, alias := range []string{"", "  Padded  ", "Pipe|Split", "Brack[et]", "Line\nBreak"} {
		req := synthesisRequest("Synthesis")
		req.Sources = []SynthesisSource{{RelPath: "Anchor.md", Title: alias}}
		plan, err := PlanSynthesisNote(root, cfg, req)
		if err != nil {
			t.Fatalf("alias %q: PlanSynthesisNote: %v", alias, err)
		}
		if !strings.HasSuffix(string(plan.Content), "## Sources\n- [[Anchor]]\n") {
			t.Fatalf("alias %q: content = %q, want a path-only wikilink", alias, string(plan.Content))
		}
	}
}

func TestPlanSynthesisNote_DuplicateSourcesRejected(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Sources = append(req.Sources, SynthesisSource{RelPath: "./Anchor.md", Title: "Anchor again"})
	_, err := PlanSynthesisNote(root, cfg, req)
	if CodeOf(err) != CodeInvalidRequest {
		t.Fatalf("code = %v (%v), want CodeInvalidRequest", CodeOf(err), err)
	}
}

func TestPlanSynthesisNote_DuplicateTitlesAllowedInSources(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Sources = []SynthesisSource{
		{RelPath: "Anchor.md", Title: "Same"},
		{RelPath: "Refs/Supporter.md", Title: "Same"},
	}
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if !strings.HasSuffix(string(plan.Content), "- [[Anchor|Same]]\n- [[Refs/Supporter|Same]]\n") {
		t.Fatalf("content = %q", string(plan.Content))
	}
}

func TestPlanSynthesisNote_NoSourcesRejected(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Sources = nil
	_, err := PlanSynthesisNote(root, cfg, req)
	if CodeOf(err) != CodeInvalidRequest {
		t.Fatalf("code = %v (%v), want CodeInvalidRequest", CodeOf(err), err)
	}
}

func TestPlanSynthesisNote_MissingSourceRejected(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Sources = []SynthesisSource{{RelPath: "Nope.md"}}
	if _, err := PlanSynthesisNote(root, cfg, req); err == nil {
		t.Fatal("expected an error for a missing source")
	}
}

func TestPlanSynthesisNote_DraftValidation(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	cases := map[string]string{
		"empty":                       "",
		"blank":                       "   \n\t\n",
		"invalid utf-8":               "bad \xff byte",
		"reserved heading":            "intro\n\n## Sources\n- [[Anchor]]\n",
		"reserved heading other case": "intro\n\n##   sources   \n",
	}
	for name, draft := range cases {
		req := synthesisRequest("Synthesis")
		req.Draft = draft
		_, err := PlanSynthesisNote(root, cfg, req)
		if CodeOf(err) != CodeInvalidRequest {
			t.Fatalf("%s: code = %v (%v), want CodeInvalidRequest", name, CodeOf(err), err)
		}
	}
}

func TestPlanSynthesisNote_FencedSourcesHeadingAllowed(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Draft = "intro\n\n```markdown\n## Sources\n- [[Example]]\n```\n"
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if strings.Count(string(plan.Content), "## Sources") != 2 {
		t.Fatalf("content = %q, want the fenced heading kept plus the real section", string(plan.Content))
	}
}

func TestPlanSynthesisNote_OversizedDraftRejected(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Draft = strings.Repeat("a", MaxSynthesisDraftBytes+1)
	_, err := PlanSynthesisNote(root, cfg, req)
	if CodeOf(err) != CodeInvalidRequest {
		t.Fatalf("code = %v (%v), want CodeInvalidRequest", CodeOf(err), err)
	}

	req.Draft = strings.Repeat("a", MaxSynthesisDraftBytes)
	if _, err := PlanSynthesisNote(root, cfg, req); err != nil {
		t.Fatalf("draft at the limit: %v", err)
	}
}

func TestPlanSynthesisNote_Folders(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	t.Run("root", func(t *testing.T) {
		req := synthesisRequest("AtRoot")
		req.Folder = ""
		plan, err := PlanSynthesisNote(root, cfg, req)
		if err != nil {
			t.Fatalf("PlanSynthesisNote: %v", err)
		}
		if plan.Destination.RelPath != "AtRoot.md" {
			t.Fatalf("destination = %q", plan.Destination.RelPath)
		}
	})

	t.Run("existing nested", func(t *testing.T) {
		req := synthesisRequest("Nested")
		req.Folder = "Refs"
		plan, err := PlanSynthesisNote(root, cfg, req)
		if err != nil {
			t.Fatalf("PlanSynthesisNote: %v", err)
		}
		if plan.Destination.RelPath != "Refs/Nested.md" {
			t.Fatalf("destination = %q", plan.Destination.RelPath)
		}
	})

	t.Run("new folder", func(t *testing.T) {
		req := synthesisRequest("Fresh")
		req.Folder = "New/Deeper"
		plan, err := PlanSynthesisNote(root, cfg, req)
		if err != nil {
			t.Fatalf("PlanSynthesisNote: %v", err)
		}
		if plan.Destination.RelPath != "New/Deeper/Fresh.md" {
			t.Fatalf("destination = %q", plan.Destination.RelPath)
		}
	})

	t.Run("ignored", func(t *testing.T) {
		req := synthesisRequest("Ignored")
		req.Folder = "Archive"
		_, err := PlanSynthesisNote(root, cfg, req)
		if CodeOf(err) != CodeForbidden {
			t.Fatalf("code = %v (%v), want CodeForbidden", CodeOf(err), err)
		}
	})

	t.Run("templates", func(t *testing.T) {
		req := synthesisRequest("Templated")
		req.Folder = "templates"
		_, err := PlanSynthesisNote(root, cfg, req)
		if CodeOf(err) != CodeForbidden {
			t.Fatalf("code = %v (%v), want CodeForbidden", CodeOf(err), err)
		}
	})

	t.Run("hidden", func(t *testing.T) {
		req := synthesisRequest("Hidden")
		req.Folder = ".bnotes"
		_, err := PlanSynthesisNote(root, cfg, req)
		if CodeOf(err) != CodeForbidden {
			t.Fatalf("code = %v (%v), want CodeForbidden", CodeOf(err), err)
		}
	})

	t.Run("escaping", func(t *testing.T) {
		for _, folder := range []string{"..", "../outside", "/etc"} {
			req := synthesisRequest("Escaped")
			req.Folder = folder
			_, err := PlanSynthesisNote(root, cfg, req)
			if err == nil {
				t.Fatalf("folder %q: expected an error", folder)
			}
			if code := CodeOf(err); code != CodeForbidden && code != CodeInvalidRequest {
				t.Fatalf("folder %q: code = %v (%v)", folder, code, err)
			}
		}
	})

	t.Run("symlinked", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs privileges on windows")
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "Linked")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		req := synthesisRequest("Linked")
		req.Folder = "Linked"
		_, err := PlanSynthesisNote(root, cfg, req)
		if err == nil {
			t.Fatal("expected an error for a symlinked folder")
		}
		if code := CodeOf(err); code != CodeForbidden {
			t.Fatalf("code = %v (%v), want CodeForbidden", code, err)
		}
	})
}

func TestPlanSynthesisNote_TitleValidation(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	for _, title := range []string{"", "   ", " Padded", "With/Slash", ".hidden", "Bracket[[x", "Pipe|x", "Hash#x"} {
		req := synthesisRequest(title)
		_, err := PlanSynthesisNote(root, cfg, req)
		if CodeOf(err) != CodeInvalidRequest {
			t.Fatalf("title %q: code = %v (%v), want CodeInvalidRequest", title, CodeOf(err), err)
		}
	}
}

func TestPlanSynthesisNote_ExistingDestinationRejected(t *testing.T) {
	root, cfg := synthesisTestVault(t)
	writeCreateTestFile(t, root, "Taken.md", "existing\n")

	_, err := PlanSynthesisNote(root, cfg, synthesisRequest("Taken"))
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	assertCreateTestFile(t, root, "Taken.md", "existing\n")
}

func TestPlanSynthesisNote_StaleSourceHashRejected(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Synthesis")
	req.Sources[0].SHA256 = vault.HashBytes([]byte("something else"))
	_, err := PlanSynthesisNote(root, cfg, req)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
}

func TestApplySynthesisNote_CreatesNoteAndJournal(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	result, err := ApplySynthesisNote(root, plan)
	if err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}
	assertCreateTestFile(t, root, "Synthesis.md", string(plan.Content))
	if string(result.Content) != string(plan.Content) {
		t.Fatal("result content diverged from the plan")
	}
	if result.Journal == nil {
		t.Fatal("expected a journal entry")
	}
	if result.Journal.Command != "bn web synthesize" {
		t.Fatalf("command = %q, want %q", result.Journal.Command, "bn web synthesize")
	}
	record := createRecordForPath(t, *result.Journal, "Synthesis.md")
	if record.Before.Kind != journal.KindMissing {
		t.Fatalf("before = %v, want missing", record.Before.Kind)
	}
	if record.After.Kind != journal.KindFile || record.After.SHA256 != vault.HashBytes(plan.Content) {
		t.Fatalf("after = %+v, want the created bytes", record.After)
	}
}

func TestApplySynthesisNote_CreatesMissingFolder(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Deep")
	req.Folder = "New/Deeper"
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if _, err := ApplySynthesisNote(root, plan); err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}
	assertCreateTestFile(t, root, "New/Deeper/Deep.md", string(plan.Content))
}

func TestApplySynthesisNote_ConcurrentDestinationLeavesNothing(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	writeCreateTestFile(t, root, "Synthesis.md", "written by someone else\n")

	_, err = ApplySynthesisNote(root, plan)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	assertCreateTestFile(t, root, "Synthesis.md", "written by someone else\n")
	assertNoJournalEntries(t, root)
}

func TestApplySynthesisNote_SourceConflictLeavesNothing(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	anchor, err := os.ReadFile(filepath.Join(root, "Anchor.md"))
	if err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	req := synthesisRequest("Synthesis")
	req.Sources[0].SHA256 = vault.HashBytes(anchor)
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	writeCreateTestFile(t, root, "Anchor.md", "edited after planning\n")

	_, err = ApplySynthesisNote(root, plan)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Synthesis.md")); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
	assertNoJournalEntries(t, root)
}

func TestApplySynthesisNote_ConfigConflictLeavesNothing(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	tomlBytes, err := os.ReadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	req := synthesisRequest("Synthesis")
	req.Requires = []SynthesisRequirement{{Path: config.FileName, Expect: vault.ExpectHash(vault.HashBytes(tomlBytes))}}
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if len(plan.Requirements) != 3 {
		t.Fatalf("requirements = %+v, want the sources plus the config", plan.Requirements)
	}
	writeCreateTestFile(t, root, config.FileName, "ignore = []\n")

	_, err = ApplySynthesisNote(root, plan)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Synthesis.md")); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
	assertNoJournalEntries(t, root)
}

func TestApplySynthesisNote_RejectsNonMissingExpectation(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	plan.Expect = vault.ExpectAnyContent()
	if _, err := ApplySynthesisNote(root, plan); CodeOf(err) != CodeInvalidRequest {
		t.Fatalf("code = %v (%v), want CodeInvalidRequest", CodeOf(err), err)
	}
}

func TestApplySynthesisNote_UndoRemovesTheNote(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	result, err := ApplySynthesisNote(root, plan)
	if err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}

	if _, err := journal.Restore(root, JournalRoot(root), result.Journal.ID, journal.RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Lstat(plan.Destination.AbsPath); !os.IsNotExist(err) {
		t.Fatalf("undo left the note behind: %v", err)
	}
}

func TestApplySynthesisNote_UndoRefusesChangedRevision(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	result, err := ApplySynthesisNote(root, plan)
	if err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}
	writeCreateTestFile(t, root, "Synthesis.md", "edited by hand\n")

	_, err = journal.Restore(root, JournalRoot(root), result.Journal.ID, journal.RestoreOptions{})
	if !journal.IsRestoreConflict(err) {
		t.Fatalf("err = %v, want a restore conflict", err)
	}
	assertCreateTestFile(t, root, "Synthesis.md", "edited by hand\n")
}

func assertNoJournalEntries(t *testing.T, root string) {
	t.Helper()
	entries, err := journal.ListEntries(JournalRoot(root))
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("journal entries = %+v, want none", entries)
	}
}
