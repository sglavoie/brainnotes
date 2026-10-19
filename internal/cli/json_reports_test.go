package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigJSON(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	portable := useVaultRoot(t, root, "[open]\neditor = 'vim'\n")

	var summary map[string]any
	if err := runJSONCommand(t, root, &summary, "config", "--json"); err != nil {
		t.Fatal(err)
	}
	if summary["config"] != portable || summary["overlay"] != nil || summary["resolved_by"] != "--vault flag" {
		t.Fatalf("config --json = %v", summary)
	}

	var explained configExplainJSON
	if err := runJSONCommand(t, root, &explained, "config", "explain", "open.editor", "--json"); err != nil {
		t.Fatal(err)
	}
	if explained.Source == nil || *explained.Source != portable || explained.Value != "vim" {
		t.Fatalf("config explain open.editor --json = %+v", explained)
	}

	var defaulted map[string]any
	if err := runJSONCommand(t, root, &defaulted, "config", "explain", "fzf.args", "--json"); err != nil {
		t.Fatal(err)
	}
	if defaulted["source"] != nil {
		t.Fatalf("a built-in default has a null source: %v", defaulted)
	}
	if list, ok := defaulted["value"].([]any); !ok || len(list) != 0 {
		t.Fatalf("an empty list is [], not null: %v", defaulted)
	}
}

func TestViewsShowAndCheckJSON(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	isolateBnEnv(t)

	var shown viewShowJSON
	if err := runJSONCommand(t, root, &shown, "views", "show", "active work", "--json"); err != nil {
		t.Fatal(err)
	}
	if shown.Name != "Active work" || shown.Sort != "-modified" || shown.Limit != 20 ||
		shown.StoredQuery != "status:wip and not folder:Archive" || shown.Query == "" {
		t.Fatalf("views show --json = %+v", shown)
	}

	var report viewsCheckJSON
	err := runJSONCommand(t, root, &report, "views", "check", "--json")
	if err == nil {
		t.Fatal("views check --json must still fail when a view does not compile")
	}
	if report.Total != 3 || len(report.Failed) != 2 || report.Failed[0].Name != "Stale query" || report.Failed[0].Error == "" {
		t.Fatalf("views check --json = %+v", report)
	}
}

func TestIndexStatusJSON(t *testing.T) {
	root := buildSearchVault(t)
	isolateBnEnv(t)

	var before map[string]any
	if err := runJSONCommand(t, root, &before, "index", "--json"); err != nil {
		t.Fatal(err)
	}
	if before["status"] != "not built" || before["entries"] != nil {
		t.Fatalf("index --json before a build = %v", before)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes", "index")); err == nil {
		t.Fatal("index --json must not build the cache")
	}

	if _, err := runCLI(t, "", "--vault", root, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err := runJSONCommand(t, root, &after, "index", "--json"); err != nil {
		t.Fatal(err)
	}
	if after["status"] != "healthy" || after["entries"] != float64(4) || after["last_write"] == nil {
		t.Fatalf("index --json after a build = %v", after)
	}
}
